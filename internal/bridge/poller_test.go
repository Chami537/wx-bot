package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

type hookHit struct {
	Header http.Header
	Body   []byte
}

func closeServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
}

func startWebhook(t *testing.T, hits chan hookHit) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits <- hookHit{Header: r.Header.Clone(), Body: b}
		w.WriteHeader(http.StatusOK)
	}))
	closeServer(t, srv)
	return srv
}

type ilinkScript struct {
	mu      sync.Mutex
	resps   []ilink.GetUpdatesResponse
	i       int
	headers []http.Header
}

func (s *ilinkScript) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ilink/bot/getupdates" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.headers = append(s.headers, r.Header.Clone())
	if s.i < len(s.resps) {
		resp := s.resps[s.i]
		s.i++
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	s.mu.Unlock()
	select {
	case <-r.Context().Done():
	case <-time.After(50 * time.Millisecond):
		_ = json.NewEncoder(w).Encode(ilink.GetUpdatesResponse{})
	}
}

func saveSession(t *testing.T, dir, binding, token, botID, userID, base string) *state.Store {
	t.Helper()
	st, err := state.Open(dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSession(&state.Session{
		BotToken:  token,
		BaseURL:   base,
		BotID:     botID,
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPollerWebhookAuthHMACMediaAllowlistCursor(t *testing.T) {
	key := []byte("0123456789abcdef")
	plain := []byte("fake-jpeg-bytes")
	ct, err := ilink.EncryptAES128ECB(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(ct)
	}))
	closeServer(t, cdn)

	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		GetUpdatesBuf: "cursor-42",
		Msgs: []ilink.IncomingMessage{
			{
				MessageID:    1001,
				FromUserID:   "wxid_owner",
				ToUserID:     "bot@im.bot",
				CreateTimeMs: 1760000000000,
				ContextToken: "ctx-owner",
				ItemList: []ilink.MessageItem{
					{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "hello"}},
					{Type: ilink.ItemTypeVoice, VoiceItem: &ilink.VoiceItem{Text: "transcribed speech"}},
					{Type: ilink.ItemTypeImage, ImageItem: &ilink.ImageItem{
						AESKey: hex.EncodeToString(key),
						Media:  ilink.CDNMedia{FullURL: cdn.URL, EncryptType: 1},
					}},
				},
			},
			{
				MessageID:    1002,
				FromUserID:   "wxid_stranger",
				ToUserID:     "bot@im.bot",
				CreateTimeMs: 1760000001000,
				ContextToken: "ctx-stranger",
				ItemList: []ilink.MessageItem{
					{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "nope"}},
				},
			},
		},
	}}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)

	hits := make(chan hookHit, 4)
	hook := startWebhook(t, hits)

	stateDir := t.TempDir()
	mediaDir := t.TempDir()
	st := saveSession(t, stateDir, "xiaox", "bot-token-secret", "bot@im.bot", "wxid_owner", ilinkSrv.URL)

	client := ilink.NewClient(&ilink.Session{
		BotToken: "bot-token-secret",
		BaseURL:  ilinkSrv.URL,
		BotID:    "bot@im.bot",
		UserID:   "wxid_owner",
	})
	wh := &Dispatcher{
		Webhook: config.Webhook{
			URL:       hook.URL,
			Key:       "hook-key",
			KeyHeader: "Authorization",
			KeyPrefix: "Bearer ",
			Secret:    "hmac-secret",
		},
		Attempts: 1,
	}
	p := &Poller{
		Name:           "xiaox",
		Client:         client,
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Webhook:        wh,
		MediaDir:       mediaDir,
		DefaultTimeout: 200 * time.Millisecond,
		RetryDelay:     20 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	var hit hookHit
	select {
	case hit = <-hits:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("timeout waiting for webhook")
	}
	// Stranger must not produce a second webhook.
	select {
	case extra := <-hits:
		cancel()
		t.Fatalf("stranger was forwarded: %s", extra.Body)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop")
	}

	if got := hit.Header.Get("Authorization"); got != "Bearer hook-key" {
		t.Fatalf("auth header %q", got)
	}
	if hit.Header.Get("X-WxBot-Timestamp") == "" {
		t.Fatal("missing timestamp")
	}
	mac := hmac.New(sha256.New, []byte("hmac-secret"))
	mac.Write(hit.Body)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := hit.Header.Get("X-WxBot-Signature"); got != wantSig {
		t.Fatalf("sig %s want %s", got, wantSig)
	}

	var payload Payload
	if err := json.Unmarshal(hit.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Binding != "xiaox" || payload.FromWxid != "wxid_owner" || payload.Text != "hello" {
		t.Fatalf("payload %+v", payload)
	}
	if payload.VoiceText != "transcribed speech" {
		t.Fatalf("voice_text %q", payload.VoiceText)
	}
	if payload.MessageID != "1001" || payload.CreateTime != 1760000000 {
		t.Fatalf("ids %+v", payload)
	}
	if !payload.HasContextToken {
		t.Fatal("has_context_token")
	}
	if len(payload.Media) != 1 || payload.Media[0].Type != "image" {
		t.Fatalf("media %+v", payload.Media)
	}
	gotPlain, err := os.ReadFile(payload.Media[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPlain) != string(plain) {
		t.Fatalf("decrypted %q", gotPlain)
	}
	fi, err := os.Stat(payload.Media[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("media perm %o", fi.Mode().Perm())
	}

	cur, err := st.LoadCursor()
	if err != nil || cur != "cursor-42" {
		t.Fatalf("cursor %q %v", cur, err)
	}
	toks, _ := st.LoadTokens()
	if toks["wxid_owner"] != "ctx-owner" {
		t.Fatalf("tokens %v", toks)
	}
	if _, ok := toks["wxid_stranger"]; ok {
		t.Fatalf("stranger token persisted: %v", toks)
	}

	if len(script.headers) == 0 {
		t.Fatal("no getupdates")
	}
	h0 := script.headers[0]
	if h0.Get("iLink-App-Id") != "bot" || h0.Get("iLink-App-ClientVersion") != "131335" {
		t.Fatalf("app headers %v", h0)
	}
	if h0.Get("Authorization") != "Bearer bot-token-secret" || h0.Get("AuthorizationType") != "ilink_bot_token" {
		t.Fatalf("auth %s %s", h0.Get("AuthorizationType"), h0.Get("Authorization"))
	}
}

func TestPollerErrCodeExpiredStopsOnlyThatBinding(t *testing.T) {
	expiredScript := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		ErrCode:       ilink.ErrCodeExpired,
		GetUpdatesBuf: "x",
	}}}
	okScript := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		GetUpdatesBuf: "ok-cursor",
		Msgs: []ilink.IncomingMessage{{
			MessageID:    7,
			FromUserID:   "wxid_owner",
			ToUserID:     "bot@im.bot",
			CreateTimeMs: 1760000000000,
			ContextToken: "c",
			ItemList:     []ilink.MessageItem{{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "still alive"}}},
		}},
	}}}
	expSrv := httptest.NewServer(http.HandlerFunc(expiredScript.handler))
	closeServer(t, expSrv)
	okSrv := httptest.NewServer(http.HandlerFunc(okScript.handler))
	closeServer(t, okSrv)

	hits := make(chan hookHit, 2)
	hook := startWebhook(t, hits)

	root := t.TempDir()
	stA := saveSession(t, root, "a", "tokA", "botA@im.bot", "wxid_owner", expSrv.URL)
	stB := saveSession(t, root, "b", "tokB", "botB@im.bot", "wxid_owner", okSrv.URL)

	makePoller := func(name string, st *state.Store, base string) *Poller {
		c := ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: base, BotID: "bot@im.bot", UserID: "wxid_owner"})
		return &Poller{
			Name:           name,
			Client:         c,
			Store:          st,
			Allow:          NewAllowlist(nil, "wxid_owner"),
			Webhook:        &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1},
			MediaDir:       t.TempDir(),
			DefaultTimeout: 200 * time.Millisecond,
		}
	}
	pA := makePoller("a", stA, expSrv.URL)
	pB := makePoller("b", stB, okSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aDone atomic.Bool
	go func() { pA.Run(ctx); aDone.Store(true) }()
	bDone := make(chan struct{})
	go func() { pB.Run(ctx); close(bDone) }()

	select {
	case <-hits:
	case <-time.After(3 * time.Second):
		t.Fatal("binding b webhook timeout")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !aDone.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !aDone.Load() {
		t.Fatal("binding a poller did not stop on -14")
	}
	if !stA.IsExpired() {
		t.Fatal("missing expired marker for a")
	}
	if stB.IsExpired() {
		t.Fatal("binding b should not be expired")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-bDone:
	case <-time.After(2 * time.Second):
		t.Fatal("binding b poller did not stop")
	}
}
