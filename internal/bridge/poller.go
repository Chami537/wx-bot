package bridge

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

const (
	defaultPollTimeout = 35 * time.Second
	retryDelay         = 2 * time.Second
	backoffDelay       = 30 * time.Second
	maxConsecFailures  = 3
)

// Poller long-polls iLink getupdates for one binding and queues each
// message in the inbox. The cursor advances only after the whole batch is
// queued, so a failure replays the batch (the inbox ignores duplicates).
type Poller struct {
	Name             string
	Client           *ilink.Client
	Store            *state.Store
	Allow            Allowlist
	ForwardUnallowed bool
	Inbox            *inbox.Inbox
	MediaDir         string
	Log              *slog.Logger

	// DefaultTimeout is the getupdates long-poll window (default 35s).
	DefaultTimeout time.Duration
	RetryDelay     time.Duration
	BackoffDelay   time.Duration
	MaxFailures    int

	pollClient *http.Client
}

func (p *Poller) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default().With("component", "poller", "binding", p.Name)
}

func (p *Poller) timeout() time.Duration {
	if p.DefaultTimeout > 0 {
		return p.DefaultTimeout
	}
	return defaultPollTimeout
}

func (p *Poller) retry() time.Duration {
	if p.RetryDelay > 0 {
		return p.RetryDelay
	}
	return retryDelay
}

func (p *Poller) backoff() time.Duration {
	if p.BackoffDelay > 0 {
		return p.BackoffDelay
	}
	return backoffDelay
}

func (p *Poller) maxFail() int {
	if p.MaxFailures > 0 {
		return p.MaxFailures
	}
	return maxConsecFailures
}

func (p *Poller) httpPoll() *http.Client {
	if p.pollClient != nil {
		return p.pollClient
	}
	// Timeout is per-request via context. Disable keep-alives so a cancelled
	// long-poll does not leave the server connection hanging (important in tests).
	p.pollClient = &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	return p.pollClient
}

