package bridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/config"
)

const (
	defaultWebhookTimeout  = 15 * time.Second
	defaultWebhookAttempts = 3
)

// MediaRef is one downloaded media file in the webhook payload.
type MediaRef struct {
	Type     string `json:"type"` // image|voice|file|video
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
}

// Payload is POSTed to the binding webhook.
type Payload struct {
	Binding         string     `json:"binding"`
	MessageID       string     `json:"message_id"`
	FromWxid        string     `json:"from_wxid"`
	ToBotID         string     `json:"to_bot_id"`
	CreateTime      int64      `json:"create_time"`
	Text            string     `json:"text"`
	VoiceText       string     `json:"voice_text,omitempty"`
	Media           []MediaRef `json:"media"`
	MediaErrors     []string   `json:"media_errors,omitempty"`
	HasContextToken bool       `json:"has_context_token"`
	ReplyHint       string     `json:"reply_hint"`
}

// Dispatcher POSTs payloads to a webhook URL with optional auth and HMAC.
type Dispatcher struct {
	Webhook  config.Webhook
	Client   *http.Client
	Attempts int
	Backoff  time.Duration
	Sleep    func(time.Duration)
	Log      *slog.Logger
}

func (d *Dispatcher) http() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: defaultWebhookTimeout}
}

func (d *Dispatcher) attempts() int {
	if d.Attempts > 0 {
		return d.Attempts
	}
	return defaultWebhookAttempts
}

func (d *Dispatcher) backoff() time.Duration {
	if d.Backoff > 0 {
		return d.Backoff
	}
	return time.Second
}

func (d *Dispatcher) sleep(durn time.Duration) {
	if d.Sleep != nil {
		d.Sleep(durn)
		return
	}
	time.Sleep(durn)
}

func (d *Dispatcher) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// Host returns the webhook URL host (no credentials).
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Post JSON-encodes p and delivers it. Retries on 5xx and network errors.
func (d *Dispatcher) Post(ctx context.Context, p Payload) error {
	if d.Webhook.URL == "" {
		return fmt.Errorf("webhook url is empty")
	}
	if p.Media == nil {
		p.Media = []MediaRef{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	// Encoder adds a trailing newline; keep the exact body for HMAC.
	body := buf.Bytes()

	var lastErr error
	n := d.attempts()
	delay := d.backoff()
	for i := 0; i < n; i++ {
		if i > 0 {
			d.sleep(delay)
			delay *= 2
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = d.postOnce(ctx, body)
		if lastErr == nil {
			return nil
		}
		if !retryable(lastErr) {
			return lastErr
		}
		d.log().Warn("webhook retry", "attempt", i+1, "err", lastErr)
	}
	return lastErr
}

type httpStatusError struct {
	code int
}

func (e httpStatusError) Error() string {
	return fmt.Sprintf("webhook status %d", e.code)
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var se httpStatusError
	if errors.As(err, &se) {
		return se.code >= 500
	}
	return true // network / other
}

func (d *Dispatcher) postOnce(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.Webhook.Key != "" {
		header := d.Webhook.KeyHeader
		if header == "" {
			header = "Authorization"
		}
		req.Header.Set(header, d.Webhook.KeyPrefix+d.Webhook.Key)
	}
	if d.Webhook.Secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(d.Webhook.Secret))
		mac.Write(body)
		req.Header.Set("X-WxBot-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-WxBot-Timestamp", ts)
	}

	resp, err := d.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return httpStatusError{code: resp.StatusCode}
	}
	return nil
}

// SignBody returns the X-WxBot-Signature value for body (tests / docs).
func SignBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
