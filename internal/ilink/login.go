package ilink

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	qrCodePath     = "/ilink/bot/get_bot_qrcode"
	qrStatusPath   = "/ilink/bot/get_qrcode_status"
	qrPollInterval = 1 * time.Second
	QRMaxExpiry    = 3 // retry QR refresh up to 3 times
)

// LoginAPI talks to the iLink auth host for QR login.
// AuthBase is overridable for tests (default DefaultAuthBase).
type LoginAPI struct {
	AuthBase   string
	HTTPClient *http.Client
}

// NewLoginAPI returns a LoginAPI pointed at the production auth host.
func NewLoginAPI() *LoginAPI {
	return &LoginAPI{
		AuthBase:   DefaultAuthBase,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (l *LoginAPI) authBase() string {
	if l != nil && l.AuthBase != "" {
		return strings.TrimRight(l.AuthBase, "/")
	}
	return DefaultAuthBase
}

func (l *LoginAPI) http() *http.Client {
	if l != nil && l.HTTPClient != nil {
		return l.HTTPClient
	}
	return http.DefaultClient
}

// GetQRCode requests a new login QR code from iLink.
// Returns the qrcode ID (used for polling) and the image URL to show the user.
func (l *LoginAPI) GetQRCode(ctx context.Context) (qrID, imgURL string, err error) {
	reqURL := l.authBase() + qrCodePath + "?bot_type=3"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build qr request: %w", err)
	}
	AddAppHeaders(req, "")

	resp, err := l.http().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("get qrcode: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("get qrcode: status %s", resp.Status)
	}

	var r QRCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", fmt.Errorf("decode qrcode response: %w", err)
	}
	if r.Ret != 0 {
		return "", "", fmt.Errorf("get qrcode: ret=%d", r.Ret)
	}
	if r.Qrcode == "" {
		return "", "", fmt.Errorf("get qrcode: empty qrcode id")
	}
	return r.Qrcode, r.QrcodeImgContent, nil
}

// ErrQRExpired is returned by PollQRStatus when the QR code times out.
var ErrQRExpired = fmt.Errorf("qr code expired")

// PollQRStatus polls get_qrcode_status until the user scans and confirms,
// the QR expires, or ctx is cancelled.
//
// Handles:
//   - "scaned_but_redirect": transparently switches to the redirect host.
//   - "expired": returns ErrQRExpired so callers can refresh.
//
// On success, returns a ready-to-use Session.
func (l *LoginAPI) PollQRStatus(ctx context.Context, qrID string) (*Session, error) {
	return l.pollQRStatus(ctx, qrID, l.authBase())
}

func (l *LoginAPI) pollQRStatus(ctx context.Context, qrID, host string) (*Session, error) {
	for {
		statusURL := strings.TrimRight(host, "/") + qrStatusPath +
			"?" + url.Values{"qrcode": {qrID}}.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build poll request: %w", err)
		}
		AddAppHeaders(req, "")

		resp, err := l.http().Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			select {
			case <-time.After(qrPollInterval):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		var r QRStatusResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()

		if decodeErr != nil {
			return nil, fmt.Errorf("decode qr status: %w", decodeErr)
		}

		switch r.Status {
		case "confirmed":
			if r.BotToken == "" || r.BaseURL == "" {
				return nil, fmt.Errorf("confirmed but missing token or base_url")
			}
			return &Session{
				BotToken: r.BotToken,
				BaseURL:  strings.TrimRight(r.BaseURL, "/"),
				BotID:    r.BotID,
				UserID:   r.UserID,
			}, nil

		case "expired":
			return nil, ErrQRExpired

		case "scaned_but_redirect":
			if r.RedirectHost == "" {
				return nil, fmt.Errorf("scaned_but_redirect with empty redirect_host")
			}
			// Switch to new IDC host and continue polling.
			host = r.RedirectHost

		default:
			// "wait" or "scaned" — keep polling
		}

		select {
		case <-time.After(qrPollInterval):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Login obtains a QR code, calls onQR (attempt is 1-based) to display it,
// polls until confirmed, and refreshes the QR up to QRMaxExpiry times on expiry.
func (l *LoginAPI) Login(ctx context.Context, onQR func(imgURL string, attempt int)) (*Session, error) {
	qrID, imgURL, err := l.GetQRCode(ctx)
	if err != nil {
		return nil, err
	}
	if onQR != nil {
		onQR(imgURL, 1)
	}

	for attempt := 1; attempt <= QRMaxExpiry; attempt++ {
		sess, err := l.PollQRStatus(ctx, qrID)
		if err == nil {
			return sess, nil
		}
		if err == ErrQRExpired && attempt < QRMaxExpiry {
			qrID, imgURL, err = l.GetQRCode(ctx)
			if err != nil {
				return nil, fmt.Errorf("refresh qrcode: %w", err)
			}
			if onQR != nil {
				onQR(imgURL, attempt+1)
			}
			continue
		}
		return nil, err
	}
	return nil, ErrQRExpired
}
