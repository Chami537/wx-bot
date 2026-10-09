# Review round 1

1. **[BLOCKER] [internal/bridge/poller_test.go:257](file:///workspace/wx-bot/internal/bridge/poller_test.go#L257)**
   - **Problem**: `go test -race -count=1 ./...` detects a data race in `TestPollerWebhookAuthHMACMediaAllowlistCursor`. The test goroutine reads `script.headers` at lines 257 and 260 without holding `script.mu`, while the HTTP handler goroutine concurrently appends to `script.headers` under `s.mu.Lock()` at line 60.
   - **Fix**: Add a thread-safe getter method to `ilinkScript` that acquires `s.mu` and returns a copy of `headers`, then call `script.getHeaders()` in the test:
     ```go
     func (s *ilinkScript) getHeaders() []http.Header {
         s.mu.Lock()
         defer s.mu.Unlock()
         out := make([]http.Header, len(s.headers))
         copy(out, s.headers)
         return out
     }
     ```

2. **[MAJOR] [internal/ilink/client.go:340](file:///workspace/wx-bot/internal/ilink/client.go#L340-L351)**
   - **Problem**: In `Client.post`, when `out == nil` (used by `sendTextChunk` and `sendMedia` for `/ilink/bot/sendmessage`), the method checks for `ErrCodeExpired`, but ignores non-zero `probe.Ret` values (e.g., ret != 0 returned on protocol errors, invalid context token, or rate limits). If iLink rejects an outbound message with a non-zero return code other than -14, `c.post` returns `nil` (success), causing `wx-bot send` and `/v1/send` to falsely report that the message was sent.
   - **Fix**: When `out == nil`, check `probe.Ret != 0` and return an error:
     ```go
     if len(raw) > 0 {
         _ = json.Unmarshal(raw, &probe)
         if probe.ErrCode == ErrCodeExpired || probe.Ret == ErrCodeExpired {
             return fmt.Errorf("%s: %w", path, ErrExpired)
         }
         if out == nil && probe.Ret != 0 {
             return fmt.Errorf("%s: ret=%d", path, probe.Ret)
         }
     }
     ```

3. **[MAJOR] [internal/bridge/poller.go:345](file:///workspace/wx-bot/internal/bridge/poller.go#L345-L350)**
   - **Problem**: In `saveMedia`, when a message contains multiple media attachments of the same type (e.g., two images in the same message), `defaultMediaName` produces the same filename (e.g., `image.jpg`), and `if idx > 0 { _ = idx }` discards the item index. Consequently, both items resolve to `<mediaDir>/<binding>/<day>/<msgid>_image.jpg`, causing the second download to overwrite the first file on disk and leaving duplicate/overwritten paths in the webhook payload.
   - **Fix**: Disambiguate the filename when `idx > 0`:
     ```go
     func (p *Poller) saveMedia(media ilink.CDNMedia, keyOverride []byte, msgid, filename string, t time.Time, itemType, idx int) (MediaRef, error) {
         name := defaultMediaName(itemType, filename)
         if idx > 0 {
             ext := filepath.Ext(name)
             base := strings.TrimSuffix(name, ext)
             name = fmt.Sprintf("%s_%d%s", base, idx, ext)
         }
         dest := mediaDest(p.MediaDir, p.Name, msgid, name, t)
         ...
     }
     ```

4. **[MINOR] [internal/ilink/login.go:123](file:///workspace/wx-bot/internal/ilink/login.go#L123)**
   - **Problem**: In `pollQRStatus`, when handling `scaned_but_redirect`, `host` is set directly to `r.RedirectHost`. If iLink returns a bare hostname (e.g. `sz.ilinkai.weixin.qq.com` without `https://`), `statusURL` will lack a scheme, causing subsequent `http.NewRequestWithContext` calls to fail with `unsupported protocol scheme ""`.
   - **Fix**: Ensure the redirect host has a scheme:
     ```go
     case "scaned_but_redirect":
         if r.RedirectHost == "" {
             return nil, fmt.Errorf("scaned_but_redirect with empty redirect_host")
         }
         host = r.RedirectHost
         if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
             host = "https://" + host
         }
     ```

5. **[MINOR] [internal/bridge/poller.go:338](file:///workspace/wx-bot/internal/bridge/poller.go#L338)**
   - **Problem**: `Payload.HasContextToken` is computed strictly as `msg.ContextToken != ""`. If an incoming message from an existing conversation arrives without a `context_token` in that specific packet, `HasContextToken` is set to `false` in the webhook payload even though `wx-bot` already holds a valid cached context token for `msg.FromUserID` in `p.Client.ContextToken(...)` and can reply successfully.
   - **Fix**: Check both the message and client cache:
     ```go
     HasContextToken: msg.ContextToken != "" || p.Client.ContextToken(msg.FromUserID) != "",
     ```

6. **[MINOR] [deploy/launchd/com.lawrencewzen.wx-bot.plist:17-19](file:///workspace/wx-bot/deploy/launchd/com.lawrencewzen.wx-bot.plist#L17-L19)**
   - **Problem**: `StandardOutPath` and `StandardErrorPath` are hardcoded to `/usr/local/var/log/wx-bot.log` and `/usr/local/var/log/wx-bot.err`. When installed via `install.sh --service` as a user agent into `~/Library/LaunchAgents/`, non-root users will not have write permissions to `/usr/local/var/log/` (and the directory does not exist on default macOS installations), causing launchd job execution to fail.
   - **Fix**: Update the user launchd plist to write logs under `~/Library/Logs/wx-bot.log` / `~/Library/Logs/wx-bot.err` or configure `install.sh` to substitute a writable user log directory when installing to user `LaunchAgents`.

7. **[NIT] [internal/ilink/crypto.go:66](file:///workspace/wx-bot/internal/ilink/crypto.go#L66)**
   - **Problem**: In `pkcs7Unpad`, the error string formatting passes `pad` instead of the byte offset index `i`: `fmt.Errorf("invalid padding byte at position %d", pad)`.
   - **Fix**: Pass `i` to format the actual error position:
     ```go
     return nil, fmt.Errorf("invalid padding byte at position %d", i)
     ```

8. **[NIT] [internal/ilink/crypto_test.go:114-116](file:///workspace/wx-bot/internal/ilink/crypto_test.go#L114-L116)**
   - **Problem**: In `crypto_test.go`, the helper function `toUpperHex` returns `hex.EncodeToString(b)` (which produces lowercase hex), so `forms["hex32_upper"]` in `TestParseAESKeyAllForms` (line 101) inadvertently tests lowercase hex.
   - **Fix**: Return `strings.ToUpper(hex.EncodeToString(b))`.

VERDICT: CHANGES REQUESTED
