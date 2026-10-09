package ilink

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

func TestDefaultCDNBase(t *testing.T) {
	if DefaultCDNBase != "https://novac2c.cdn.weixin.qq.com/c2c" {
		t.Fatalf("DefaultCDNBase=%q", DefaultCDNBase)
	}
}

func TestUploadMediaTypeMapping(t *testing.T) {
	if uploadMediaType(ItemTypeImage) != UploadMediaImage {
		t.Fatalf("image item=%d upload=%d", ItemTypeImage, uploadMediaType(ItemTypeImage))
	}
	if uploadMediaType(ItemTypeVideo) != UploadMediaVideo {
		t.Fatalf("video item=%d upload=%d", ItemTypeVideo, uploadMediaType(ItemTypeVideo))
	}
	if uploadMediaType(ItemTypeFile) != UploadMediaFile {
		t.Fatalf("file item=%d upload=%d", ItemTypeFile, uploadMediaType(ItemTypeFile))
	}
	if uploadMediaType(ItemTypeVoice) != UploadMediaVoice {
		t.Fatalf("voice item=%d upload=%d", ItemTypeVoice, uploadMediaType(ItemTypeVoice))
	}
}

func TestAESEcbPaddedSize(t *testing.T) {
	cases := []struct{ n, want int64 }{
		{0, 16}, {1, 16}, {15, 16}, {16, 32}, {17, 32},
	}
	for _, tc := range cases {
		if got := aesECBPaddedSize(tc.n); got != tc.want {
			t.Errorf("aesECBPaddedSize(%d)=%d want %d", tc.n, got, tc.want)
		}
	}
}

func TestBuildCdnUploadURL(t *testing.T) {
	got := buildCdnUploadURL("https://novac2c.cdn.weixin.qq.com/c2c", "a+b/c=", "deadbeef")
	want := "https://novac2c.cdn.weixin.qq.com/c2c/upload?encrypted_query_param=a%2Bb%2Fc%3D&filekey=deadbeef"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// mediaFake is an httptest double for iLink getuploadurl + CDN /upload + sendmessage.
type mediaFake struct {
	mu sync.Mutex

	base          string
	mode          string // "full" (upload_full_url) or "param" (upload_param fallback)
	uploadParam   string
	ret           int
	downloadParam string

	uploadRaw []byte
	cdnMethod string
	cdnCT     string
	cdnURL    string
	cdnBody   []byte
	sendRaws  [][]byte
}

func (f *mediaFake) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/ilink/bot/getuploadurl":
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.uploadRaw = raw
		mode, ret, param, base := f.mode, f.ret, f.uploadParam, f.base
		f.mu.Unlock()
		if ret != 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": ret, "errmsg": "rejected"})
			return
		}
		resp := map[string]any{"ret": 0}
		if mode == "param" {
			if param == "" {
				param = "up-param-xyz"
			}
			resp["upload_param"] = param
		} else {
			resp["upload_full_url"] = base + "/upload"
		}
		_ = json.NewEncoder(w).Encode(resp)

	case "/upload":
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.cdnMethod = r.Method
		f.cdnCT = r.Header.Get("Content-Type")
		f.cdnURL = r.URL.String()
		f.cdnBody = body
		dl := f.downloadParam
		f.mu.Unlock()
		if dl == "" {
			dl = "dl-param-from-cdn"
		}
		w.Header().Set("x-encrypted-param", dl)
		w.WriteHeader(http.StatusOK)

	case "/ilink/bot/sendmessage":
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.sendRaws = append(f.sendRaws, raw)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ret":0}`))

	default:
		http.NotFound(w, r)
	}
}

