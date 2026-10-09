package ilink

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrExpired is returned when iLink reports errcode/ret -14.
var ErrExpired = errors.New("ilink session expired (errcode -14)")

// Client sends messages to WeChat via the iLink bot API.
type Client struct {
	baseURL    string // from Session.BaseURL
	token      string // bot_token from Session
	botID      string // ilink_bot_id (used as from_user_id on outbound messages)
	cdnBaseURL string // WeChat C2C CDN base (DefaultCDNBase unless overridden)
	httpClient *http.Client
	log        *slog.Logger

	// tokenCache stores the latest context_token per wxid.
	// Updated by the poller on every incoming message; read by send methods.
	tokenCache sync.Map // map[string]string: wxid → context_token
}

// NewClient creates a Client from an authenticated Session.
func NewClient(sess *Session) *Client {
	return NewClientWithHTTP(sess, nil)
}

// NewClientWithHTTP creates a Client with a custom HTTP client (for tests).
func NewClientWithHTTP(sess *Session, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(sess.BaseURL, "/"),
		token:      sess.BotToken,
		botID:      sess.BotID,
		cdnBaseURL: DefaultCDNBase,
		httpClient: hc,
		log:        slog.Default().With("component", "ilink"),
	}
}

// uploadTimeout bounds a single CDN upload request (large videos).
const uploadTimeout = 5 * time.Minute

// SetCDNBaseURL overrides the CDN base URL (tests / custom deployments).
func (c *Client) SetCDNBaseURL(u string) { c.cdnBaseURL = strings.TrimRight(u, "/") }

// BotID returns the iLink bot id (from_user_id).
func (c *Client) BotID() string { return c.botID }

// BaseURL returns the session API base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// SetAuthHeaders attaches iLink app + bot_token headers to req.
func (c *Client) SetAuthHeaders(req *http.Request) {
	AddAppHeaders(req, c.token)
}

// SetToken stores the latest context_token for a wxid.
func (c *Client) SetToken(wxid, token string) {
	c.tokenCache.Store(wxid, token)
}

// ContextToken returns the cached context_token for wxid, or "".
func (c *Client) ContextToken(wxid string) string {
	if v, ok := c.tokenCache.Load(wxid); ok {
		return v.(string)
	}
	return ""
}

// LoadTokens seeds the context_token cache from persisted state.
func (c *Client) LoadTokens(m map[string]string) {
	for wxid, tok := range m {
		if wxid != "" && tok != "" {
			c.tokenCache.Store(wxid, tok)
		}
	}
}

// Tokens returns a snapshot of the context_token cache.
func (c *Client) Tokens() map[string]string {
	out := make(map[string]string)
	c.tokenCache.Range(func(k, v any) bool {
		out[k.(string)] = v.(string)
		return true
	})
	return out
}

