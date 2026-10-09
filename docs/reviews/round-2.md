# Review round 2

## Round-1 findings

1. **[BLOCKER] [internal/bridge/poller_test.go:257](file:///workspace/wx-bot/internal/bridge/poller_test.go#L257)**
   - **Status**: FIXED
   - **Reason**: `ilinkScript` now defines `getHeaders()`, which acquires `s.mu.Lock()` and returns a copy of `s.headers`. `TestPollerWebhookAuthHMACMediaAllowlistCursor` now invokes `script.getHeaders()` instead of accessing `script.headers` directly, eliminating the data race.

2. **[MAJOR] [internal/ilink/client.go:340](file:///workspace/wx-bot/internal/ilink/client.go#L340-L351)**
   - **Status**: FIXED
   - **Reason**: In `Client.post`, when `out == nil`, non-zero `probe.Ret` return codes are now explicitly checked and returned as `fmt.Errorf("%s: ret=%d", path, probe.Ret)`. Outbound message failures are correctly surfaced to callers and test coverage was added in `internal/ilink/client_test.go`.

3. **[MAJOR] [internal/bridge/poller.go:345](file:///workspace/wx-bot/internal/bridge/poller.go#L345-L350)**
   - **Status**: FIXED
   - **Reason**: In `saveMedia`, when `idx > 0`, the filename is now disambiguated by appending `_<idx>` before the file extension (`fmt.Sprintf("%s_%d%s", base, idx, ext)`). Multiple media items in a single message now write to distinct disk paths without collision or overwrite, verified by `TestPollerTwoImagesDistinctFiles`.

4. **[MINOR] [internal/ilink/login.go:123](file:///workspace/wx-bot/internal/ilink/login.go#L123)**
   - **Status**: FIXED
   - **Reason**: In `pollQRStatus` under `scaned_but_redirect`, bare redirect hosts lacking a scheme are now automatically prefixed with `https://`. Verified by `TestLoginBareRedirectHostGetsHTTPS`.

5. **[MINOR] [internal/bridge/poller.go:338](file:///workspace/wx-bot/internal/bridge/poller.go#L338)**
   - **Status**: FIXED
   - **Reason**: `Payload.HasContextToken` is now computed as `msg.ContextToken != "" || p.Client.ContextToken(msg.FromUserID) != ""`, correctly reporting `true` when a valid context token is already cached in memory for the sender. Verified by `TestPollerHasContextTokenFromCache`.

6. **[MINOR] [deploy/launchd/com.lawrencewzen.wx-bot.plist:17-19](file:///workspace/wx-bot/deploy/launchd/com.lawrencewzen.wx-bot.plist#L17-L19)**
   - **Status**: FIXED
   - **Reason**: Plist log paths were updated to user log paths (`~/Library/Logs/wx-bot.log` and `~/Library/Logs/wx-bot.err`), and `install.sh --service` creates `~/Library/Logs` and substitutes `~/Library/Logs` with `$HOME/Library/Logs` for launchd compatibility.

7. **[NIT] [internal/ilink/crypto.go:66](file:///workspace/wx-bot/internal/ilink/crypto.go#L66)**
   - **Status**: FIXED
   - **Reason**: `pkcs7Unpad` now formats the invalid byte offset using index `i` (`fmt.Errorf("invalid padding byte at position %d", i)`), and this is asserted in `TestPKCS7EdgeCases`.

8. **[NIT] [internal/ilink/crypto_test.go:114-116](file:///workspace/wx-bot/internal/ilink/crypto_test.go#L114-L116)**
   - **Status**: FIXED
   - **Reason**: `toUpperHex` in `internal/ilink/crypto_test.go` now wraps `hex.EncodeToString(b)` in `strings.ToUpper(...)`, ensuring uppercase hex decoding is properly validated.

---

## New findings

No new findings.

All areas verified:
- **Protocol correctness vs alice**: Headers (`iLink-App-Id`, `iLink-App-ClientVersion`, random `X-WECHAT-UIN`, `AuthorizationType`), `ChannelVersion` 2.1.7, AES-128-ECB PKCS7 crypto, `ParseAESKey` formats, QR login flow with redirect handling, long-poll `getupdates`, upload/download CDN routines, and rune-based text splitting strictly match the reference implementation.
- **Security**: Zero secrets in logs or webhook payloads; loopback-only binding enforcement on local API with constant-time token comparison; atomic writes with `0700` directory and `0600` file permissions; strict filename sanitization against path traversal; allowlist checks on inbound messages and outbound send requests.
- **Concurrency & lifecycle**: Clean goroutine management with context propagation and no leaks; HTTP keep-alives disabled on polling transport; proper session `-14` expiry isolation per binding.
- **Packaging & Deployment**: `install.sh` shell syntax and OS/architecture guards (explicitly rejecting Windows); cross-compilation `Makefile`; systemd user unit; launchd agent plist; example configuration; and documentation accuracy matching the codebase.

---

VERDICT: CLEAN
