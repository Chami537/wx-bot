package ilink

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
)

// AddAppHeaders sets the common iLink API headers on req.
// token is the bot_token from the Session; pass "" for pre-auth requests.
func AddAppHeaders(req *http.Request, token string) {
	addAppIdentity(req)
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if token != "" {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// addAppIdentity sets only the app id and client version headers.
// QR status polling sends these and nothing else.
func addAppIdentity(req *http.Request) {
	req.Header.Set("iLink-App-Id", iLinkAppID)
	req.Header.Set("iLink-App-ClientVersion", fmt.Sprintf("%d", iLinkClientVersion))
}

// generateUIN creates a fresh X-WECHAT-UIN value: base64(decimal(random_uint32)).
// A new value is generated per request, matching the plugin's behaviour.
func generateUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	u32 := binary.BigEndian.Uint32(b[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", u32)))
}
