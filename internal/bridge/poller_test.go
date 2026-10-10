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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
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
	bodies  []string
}

func (s *ilinkScript) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ilink/bot/getupdates" {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.headers = append(s.headers, r.Header.Clone())
	s.bodies = append(s.bodies, string(body))
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

func (s *ilinkScript) getHeaders() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]http.Header, len(s.headers))
	copy(out, s.headers)
	return out
}

func (s *ilinkScript) getBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

// withWebhook opens a fresh inbox and drains binding's events to d until the
// test ends.
func withWebhook(t *testing.T, binding string, d *Dispatcher) *inbox.Inbox {
	t.Helper()
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &WebhookWorker{Binding: binding, Inbox: in, Dispatcher: d}
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		in.Close()
	})
	return in
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
		Inbox:          withWebhook(t, "xiaox", wh),
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

	hdrs := script.getHeaders()
	if len(hdrs) == 0 {
		t.Fatal("no getupdates")
	}
	h0 := hdrs[0]
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
			Inbox:          withWebhook(t, name, &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1}),
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

func TestPollerTwoImagesDistinctFiles(t *testing.T) {
	key := []byte("0123456789abcdef")
	plainA := []byte("jpeg-bytes-A----")
	plainB := []byte("jpeg-bytes-B----")
	ctA, err := ilink.EncryptAES128ECB(plainA, key)
	if err != nil {
		t.Fatal(err)
	}
	ctB, err := ilink.EncryptAES128ECB(plainB, key)
	if err != nil {
		t.Fatal(err)
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.jpg":
			_, _ = w.Write(ctA)
		case "/b.jpg":
			_, _ = w.Write(ctB)
		default:
			http.NotFound(w, r)
		}
	}))
	closeServer(t, cdn)

	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		GetUpdatesBuf: "c1",
		Msgs: []ilink.IncomingMessage{{
			MessageID:    2002,
			FromUserID:   "wxid_owner",
			ToUserID:     "bot@im.bot",
			CreateTimeMs: 1760000000000,
			ContextToken: "ctx",
			ItemList: []ilink.MessageItem{
				{Type: ilink.ItemTypeImage, ImageItem: &ilink.ImageItem{
					AESKey: hex.EncodeToString(key),
					Media:  ilink.CDNMedia{FullURL: cdn.URL + "/a.jpg", EncryptType: 1},
				}},
				{Type: ilink.ItemTypeImage, ImageItem: &ilink.ImageItem{
					AESKey: hex.EncodeToString(key),
					Media:  ilink.CDNMedia{FullURL: cdn.URL + "/b.jpg", EncryptType: 1},
				}},
			},
		}},
	}}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)

	hits := make(chan hookHit, 2)
	hook := startWebhook(t, hits)
	st := saveSession(t, t.TempDir(), "xiaox", "tok", "bot@im.bot", "wxid_owner", ilinkSrv.URL)
	p := &Poller{
		Name:           "xiaox",
		Client:         ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: ilinkSrv.URL, BotID: "bot@im.bot", UserID: "wxid_owner"}),
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Inbox:          withWebhook(t, "xiaox", &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1}),
		MediaDir:       t.TempDir(),
		DefaultTimeout: 200 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	var hit hookHit
	select {
	case hit = <-hits:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}
	cancel()
	<-done

	var payload Payload
	if err := json.Unmarshal(hit.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Media) != 2 {
		t.Fatalf("media count %d: %+v", len(payload.Media), payload.Media)
	}
	if payload.Media[0].Path == payload.Media[1].Path {
		t.Fatalf("paths collided: %s", payload.Media[0].Path)
	}
	a, err := os.ReadFile(payload.Media[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(payload.Media[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(plainA) || string(b) != string(plainB) {
		t.Fatalf("contents %q %q", a, b)
	}
}

func TestPollerHasContextTokenFromCache(t *testing.T) {
	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		GetUpdatesBuf: "c2",
		Msgs: []ilink.IncomingMessage{{
			MessageID:    9,
			FromUserID:   "wxid_owner",
			ToUserID:     "bot@im.bot",
			CreateTimeMs: 1760000000000,
			ItemList:     []ilink.MessageItem{{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "hi"}}},
		}},
	}}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)
	hits := make(chan hookHit, 1)
	hook := startWebhook(t, hits)
	st := saveSession(t, t.TempDir(), "xiaox", "tok", "bot@im.bot", "wxid_owner", ilinkSrv.URL)
	client := ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: ilinkSrv.URL, BotID: "bot@im.bot", UserID: "wxid_owner"})
	client.SetToken("wxid_owner", "cached-ctx")
	p := &Poller{
		Name:           "xiaox",
		Client:         client,
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Inbox:          withWebhook(t, "xiaox", &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1}),
		MediaDir:       t.TempDir(),
		DefaultTimeout: 200 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	var hit hookHit
	select {
	case hit = <-hits:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	<-done
	var payload Payload
	if err := json.Unmarshal(hit.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.HasContextToken {
		t.Fatal("HasContextToken should be true when a cached token exists")
	}
}

func TestPollerBusinessErrorKeepsCursor(t *testing.T) {
	msg := ilink.IncomingMessage{
		MessageID:    11,
		FromUserID:   "wxid_owner",
		ToUserID:     "bot@im.bot",
		CreateTimeMs: 1760000000000,
		ItemList:     []ilink.MessageItem{{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "after error"}}},
	}
	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{
		{Ret: -1, GetUpdatesBuf: "bad-cursor", Msgs: []ilink.IncomingMessage{msg}},
		{GetUpdatesBuf: "good-cursor", Msgs: []ilink.IncomingMessage{msg}},
	}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)
	hits := make(chan hookHit, 2)
	hook := startWebhook(t, hits)
	st := saveSession(t, t.TempDir(), "xiaox", "tok", "bot@im.bot", "wxid_owner", ilinkSrv.URL)
	p := &Poller{
		Name:           "xiaox",
		Client:         ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: ilinkSrv.URL, BotID: "bot@im.bot", UserID: "wxid_owner"}),
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Inbox:          withWebhook(t, "xiaox", &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1}),
		MediaDir:       t.TempDir(),
		DefaultTimeout: 200 * time.Millisecond,
		RetryDelay:     10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	select {
	case <-hits:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}
	cancel()
	<-done

	select {
	case <-hits:
		t.Fatal("message from the business-error response was delivered")
	default:
	}
	cursor, err := st.LoadCursor()
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "good-cursor" {
		t.Fatalf("cursor %q, want good-cursor", cursor)
	}
	bodies := script.getBodies()
	if len(bodies) < 2 || !strings.Contains(bodies[1], `"get_updates_buf":""`) {
		t.Fatalf("second getupdates should reuse the old cursor, got %v", bodies)
	}
}