// SendText sends a plain text message to wxid, auto-splitting at MaxTextRunes.
func (c *Client) SendText(ctx context.Context, wxid, text string) error {
	chunks := SplitText(text, MaxTextRunes)
	for _, chunk := range chunks {
		if err := c.sendTextChunk(ctx, wxid, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) sendTextChunk(ctx context.Context, wxid, text string) error {
	req := SendMessageRequest{
		Msg: OutgoingMessage{
			FromUserID:   c.botID,
			ToUserID:     wxid,
			ClientID:     newClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: c.ContextToken(wxid),
			ItemList: []MessageItem{
				{Type: ItemTypeText, TextItem: &TextItem{Text: text}},
			},
		},
		BaseInfo: BaseInfo{ChannelVersion: ChannelVersion},
	}
	return c.post(ctx, "/ilink/bot/sendmessage", req, nil)
}

// SendImage uploads a local image and sends it to wxid.
func (c *Client) SendImage(ctx context.Context, wxid, filePath, caption string) error {
	return c.sendMedia(ctx, wxid, filePath, caption, ItemTypeImage)
}

// SendVideo uploads a local video and sends it to wxid.
func (c *Client) SendVideo(ctx context.Context, wxid, filePath, caption string) error {
	return c.sendMedia(ctx, wxid, filePath, caption, ItemTypeVideo)
}

// SendFile uploads a local file and sends it to wxid.
func (c *Client) SendFile(ctx context.Context, wxid, filePath, caption string) error {
	return c.sendMedia(ctx, wxid, filePath, caption, mediaTypeFromPath(filePath))
}

// uploadMediaType maps a message item type to the getuploadurl media_type.
// The two enums use different numbering (image item=2 but upload type=1, etc).
func uploadMediaType(itemType int) int {
	switch itemType {
	case ItemTypeImage:
		return UploadMediaImage
	case ItemTypeVideo:
		return UploadMediaVideo
	case ItemTypeVoice:
		return UploadMediaVoice
	default:
		return UploadMediaFile
	}
}

// aesECBPaddedSize is the AES-128-ECB/PKCS7 ciphertext size for n plaintext bytes.
func aesECBPaddedSize(n int64) int64 { return (n/16 + 1) * 16 }

// uploadedMedia is the result of a successful CDN upload.
type uploadedMedia struct {
	filekey       string
	downloadParam string // x-encrypted-param from the CDN response
	aesKeyHex     string
	rawSize       int64
	cipherSize    int64
}

// cdnMedia builds the CDNMedia reference for sendmessage. Matching the official
// plugin, aes_key is base64 of the 32-char hex key string (not of the raw bytes).
func (u *uploadedMedia) cdnMedia() CDNMedia {
	return CDNMedia{
		EncryptQueryParam: u.downloadParam,
		AESKey:            base64.StdEncoding.EncodeToString([]byte(u.aesKeyHex)),
		EncryptType:       1,
	}
}

func (c *Client) uploadMedia(ctx context.Context, wxid string, data []byte, itemType int) (*uploadedMedia, error) {
	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return nil, fmt.Errorf("generate aes key: %w", err)
	}
	fk := make([]byte, 16)
	if _, err := rand.Read(fk); err != nil {
		return nil, fmt.Errorf("generate filekey: %w", err)
	}
	encrypted, err := EncryptAES128ECB(data, aesKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt file: %w", err)
	}
	u := &uploadedMedia{
		filekey:    hex.EncodeToString(fk),
		aesKeyHex:  hex.EncodeToString(aesKey),
		rawSize:    int64(len(data)),
		cipherSize: aesECBPaddedSize(int64(len(data))),
	}

	uploadReq := GetUploadURLRequest{
		Filekey:     u.filekey,
		MediaType:   uploadMediaType(itemType),
		ToUserID:    wxid,
		Rawsize:     u.rawSize,
		RawFileMD5:  md5Hex(data),
		Filesize:    u.cipherSize,
		NoNeedThumb: true,
		AESKey:      u.aesKeyHex,
		BaseInfo:    BaseInfo{ChannelVersion: ChannelVersion},
	}
	var uploadResp GetUploadURLResponse
	if err := c.post(ctx, "/ilink/bot/getuploadurl", uploadReq, &uploadResp); err != nil {
		return nil, fmt.Errorf("get upload url: %w", err)
	}
	if uploadResp.Ret != 0 {
		return nil, fmt.Errorf("get upload url: ret=%d %s", uploadResp.Ret, uploadResp.ErrMsg)
	}

	uploadURL := strings.TrimSpace(uploadResp.UploadFullURL)
	if uploadURL == "" {
		if uploadResp.UploadParam == "" {
			return nil, fmt.Errorf("get upload url: response has neither upload_full_url nor upload_param")
		}
		uploadURL = buildCdnUploadURL(c.cdnBaseURL, uploadResp.UploadParam, u.filekey)
	}
	dl, err := c.uploadBytes(ctx, uploadURL, encrypted)
	if err != nil {
		return nil, fmt.Errorf("upload to cdn: %w", err)
	}
	u.downloadParam = dl
	return u, nil
}

func (c *Client) sendMedia(ctx context.Context, wxid, filePath, caption string, itemType int) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	if int64(len(data)) > maxMediaBytes {
		return fmt.Errorf("file too large (>100 MB)")
	}

	u, err := c.uploadMedia(ctx, wxid, data, itemType)
	if err != nil {
		return err
	}

	var item MessageItem
	switch itemType {
	case ItemTypeImage:
		item = MessageItem{Type: ItemTypeImage, ImageItem: &ImageItem{Media: u.cdnMedia(), MidSize: u.cipherSize}}
	case ItemTypeVideo:
		item = MessageItem{Type: ItemTypeVideo, VideoItem: &VideoItem{Media: u.cdnMedia(), VideoSize: u.cipherSize}}
	default:
		item = MessageItem{Type: ItemTypeFile, FileItem: &FileItem{
			Media:    u.cdnMedia(),
			FileName: filepath.Base(filePath),
			Len:      fmt.Sprintf("%d", len(data)),
		}}
	}

	// Like the official plugin: caption goes first as its own TEXT message.
	if caption != "" {
		if err := c.sendTextChunk(ctx, wxid, caption); err != nil {
			return fmt.Errorf("send caption: %w", err)
		}
	}

	sendReq := SendMessageRequest{
		Msg: OutgoingMessage{
			FromUserID:   c.botID,
			ToUserID:     wxid,
			ClientID:     newClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: c.ContextToken(wxid),
			ItemList:     []MessageItem{item},
		},
		BaseInfo: BaseInfo{ChannelVersion: ChannelVersion},
	}
	return c.post(ctx, "/ilink/bot/sendmessage", sendReq, nil)
}