func (f *mediaFake) snapshot() (upload map[string]any, cdnMethod, cdnCT, cdnURL string, cdnBody []byte, sends [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	upload = map[string]any{}
	if len(f.uploadRaw) > 0 {
		_ = json.Unmarshal(f.uploadRaw, &upload)
	}
	sends = append([][]byte(nil), f.sendRaws...)
	return upload, f.cdnMethod, f.cdnCT, f.cdnURL, append([]byte(nil), f.cdnBody...), sends
}

func startMediaFake(t *testing.T, mode string) (*mediaFake, *httptest.Server) {
	t.Helper()
	f := &mediaFake{mode: mode, downloadParam: "dl-param-from-cdn", uploadParam: "up+param/x="}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	f.base = srv.URL
	return f, srv
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestClient(srv *httptest.Server) *Client {
	c := NewClientWithHTTP(&Session{
		BotToken: "tok",
		BaseURL:  srv.URL,
		BotID:    "bot@im.bot",
	}, srv.Client())
	c.SetCDNBaseURL(srv.URL)
	c.SetToken("wxid_owner", "ctx-123")
	return c
}

func assertGetUploadURLBody(t *testing.T, body map[string]any, to string, plaintext []byte, wantMediaType int) {
	t.Helper()
	wantKeys := []string{"aeskey", "base_info", "filekey", "filesize", "media_type", "no_need_thumb", "rawfilemd5", "rawsize", "to_user_id"}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("getuploadurl keys %v want %v", keys, wantKeys)
	}

	filekey, _ := body["filekey"].(string)
	if len(filekey) != 32 {
		t.Fatalf("filekey len %d (want 32 hex chars of 16 random bytes): %q", len(filekey), filekey)
	}
	if _, err := hex.DecodeString(filekey); err != nil {
		t.Fatalf("filekey not hex: %s", filekey)
	}

	aeskey, _ := body["aeskey"].(string)
	if len(aeskey) != 32 {
		t.Fatalf("aeskey len %d (want 32 hex chars): %q", len(aeskey), aeskey)
	}
	keyBytes, err := hex.DecodeString(aeskey)
	if err != nil || len(keyBytes) != 16 {
		t.Fatalf("aeskey not 16-byte hex: %s", aeskey)
	}

	if int(body["media_type"].(float64)) != wantMediaType {
		t.Fatalf("media_type=%v want %d", body["media_type"], wantMediaType)
	}
	if body["to_user_id"] != to {
		t.Fatalf("to_user_id=%v", body["to_user_id"])
	}
	if int64(body["rawsize"].(float64)) != int64(len(plaintext)) {
		t.Fatalf("rawsize=%v want %d", body["rawsize"], len(plaintext))
	}
	sum := md5.Sum(plaintext)
	if body["rawfilemd5"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("rawfilemd5=%v", body["rawfilemd5"])
	}
	if int64(body["filesize"].(float64)) != aesECBPaddedSize(int64(len(plaintext))) {
		t.Fatalf("filesize=%v want padded %d", body["filesize"], aesECBPaddedSize(int64(len(plaintext))))
	}
	if body["no_need_thumb"] != true {
		t.Fatalf("no_need_thumb=%v", body["no_need_thumb"])
	}
	bi, _ := body["base_info"].(map[string]any)
	if bi["channel_version"] != ChannelVersion {
		t.Fatalf("base_info=%v", body["base_info"])
	}
}

func assertCDNUpload(t *testing.T, method, ct, cdnURL string, body, plaintext []byte, aeskeyHex string, expectQuery bool, filekey, uploadParam string) {
	t.Helper()
	if method != http.MethodPost {
		t.Fatalf("cdn method %s want POST", method)
	}
	if ct != "application/octet-stream" {
		t.Fatalf("cdn content-type %q", ct)
	}
	if int64(len(body)) != aesECBPaddedSize(int64(len(plaintext))) {
		t.Fatalf("cdn body len %d want padded %d", len(body), aesECBPaddedSize(int64(len(plaintext))))
	}
	key, err := hex.DecodeString(aeskeyHex)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := DecryptAES128ECB(body, key)
	if err != nil {
		t.Fatalf("decrypt cdn body: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("cdn plaintext mismatch %q vs %q", pt, plaintext)
	}
	if expectQuery {
		if !strings.HasPrefix(cdnURL, "/upload?") {
			t.Fatalf("cdn url %s", cdnURL)
		}
		if !strings.Contains(cdnURL, "encrypted_query_param=") || !strings.Contains(cdnURL, "filekey=") {
			t.Fatalf("cdn url missing query: %s", cdnURL)
		}
		want := buildCdnUploadURL("", uploadParam, filekey)
		if cdnURL != want {
			t.Fatalf("cdn url %s want %s", cdnURL, want)
		}
	} else if cdnURL != "/upload" {
		t.Fatalf("cdn url %s want /upload (upload_full_url, no extra query)", cdnURL)
	}
}

func decodeSend(t *testing.T, raw []byte) SendMessageRequest {
	t.Helper()
	var req SendMessageRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func assertOutboundMedia(t *testing.T, media CDNMedia, aeskeyHex, downloadParam string) {
	t.Helper()
	if media.EncryptQueryParam != downloadParam {
		t.Fatalf("encrypt_query_param %q want %q", media.EncryptQueryParam, downloadParam)
	}
	if media.EncryptType != 1 {
		t.Fatalf("encrypt_type %d", media.EncryptType)
	}
	if media.FullURL != "" {
		t.Fatalf("full_url should be omitted, got %q", media.FullURL)
	}
	want := base64.StdEncoding.EncodeToString([]byte(aeskeyHex))
	if media.AESKey != want {
		t.Fatalf("aes_key %q want base64(hex)=%q", media.AESKey, want)
	}
}

func TestSendImageUploadFullURL(t *testing.T) {
	plain := []byte("hello-image")
	path := writeTempFile(t, "pic.jpg", plain)
	f, srv := startMediaFake(t, "full")
	c := newTestClient(srv)

	if err := c.SendImage(context.Background(), "wxid_owner", path, ""); err != nil {
		t.Fatal(err)
	}

	upload, method, ct, cdnURL, cdnBody, sends := f.snapshot()
	assertGetUploadURLBody(t, upload, "wxid_owner", plain, UploadMediaImage)
	assertCDNUpload(t, method, ct, cdnURL, cdnBody, plain, upload["aeskey"].(string), false, "", "")
	if len(sends) != 1 {
		t.Fatalf("sendmessage count %d", len(sends))
	}
	req := decodeSend(t, sends[0])
	if req.Msg.ToUserID != "wxid_owner" || req.Msg.ContextToken != "ctx-123" {
		t.Fatalf("send msg %+v", req.Msg)
	}
	if req.Msg.MessageType != 2 || req.Msg.MessageState != 2 {
		t.Fatalf("message type/state %+v", req.Msg)
	}
	if len(req.Msg.ItemList) != 1 || req.Msg.ItemList[0].Type != ItemTypeImage {
		t.Fatalf("items %+v", req.Msg.ItemList)
	}
	img := req.Msg.ItemList[0].ImageItem
	if img == nil {
		t.Fatal("missing image_item")
	}
	assertOutboundMedia(t, img.Media, upload["aeskey"].(string), "dl-param-from-cdn")
	if img.MidSize != aesECBPaddedSize(int64(len(plain))) {
		t.Fatalf("mid_size %d", img.MidSize)
	}
	if img.ThumbMedia != nil {
		t.Fatalf("thumb_media should be omitted, got %+v", img.ThumbMedia)
	}

	var raw map[string]any
	if err := json.Unmarshal(sends[0], &raw); err != nil {
		t.Fatal(err)
	}
	item := raw["msg"].(map[string]any)["item_list"].([]any)[0].(map[string]any)
	imageItem := item["image_item"].(map[string]any)
	if _, ok := imageItem["thumb_media"]; ok {
		t.Fatal("image_item.thumb_media present")
	}
	media := imageItem["media"].(map[string]any)
	if _, ok := media["full_url"]; ok {
		t.Fatal("media.full_url present")
	}
}

func TestSendImageUploadParamFallback(t *testing.T) {
	plain := []byte("param-fallback-img")
	path := writeTempFile(t, "pic.png", plain)
	f, srv := startMediaFake(t, "param")
	c := newTestClient(srv)

	if err := c.SendImage(context.Background(), "wxid_owner", path, ""); err != nil {
		t.Fatal(err)
	}

	upload, method, ct, cdnURL, cdnBody, sends := f.snapshot()
	assertGetUploadURLBody(t, upload, "wxid_owner", plain, UploadMediaImage)
	assertCDNUpload(t, method, ct, cdnURL, cdnBody, plain, upload["aeskey"].(string), true, upload["filekey"].(string), "up+param/x=")
	if len(sends) != 1 {
		t.Fatalf("sendmessage count %d", len(sends))
	}
	req := decodeSend(t, sends[0])
	img := req.Msg.ItemList[0].ImageItem
	assertOutboundMedia(t, img.Media, upload["aeskey"].(string), "dl-param-from-cdn")
}

func TestSendImageCaptionIsSeparateTextMessage(t *testing.T) {
	plain := []byte("cap-img")
	path := writeTempFile(t, "pic.jpg", plain)
	f, srv := startMediaFake(t, "full")
	c := newTestClient(srv)

	if err := c.SendImage(context.Background(), "wxid_owner", path, "look at this"); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, sends := f.snapshot()
	if len(sends) != 2 {
		t.Fatalf("sendmessage count %d want 2 (caption then image)", len(sends))
	}
	first := decodeSend(t, sends[0])
	if len(first.Msg.ItemList) != 1 || first.Msg.ItemList[0].Type != ItemTypeText {
		t.Fatalf("first item %+v", first.Msg.ItemList)
	}
	if first.Msg.ItemList[0].TextItem == nil || first.Msg.ItemList[0].TextItem.Text != "look at this" {
		t.Fatalf("caption %+v", first.Msg.ItemList[0].TextItem)
	}
	second := decodeSend(t, sends[1])
	if len(second.Msg.ItemList) != 1 || second.Msg.ItemList[0].Type != ItemTypeImage {
		t.Fatalf("second item %+v", second.Msg.ItemList)
	}
}

func TestSendVideoMediaFields(t *testing.T) {
	plain := []byte("fake-mp4-bytes!!")
	path := writeTempFile(t, "clip.mp4", plain)
	f, srv := startMediaFake(t, "full")
	c := newTestClient(srv)

	if err := c.SendVideo(context.Background(), "wxid_owner", path, ""); err != nil {
		t.Fatal(err)
	}
	upload, _, _, _, cdnBody, sends := f.snapshot()
	assertGetUploadURLBody(t, upload, "wxid_owner", plain, UploadMediaVideo)
	if len(sends) != 1 {
		t.Fatalf("sendmessage count %d", len(sends))
	}
	req := decodeSend(t, sends[0])
	if req.Msg.ItemList[0].Type != ItemTypeVideo || req.Msg.ItemList[0].VideoItem == nil {
		t.Fatalf("items %+v", req.Msg.ItemList)
	}
	v := req.Msg.ItemList[0].VideoItem
	assertOutboundMedia(t, v.Media, upload["aeskey"].(string), "dl-param-from-cdn")
	if v.VideoSize != int64(len(cdnBody)) || v.VideoSize != aesECBPaddedSize(int64(len(plain))) {
		t.Fatalf("video_size %d cdn %d", v.VideoSize, len(cdnBody))
	}
	if v.ThumbMedia != nil {
		t.Fatalf("thumb_media %+v", v.ThumbMedia)
	}
}

func TestSendFileMediaFields(t *testing.T) {
	plain := []byte("plain-file-body")
	path := writeTempFile(t, "note.pdf", plain)
	f, srv := startMediaFake(t, "full")
	c := newTestClient(srv)

	if err := c.SendFile(context.Background(), "wxid_owner", path, ""); err != nil {
		t.Fatal(err)
	}
	upload, _, _, _, _, sends := f.snapshot()
	assertGetUploadURLBody(t, upload, "wxid_owner", plain, UploadMediaFile)
	req := decodeSend(t, sends[0])
	if req.Msg.ItemList[0].Type != ItemTypeFile || req.Msg.ItemList[0].FileItem == nil {
		t.Fatalf("items %+v", req.Msg.ItemList)
	}
	fi := req.Msg.ItemList[0].FileItem
	assertOutboundMedia(t, fi.Media, upload["aeskey"].(string), "dl-param-from-cdn")
	if fi.FileName != "note.pdf" {
		t.Fatalf("file_name %q", fi.FileName)
	}
	if fi.Len != "15" { // plaintext size as string
		t.Fatalf("len %q want plaintext size 15", fi.Len)
	}
	if fi.Md5 != "" {
		t.Fatalf("md5 should be omitted, got %q", fi.Md5)
	}

	var raw map[string]any
	if err := json.Unmarshal(sends[0], &raw); err != nil {
		t.Fatal(err)
	}
	fileItem := raw["msg"].(map[string]any)["item_list"].([]any)[0].(map[string]any)["file_item"].(map[string]any)
	if _, ok := fileItem["md5"]; ok {
		t.Fatal("file_item.md5 present")
	}
}

func TestSendImageGetUploadURLRetMinus2(t *testing.T) {
	path := writeTempFile(t, "pic.jpg", []byte("x"))
	f, srv := startMediaFake(t, "full")
	f.mu.Lock()
	f.ret = -2
	f.mu.Unlock()
	c := newTestClient(srv)

	err := c.SendImage(context.Background(), "wxid_owner", path, "")
	if err == nil || !strings.Contains(err.Error(), "ret=-2") {
		t.Fatalf("got %v", err)
	}
}
