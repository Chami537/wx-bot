package ilink

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
			if r.Method != http.MethodPost {
				t.Errorf("qrcode method %s, want POST", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"local_token_list":[]}` {
				t.Errorf("qrcode body %q", body)
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("qrcode Content-Type %q", r.Header.Get("Content-Type"))
			}
			if r.Header.Get("AuthorizationType") != "ilink_bot_token" {
				t.Errorf("qrcode AuthorizationType %q", r.Header.Get("AuthorizationType"))
			}
			if r.Header.Get("X-WECHAT-UIN") == "" {
				t.Errorf("qrcode missing X-WECHAT-UIN")
			}
			if r.Header.Get("Authorization") != "" {
				t.Errorf("qrcode must not send Authorization")
			}
			_ = json.NewEncoder(w).Encode(QRCodeResponse{
				Ret:              0,
				Qrcode:           "qr-1",
				QrcodeImgContent: "https://example.com/qr.png",
			})
		case qrStatusPath:
			for _, h := range []string{"AuthorizationType", "Authorization", "X-WECHAT-UIN"} {
				if r.Header.Get(h) != "" {
					t.Errorf("qr status must not send %s", h)
				}
			}
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

func TestLoginBareRedirectHostGetsHTTPS(t *testing.T) {
	var n atomic.Int32
	var sawHTTPS atomic.Bool
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case qrCodePath:
			return jsonResp(req, `{"ret":0,"qrcode":"qr-1","qrcode_img_content":"https://example.com/qr.png"}`), nil
		case qrStatusPath:
			i := n.Add(1)
			if i == 1 {
				return jsonResp(req, `{"ret":0,"status":"scaned_but_redirect","redirect_host":"sz.ilinkai.weixin.qq.com"}`), nil
			}
			if req.URL.Scheme != "https" || req.URL.Host != "sz.ilinkai.weixin.qq.com" {
				t.Errorf("redirect poll URL = %s, want https://sz.ilinkai.weixin.qq.com/...", req.URL.String())
			} else {
				sawHTTPS.Store(true)
			}
			return jsonResp(req, `{"ret":0,"status":"confirmed","bot_token":"tok","ilink_bot_id":"bot@im.bot","baseurl":"https://ilink-session.example","ilink_user_id":"wxid_owner"}`), nil
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
			return jsonResp(req, `{"ret":1}`), nil
		}
	})
	api := &LoginAPI{
		AuthBase:   "https://ilinkai.weixin.qq.com",
		HTTPClient: &http.Client{Transport: rt},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := api.Login(ctx, nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !sawHTTPS.Load() {
		t.Fatal("bare redirect_host was not polled over https")
	}
	if sess == nil || sess.BotToken != "tok" {
		t.Fatalf("session %+v", sess)
	}
	if !strings.HasPrefix(sess.BaseURL, "https://") {
		t.Fatalf("base url %s", sess.BaseURL)
	}
}
