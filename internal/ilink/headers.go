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
	req.Header.Set("iLink-App-Id", iLinkAppID)
	req.Header.Set("iLink-App-ClientVersion", fmt.Sprintf("%d", iLinkClientVersion))
	req.Header.Set("X-WECHAT-UIN", generateUIN())
	if token != "" {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// generateUIN creates a fresh X-WECHAT-UIN value: base64(decimal(random_uint32)).
// A new value is generated per request, matching the plugin's behaviour.
func generateUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	u32 := binary.BigEndian.Uint32(b[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", u32)))
}
