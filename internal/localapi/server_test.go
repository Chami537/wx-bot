package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/ilink"
)

func TestRejectsNonLoopback(t *testing.T) {
	s := &Server{Listen: "0.0.0.0:18790"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := s.Start(ctx)
	if err == nil || err.Error() == "" {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestTokenAuthAndSendRoutesContextToken(t *testing.T) {
	var got ilink.SendMessageRequest
	ilinkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode sendmessage: %v", err)
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	t.Cleanup(func() {
		ilinkSrv.CloseClientConnections()
		ilinkSrv.Close()
	})

	client := ilink.NewClient(&ilink.Session{
		BotToken: "tok",
		BaseURL:  ilinkSrv.URL,
		BotID:    "bot@im.bot",
		UserID:   "wxid_owner",
	})
	client.SetToken("wxid_owner", "ctx-123")
	sender := bridge.Sender{Client: client, Allow: bridge.NewAllowlist(nil, "wxid_owner")}

	ready := make(chan string, 1)
	s := &Server{
		Listen: "127.0.0.1:0",
		Token:  "local-secret",
		Send:   sender.Send,
		Status: func() []bridge.BindingStatus { return []bridge.BindingStatus{{Name: "xiaox", LoggedIn: true}} },
		OnListen: func(addr string) {
			ready <- addr
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()
	var addr string
	select {
	case addr = <-ready:
	case err := <-errCh:
		t.Fatalf("server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("listen timeout")
	}

	base := "http://" + addr
	resp, err := http.Post(base+"/v1/send", "application/json", bytes.NewBufferString(`{"binding":"xiaox","to":"wxid_owner","text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/send", bytes.NewBufferString(`{"binding":"xiaox","to":"wxid_owner","text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer local-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send %d %s", resp.StatusCode, body)
	}
	if got.Msg.ContextToken != "ctx-123" {
		t.Fatalf("context_token %q", got.Msg.ContextToken)
	}
	if got.Msg.ToUserID != "wxid_owner" || got.BaseInfo.ChannelVersion != ilink.ChannelVersion {
		t.Fatalf("sendmessage %+v", got)
	}
	if len(got.Msg.ItemList) != 1 || got.Msg.ItemList[0].TextItem == nil || got.Msg.ItemList[0].TextItem.Text != "hi" {
		t.Fatalf("items %+v", got.Msg.ItemList)
	}

	sreq, _ := http.NewRequest(http.MethodGet, base+"/v1/status", nil)
	sreq.Header.Set("Authorization", "Bearer local-secret")
	sresp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatal(err)
	}
	sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", sresp.StatusCode)
	}

	hresp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		t.Fatalf("healthz %d", hresp.StatusCode)
	}

	// allowlist reject
	bad, _ := http.NewRequest(http.MethodPost, base+"/v1/send", bytes.NewBufferString(`{"binding":"xiaox","to":"wxid_stranger","text":"x"}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("Authorization", "Bearer local-secret")
	bresp, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	bresp.Body.Close()
	if bresp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger send %d", bresp.StatusCode)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestConfigCheckUsed(t *testing.T) {
	if err := config.CheckLoopbackListen("127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
}
