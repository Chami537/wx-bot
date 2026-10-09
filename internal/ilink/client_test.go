package ilink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTextNonZeroRetIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ret":1001}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClientWithHTTP(&Session{
		BotToken: "tok",
		BaseURL:  srv.URL,
		BotID:    "bot@im.bot",
	}, srv.Client())
	err := c.SendText(context.Background(), "wxid_owner", "hi")
	if err == nil {
		t.Fatal("expected error for non-zero ret")
	}
	if !strings.Contains(err.Error(), "ret=1001") {
		t.Fatalf("got %v", err)
	}
}

func TestSendTextExpiredRet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":-14,"errcode":-14}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClientWithHTTP(&Session{BotToken: "tok", BaseURL: srv.URL, BotID: "b"}, srv.Client())
	err := c.SendText(context.Background(), "wxid_owner", "hi")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("got %v", err)
	}
}