// DownloadMedia downloads and decrypts a CDN media item to destPath (0600).
// Parent directories are created with 0700.
// aesKeyOverride, if non-nil, is used instead of media.AESKey (used for images
// where the key comes from ImageItem.AESKey as a hex string).
func (c *Client) DownloadMedia(media CDNMedia, aesKeyOverride []byte, destPath string) error {
	var aesKeyBytes []byte
	if len(aesKeyOverride) > 0 {
		aesKeyBytes = aesKeyOverride
	} else {
		var err error
		aesKeyBytes, err = ParseAESKey(media.AESKey)
		if err != nil {
			return fmt.Errorf("decode aes_key: %w", err)
		}
	}

	var cdnURL string
	if media.FullURL != "" {
		cdnURL = media.FullURL
	} else if media.EncryptQueryParam != "" {
		cdnURL = buildCdnDownloadURL(c.cdnBaseURL, media.EncryptQueryParam)
	} else {
		return fmt.Errorf("CDNMedia has neither full_url nor encrypt_query_param")
	}

	req, err := http.NewRequest(http.MethodGet, cdnURL, nil)
	if err != nil {
		return fmt.Errorf("build cdn request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cdn download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cdn download: status %s", resp.Status)
	}

	limited := io.LimitReader(resp.Body, maxMediaBytes+1)
	encrypted, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read cdn response: %w", err)
	}
	if int64(len(encrypted)) > maxMediaBytes {
		return fmt.Errorf("media too large (>100 MB)")
	}

	plaintext, err := DecryptAES128ECB(encrypted, aesKeyBytes)
	if err != nil {
		return fmt.Errorf("decrypt media: %w", err)
	}

	if err := mkdirAll0700(filepath.Dir(destPath)); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	if err := writeFile0600(destPath, plaintext); err != nil {
		return fmt.Errorf("write media: %w", err)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	AddAppHeaders(req, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ilink request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ilink request %s: status %s", path, resp.Status)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response %s: %w", path, err)
	}

	var probe struct {
		Ret     int `json:"ret"`
		ErrCode int `json:"errcode"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &probe)
		if probe.ErrCode == ErrCodeExpired || probe.Ret == ErrCodeExpired {
			return fmt.Errorf("%s: %w", path, ErrExpired)
		}
		if out == nil && probe.Ret != 0 {
			return fmt.Errorf("%s: ret=%d", path, probe.Ret)
		}
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// uploadBytes POSTs ciphertext to the CDN and returns the x-encrypted-param
// header (the download encrypt_query_param for sendmessage).
func (c *Client) uploadBytes(ctx context.Context, uploadURL string, data []byte) (string, error) {
	hc := *c.httpClient
	hc.Timeout = uploadTimeout
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return "", fmt.Errorf("cdn client error %s: %s", resp.Status, resp.Header.Get("x-error-message"))
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("cdn server error %s: %s", resp.Status, resp.Header.Get("x-error-message"))
			continue
		}
		dl := resp.Header.Get("x-encrypted-param")
		if dl == "" {
			lastErr = fmt.Errorf("cdn response missing x-encrypted-param header")
			continue
		}
		return dl, nil
	}
	return "", lastErr
}

// buildCdnUploadURL matches official cdn/cdn-url.ts buildCdnUploadUrl.
func buildCdnUploadURL(cdnBaseURL, uploadParam, filekey string) string {
	return cdnBaseURL + "/upload?encrypted_query_param=" + url.QueryEscape(uploadParam) +
		"&filekey=" + url.QueryEscape(filekey)
}

// buildCdnDownloadURL matches official cdn/cdn-url.ts buildCdnDownloadUrl.
func buildCdnDownloadURL(cdnBaseURL, encryptedQueryParam string) string {
	return cdnBaseURL + "/download?encrypted_query_param=" + url.QueryEscape(encryptedQueryParam)
}

func md5Hex(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

func newClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "wxbot-" + hex.EncodeToString(b)
}

func mediaTypeFromPath(path string) int {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return ItemTypeImage
	case ".mp4", ".mov", ".avi", ".mkv":
		return ItemTypeVideo
	default:
		return ItemTypeFile
	}
}

// SanitizeFilename keeps [A-Za-z0-9._-] and replaces everything else with '_'.
func SanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." {
		return "attachment"
	}
	var sb strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	result := sb.String()
	if result == "" || result == "." {
		return "attachment"
	}
	return result
}

func mkdirAll0700(path string) error {
	if path == "" || path == "." {
		return nil
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func writeFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return os.Chmod(path, 0o600)
}
