package localapi

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
)

func startSend(t *testing.T, send func(context.Context, bridge.SendRequest) error) (string, string) {
	t.Helper()
	upload := filepath.Join(t.TempDir(), "outgoing")
	ready := make(chan string, 1)
	s := &Server{
		Listen:    "127.0.0.1:0",
		Send:      send,
		UploadDir: upload,
		OnListen:  func(addr string) { ready <- addr },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case addr := <-ready:
		return "http://" + addr, upload
	case <-time.After(2 * time.Second):
		t.Fatal("listen timeout")
		return "", ""
	}
}

func multipartBody(t *testing.T, fields map[string]string, fileField, fileName string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if fileField != "" {
		fw, err := mw.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestSendMultipartUpload(t *testing.T) {
	var got bridge.SendRequest
	var content []byte
	base, upload := startSend(t, func(_ context.Context, req bridge.SendRequest) error {
		got = req
		content, _ = os.ReadFile(req.Image)
		return nil
	})
	body, ct := multipartBody(t, map[string]string{"binding": "xiaox", "to": "wxid_a", "caption": "见图"},
		"image", "../../evil photo.png", []byte("png-bytes"))
	resp, err := http.Post(base+"/v1/send", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got.Binding != "xiaox" || got.To != "wxid_a" || got.Caption != "见图" {
		t.Fatalf("request %+v", got)
	}
	if filepath.Base(got.Image) != "evil_photo.png" || filepath.Dir(filepath.Dir(got.Image)) != upload {
		t.Fatalf("upload path %s (dir %s)", got.Image, upload)
	}
	if string(content) != "png-bytes" {
		t.Fatalf("content %q", content)
	}
	if _, err := os.Stat(got.Image); !os.IsNotExist(err) {
		t.Fatalf("upload not cleaned up: %v", err)
	}
}

func TestSendMultipartRejectsBadPart(t *testing.T) {
	base, _ := startSend(t, func(context.Context, bridge.SendRequest) error { return nil })
	body, ct := multipartBody(t, map[string]string{"binding": "xiaox", "to": "wxid_a"}, "attachment", "a.txt", []byte("x"))
	resp, err := http.Post(base+"/v1/send", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSendMultipartTextOnly(t *testing.T) {
	var got bridge.SendRequest
	base, _ := startSend(t, func(_ context.Context, req bridge.SendRequest) error { got = req; return nil })
	body, ct := multipartBody(t, map[string]string{"binding": "xiaox", "to": "wxid_a", "text": "it's \"fine\" $HOME"}, "", "", nil)
	resp, err := http.Post(base+"/v1/send", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || got.Text != "it's \"fine\" $HOME" {
		t.Fatalf("status %d text %q", resp.StatusCode, got.Text)
	}
}