// Run starts the polling loop. Blocks until ctx is cancelled or the session expires (-14).
func (p *Poller) Run(ctx context.Context) {
	log := p.log()
	log.Info("poller start")
	defer log.Info("poller stop")

	cursor, err := p.Store.LoadCursor()
	if err != nil {
		log.Warn("load cursor", "err", err)
		cursor = ""
	}
	pollTimeout := p.timeout()
	consecFailures := 0

	for {
		if ctx.Err() != nil {
			return
		}

		resp, err := p.fetchUpdates(ctx, cursor, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecFailures++
			log.Warn("getupdates error", "err", err, "failures", consecFailures)

			delay := p.retry()
			if consecFailures >= p.maxFail() {
				delay = p.backoff()
				log.Warn("poller backoff", "delay", delay.String())
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}

		if resp.ErrCode == ilink.ErrCodeExpired || resp.Ret == ilink.ErrCodeExpired {
			log.Error("binding session expired: run `wx-bot login "+p.Name+"`", "binding", p.Name, "errcode", resp.ErrCode, "ret", resp.Ret)
			if err := p.Store.MarkExpired(); err != nil {
				log.Error("write expired marker", "err", err)
			}
			return
		}

		// Business error: keep the cursor and messages untouched, retry like a transport error.
		if resp.Ret != 0 || resp.ErrCode != 0 {
			consecFailures++
			log.Warn("getupdates business error; cursor unchanged", "ret", resp.Ret, "errcode", resp.ErrCode, "failures", consecFailures)
			delay := p.retry()
			if consecFailures >= p.maxFail() {
				delay = p.backoff()
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}

		consecFailures = 0

		if resp.LongpollingTimeoutMs > 0 {
			pollTimeout = time.Duration(resp.LongpollingTimeoutMs) * time.Millisecond
		}

		queued := true
		for _, msg := range resp.Msgs {
			if err := p.handleMessage(ctx, msg); err != nil {
				log.Error("queue message failed; cursor unchanged", "msg_id", msg.MessageID, "err", err)
				queued = false
				break
			}
		}
		if !queued {
			select {
			case <-time.After(p.retry()):
			case <-ctx.Done():
				return
			}
			continue
		}

		if resp.GetUpdatesBuf != "" {
			if err := p.Store.SaveCursor(resp.GetUpdatesBuf); err != nil {
				log.Error("save cursor", "err", err)
			}
			cursor = resp.GetUpdatesBuf
		}
	}
}

func (p *Poller) fetchUpdates(ctx context.Context, cursor string, timeout time.Duration) (*ilink.GetUpdatesResponse, error) {
	body := ilink.GetUpdatesRequest{
		GetUpdatesBuf: cursor,
		BaseInfo:      ilink.BaseInfo{ChannelVersion: ilink.ChannelVersion},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal getupdates: %w", err)
	}

	reqURL := p.Client.BaseURL() + "/ilink/bot/getupdates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build getupdates request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.Client.SetAuthHeaders(req)

	pollCtx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	req = req.WithContext(pollCtx)

	resp, err := p.httpPoll().Do(req)
	if err != nil {
		if pollCtx.Err() != nil && ctx.Err() == nil {
			return &ilink.GetUpdatesResponse{}, nil
		}
		return nil, fmt.Errorf("getupdates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getupdates: status %s", resp.Status)
	}

	var r ilink.GetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode getupdates: %w", err)
	}
	return &r, nil
}

// handleMessage queues one message. It returns an error only when the message
// could not be queued and the batch must be replayed.
func (p *Poller) handleMessage(ctx context.Context, msg ilink.IncomingMessage) error {
	wxid := msg.FromUserID
	log := p.log().With("wxid", wxid, "msg_id", msg.MessageID)

	allowed := p.Allow.Contains(wxid)
	if !allowed {
		log.Info("unallowed sender")
		if !p.ForwardUnallowed {
			return nil
		}
	}

	if msg.ContextToken != "" && allowed {
		p.Client.SetToken(wxid, msg.ContextToken)
		if err := p.Store.SaveTokens(p.Client.Tokens()); err != nil {
			log.Error("save context tokens", "err", err)
		}
	}

	payload, err := p.buildPayload(ctx, msg)
	if err != nil {
		return err
	}
	body, err := EncodePayload(payload)
	if err != nil {
		return err
	}
	added, err := p.Inbox.Add(ctx, p.Name, payload.EventID, body)
	if err != nil {
		return err
	}
	if !added {
		log.Debug("duplicate message ignored")
	}
	return nil
}

func (p *Poller) buildPayload(ctx context.Context, msg ilink.IncomingMessage) (Payload, error) {
	_ = ctx
	var texts []string
	var voiceText string
	var media []MediaRef
	var mediaErrs []string

	msgid := strconv.FormatInt(msg.MessageID, 10)
	created := time.UnixMilli(msg.CreateTimeMs)
	if msg.CreateTimeMs == 0 {
		created = time.Now()
	}

	for i, item := range msg.ItemList {
		switch item.Type {
		case ilink.ItemTypeText:
			if item.TextItem != nil && item.TextItem.Text != "" {
				texts = append(texts, item.TextItem.Text)
			}

		case ilink.ItemTypeVoice:
			if item.VoiceItem != nil {
				if item.VoiceItem.Text != "" {
					voiceText = item.VoiceItem.Text
					continue
				}
				ref, err := p.saveMedia(item.VoiceItem.Media, nil, msgid, defaultMediaName(item.Type, ""), created, item.Type, i)
				if err != nil {
					p.log().Warn("download voice", "err", err)
					mediaErrs = append(mediaErrs, fmt.Sprintf("voice: %v", err))
				} else {
					media = append(media, ref)
				}
			}

		case ilink.ItemTypeImage:
			if item.ImageItem != nil {
				var keyOverride []byte
				if item.ImageItem.AESKey != "" {
					if k, err := hex.DecodeString(item.ImageItem.AESKey); err == nil {
						keyOverride = k
					}
				}
				ref, err := p.saveMedia(item.ImageItem.Media, keyOverride, msgid, defaultMediaName(item.Type, ""), created, item.Type, i)
				if err != nil {
					p.log().Warn("download image", "err", err)
					mediaErrs = append(mediaErrs, fmt.Sprintf("image: %v", err))
				} else {
					media = append(media, ref)
				}
			}

		case ilink.ItemTypeFile:
			if item.FileItem != nil {
				name := item.FileItem.FileName
				ref, err := p.saveMedia(item.FileItem.Media, nil, msgid, defaultMediaName(item.Type, name), created, item.Type, i)
				if err != nil {
					p.log().Warn("download file", "err", err, "name", name)
					mediaErrs = append(mediaErrs, fmt.Sprintf("file %s: %v", name, err))
				} else {
					media = append(media, ref)
				}
			}

		case ilink.ItemTypeVideo:
			if item.VideoItem != nil {
				ref, err := p.saveMedia(item.VideoItem.Media, nil, msgid, defaultMediaName(item.Type, ""), created, item.Type, i)
				if err != nil {
					p.log().Warn("download video", "err", err)
					mediaErrs = append(mediaErrs, fmt.Sprintf("video: %v", err))
				} else {
					media = append(media, ref)
				}
			}
		}
	}

	toBot := msg.ToUserID
	if toBot == "" {
		toBot = p.Client.BotID()
	}
	createTime := msg.CreateTimeMs / 1000
	if createTime == 0 && msg.CreateTimeMs != 0 {
		createTime = msg.CreateTimeMs
	}

	if media == nil {
		media = []MediaRef{}
	}

	return Payload{
		Schema:          EventSchema,
		EventID:         p.Name + ":" + msgid,
		Binding:         p.Name,
		MessageID:       msgid,
		FromWxid:        msg.FromUserID,
		ToBotID:         toBot,
		CreateTime:      createTime,
		Text:            strings.Join(texts, "\n"),
		VoiceText:       voiceText,
		Media:           media,
		MediaErrors:     mediaErrs,
		HasContextToken: msg.ContextToken != "" || p.Client.ContextToken(msg.FromUserID) != "",
		ReplyHint:       fmt.Sprintf("wx-bot send --binding %s --to %s --text - <<'EOF'\n<reply>\nEOF", p.Name, msg.FromUserID),
	}, nil
}

func (p *Poller) saveMedia(media ilink.CDNMedia, keyOverride []byte, msgid, filename string, t time.Time, itemType, idx int) (MediaRef, error) {
	name := defaultMediaName(itemType, filename)
	if idx > 0 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		name = fmt.Sprintf("%s_%d%s", base, idx, ext)
	}
	dest := mediaDest(p.MediaDir, p.Name, msgid, name, t)
	if err := p.Client.DownloadMedia(media, keyOverride, dest); err != nil {
		return MediaRef{}, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return MediaRef{}, err
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		abs = dest
	}
	return MediaRef{
		Type:     mediaTypeName(itemType),
		Path:     abs,
		FileName: ilink.SanitizeFilename(name),
		Size:     info.Size(),
	}, nil
}