func TestPollerReplayedMessageDeliveredOnce(t *testing.T) {
	msg := ilink.IncomingMessage{
		MessageID:    21,
		FromUserID:   "wxid_owner",
		ToUserID:     "bot@im.bot",
		CreateTimeMs: 1760000000000,
		ItemList:     []ilink.MessageItem{{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "once"}}},
	}
	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{
		{GetUpdatesBuf: "c1", Msgs: []ilink.IncomingMessage{msg}},
		{GetUpdatesBuf: "c2", Msgs: []ilink.IncomingMessage{msg}},
	}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)
	hits := make(chan hookHit, 2)
	hook := startWebhook(t, hits)
	st := saveSession(t, t.TempDir(), "xiaox", "tok", "bot@im.bot", "wxid_owner", ilinkSrv.URL)
	p := &Poller{
		Name:           "xiaox",
		Client:         ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: ilinkSrv.URL, BotID: "bot@im.bot", UserID: "wxid_owner"}),
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Inbox:          withWebhook(t, "xiaox", &Dispatcher{Webhook: config.Webhook{URL: hook.URL}, Attempts: 1}),
		MediaDir:       t.TempDir(),
		DefaultTimeout: 200 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	var hit hookHit
	select {
	case hit = <-hits:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}
	select {
	case <-hits:
		t.Fatal("replayed message delivered twice")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	<-done
	var payload Payload
	if err := json.Unmarshal(hit.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schema != EventSchema || payload.EventID != "xiaox:21" {
		t.Fatalf("schema %q event_id %q", payload.Schema, payload.EventID)
	}
}

func TestPollerQueueFailureKeepsCursor(t *testing.T) {
	script := &ilinkScript{resps: []ilink.GetUpdatesResponse{{
		GetUpdatesBuf: "must-not-save",
		Msgs: []ilink.IncomingMessage{{
			MessageID:  31,
			FromUserID: "wxid_owner",
			ItemList:   []ilink.MessageItem{{Type: ilink.ItemTypeText, TextItem: &ilink.TextItem{Text: "x"}}},
		}},
	}}}
	ilinkSrv := httptest.NewServer(http.HandlerFunc(script.handler))
	closeServer(t, ilinkSrv)
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	in.Close() // every Add now fails
	st := saveSession(t, t.TempDir(), "xiaox", "tok", "bot@im.bot", "wxid_owner", ilinkSrv.URL)
	p := &Poller{
		Name:           "xiaox",
		Client:         ilink.NewClient(&ilink.Session{BotToken: "tok", BaseURL: ilinkSrv.URL, BotID: "bot@im.bot", UserID: "wxid_owner"}),
		Store:          st,
		Allow:          NewAllowlist(nil, "wxid_owner"),
		Inbox:          in,
		MediaDir:       t.TempDir(),
		DefaultTimeout: 100 * time.Millisecond,
		RetryDelay:     10 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if cur, _ := st.LoadCursor(); cur != "" {
		t.Fatalf("cursor advanced to %q although queueing failed", cur)
	}
}
