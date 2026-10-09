package ilink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginRedirectThenConfirm(t *testing.T) {
	var confirmed atomic.Bool
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != qrStatusPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("iLink-App-Id") != "bot" {
			t.Errorf("missing iLink-App-Id")
		}
		if r.Header.Get("iLink-App-ClientVersion") != "131335" {
			t.Errorf("client version %s", r.Header.Get("iLink-App-ClientVersion"))
		}
		confirmed.Store(true)
		_ = json.NewEncoder(w).Encode(QRStatusResponse{
			Ret:      0,
			Status:   "confirmed",
			BotToken: "tok_abc",
			BotID:    "bot123@im.bot",
			BaseURL:  "https://ilink-session.example",
			UserID:   "wxid_owner",
		})
	}))
	defer redirect.Close()

	var statusHits atomic.Int32
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qrCodePath:
			if r.URL.Query().Get("bot_type") != "3" {
				t.Errorf("bot_type=%s", r.URL.Query().Get("bot_type"))
			}
			_ = json.NewEncoder(w).Encode(QRCodeResponse{
				Ret:              0,
				Qrcode:           "qr-1",
				QrcodeImgContent: "https://example.com/qr.png",
			})
		case qrStatusPath:
			n := statusHits.Add(1)
			if n == 1 {
				_ = json.NewEncoder(w).Encode(QRStatusResponse{
					Ret:          0,
					Status:       "scaned_but_redirect",
					RedirectHost: redirect.URL,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(QRStatusResponse{Ret: 0, Status: "wait"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer auth.Close()

	api := &LoginAPI{AuthBase: auth.URL, HTTPClient: auth.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := api.Login(ctx, nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !confirmed.Load() {
		t.Fatal("redirect host was never polled")
	}
	if sess.BotToken != "tok_abc" || sess.UserID != "wxid_owner" || sess.BotID != "bot123@im.bot" {
		t.Fatalf("session %+v", sess)
	}
	if sess.BaseURL != "https://ilink-session.example" {
		t.Fatalf("base url %s", sess.BaseURL)
	}
}
