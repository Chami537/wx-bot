# wx-bot

把个人微信（腾讯官方 [ClawBot / iLink](https://ilinkai.weixin.qq.com)）接到你自己的 Agent webhook。

微信消息经 iLink 长轮询进入本机 `wx-bot`，下载并解密媒体后 POST JSON 到绑定的 webhook（例如 Grok Bot routine）。Agent 再通过 `wx-bot send` 或本机 HTTP API 把纯文本/文件发回微信。

**只出站，不在公网端口上监听。** 本地发送 API 仅绑定 loopback（`127.0.0.1` / `::1`）。

平台：**仅 Linux 与 macOS**（amd64 / arm64）。不支持 Windows。

回复内容为**纯文本，不要发 Markdown**（Markdown 应由 Agent 侧去掉）。

## 架构

```
微信用户
    │  iLink getupdates（长轮询）
    ▼
wx-bot（本机）
    │  下载/解密媒体
    │  POST JSON → webhook
    ▼
Agent（Grok Bot / 其他）
    │  wx-bot send  或  POST http://127.0.0.1:18790/v1/send
    ▼
wx-bot ── iLink sendmessage ──► 微信用户
```

一个 binding = 一个微信账号 = 一个 ClawBot = 一个 webhook。

## 安装（Linux / macOS）

需要 Go 1.24+（无 Go 时可用 `make cross` 预先放在 `dist/` 里的二进制）。

```bash
git clone https://github.com/lawrencewzen/wx-bot
cd wx-bot
chmod +x install.sh
./install.sh            # 安装到 ~/.local/bin
# ./install.sh --system # 安装到 /usr/local/bin
# ./install.sh --service
```

或：

```bash
make build
make install            # ~/.local/bin/wx-bot
```

交叉编译四个目标：

```bash
make cross
# dist/wx-bot_linux_amd64
# dist/wx-bot_linux_arm64
# dist/wx-bot_darwin_amd64
# dist/wx-bot_darwin_arm64
```

`GOOS=windows go build ./cmd/wx-bot` 会编译失败（明确的平台守卫）。

## 配置

默认路径 `~/.config/wx-bot/config.json`（`-config` 可覆盖）。示例见 `examples/config.example.json`。

```json
{
  "state_dir": "~/.local/state/wx-bot",
  "media_dir": "~/.local/state/wx-bot/media",
  "local_api": { "listen": "127.0.0.1:18790", "token": "" },
  "bindings": [
    {
      "name": "xiaox",
      "webhook": {
        "url": "https://example.com/webhook/wx-bot",
        "key": "env:WXBOT_WEBHOOK_KEY",
        "key_header": "Authorization",
        "key_prefix": "Bearer ",
        "secret": "env:WXBOT_WEBHOOK_SECRET"
      },
      "allow_wxids": [],
      "forward_unallowed": false
    }
  ]
}
```

- `name`：`[a-z0-9_-]{1,32}`，全局唯一。
- 任意字符串字段可用 `env:VAR_NAME` 从环境变量读取。
- `allow_wxids` 为空时，默认只放行登录时扫码的 `ilink_user_id`。
- `local_api.listen` 必须是 loopback，否则拒绝启动。
- `webhook.secret` 若设置，会附加 `X-WxBot-Signature: sha256=<HMAC-SHA256(body)>` 和 `X-WxBot-Timestamp`。

未知 JSON 字段会被拒绝。状态目录 `0700`，会话/token 文件 `0600`，原子写入。

## 登录 / 运行 / 发送

```bash
wx-bot login xiaox     # 终端打印二维码 + URL，扫码登录
wx-bot run             # 为所有已登录且未过期的 binding 启动 poller + 本地 API
wx-bot status
wx-bot send --binding xiaox --to <wxid> --text '你好'
wx-bot send --binding xiaox --to <wxid> --image ./a.jpg --caption '见图'
wx-bot version
```

全局参数：`-config`、`-log-level`（debug/info/warn/error）。日志走 stderr 的 `log/slog` 文本格式。`bot_token` / `context_token` / webhook key 与 secret **不会写入日志**。

`send` 若发现 `run` 的本地 API 可达，会优先走它；否则直接用磁盘上的 session 发。目标 wxid 必须在该 binding 的 allowlist 内，且该用户必须先给 bot 发过消息（需要 `context_token`）。

本地 API（仅 `run` 时）：

- `GET /healthz`
- `GET /v1/status`
- `POST /v1/send` JSON：`{binding,to,text|image|file|video,caption}`
- 若配置了 `local_api.token`，`/v1/*` 需要 `Authorization: Bearer <token>`

## Webhook 载荷

`POST`，`Content-Type: application/json`，15s 超时，5xx/网络错误最多 3 次退避重试。

```json
{
  "binding": "xiaox",
  "message_id": "1001",
  "from_wxid": "wxid_xxx",
  "to_bot_id": "xxx@im.bot",
  "create_time": 1760000000,
  "text": "拼接后的文本",
  "voice_text": "语音识别文本",
  "media": [
    { "type": "image", "path": "/abs/path", "file_name": "image.jpg", "size": 123 }
  ],
  "media_errors": [],
  "has_context_token": true,
  "reply_hint": "wx-bot send --binding xiaox --to wxid_xxx --text '...'"
}
```

媒体落在 `media_dir/<binding>/<yyyymmdd>/<msgid>_<sanitized>`（目录 0700，文件 0600）。载荷和日志中从不包含 bot_token / context_token / webhook key。

未授权发送者默认只打 INFO 日志、不转发、不回复。`forward_unallowed: true` 时会转发，但仍不回复。

## 接到 Grok Bot

把 binding 的 `webhook.url` 设成 Grok Bot routine 的 webhook 地址，`key` 填 routine 的 bearer token（或 `env:WXBOT_WEBHOOK_KEY`）。Agent 收到 JSON 后用本机 `wx-bot send` 或 `127.0.0.1:18790/v1/send` 回微信。Agent 输出请保持纯文本。

## 开机自启

**systemd --user（Linux）**

```bash
./install.sh --service
systemctl --user enable --now wx-bot
journalctl --user -u wx-bot -f
```

单元文件：`deploy/systemd/wx-bot.service`。

**launchd（macOS）**

```bash
./install.sh --service
launchctl load ~/Library/LaunchAgents/com.lawrencewzen.wx-bot.plist
```

plist：`deploy/launchd/com.lawrencewzen.wx-bot.plist`。

**无 systemd（tmux / nohup + cron）**

```bash
tmux new -s wx-bot 'wx-bot run'
# 或
nohup wx-bot run >>~/.local/state/wx-bot/wx-bot.log 2>&1 &
crontab -e
# @reboot /home/you/.local/bin/wx-bot run >>/home/you/.local/state/wx-bot/wx-bot.log 2>&1
```

## 安全

- 本地 API 拒绝非 loopback。
- 状态/媒体目录 0700，密钥文件 0600。
- 密钥只允许 `env:` 或文件，日志里只出现脱敏 bot id 和 webhook host。
- 默认 allowlist = 扫码登录的微信用户。
- 不要把 `config.json` 提交到 git；`webhook.key` / `secret` / `local_api.token` 建议用环境变量。

## 限制

- iLink 返回 errcode/ret `-14` 表示该 binding 会话过期：poller 停止并写下 `expired` 标记，需重新 `wx-bot login <binding>`。其它 binding 不受影响。
- 只实现 text / image / voice（优先用 `voice_item.text`）/ file / video。其它 item 类型会忽略。
- 文本按 rune 切分，每片最多 2000。
- 未在全部 CDN 变体与视频编码上做完整实网测试。
- 不支持 Windows。

## 开发

```bash
gofmt -l .
go vet ./...
go test ./...
make cross
```

协议代码从 `lawrencewzen/alice` 的 `internal/wechat` 抽出，行为保持一致（headers、`channel_version` 2.1.7、AES-128-ECB、`parseAESKey`、上传流程、QR `scaned_but_redirect`、errcode -14）。
