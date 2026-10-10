<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" width="96" height="96" alt="wx-bot">
  </picture>
</p>

<h1 align="center">wx-bot</h1>

<p align="center">
  <strong>把微信接入到 Personal Agent（Grok、Muse 等）。</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/Linux-supported-FCC624?style=flat-square&logo=linux&logoColor=black" alt="Linux">
  <img src="https://img.shields.io/badge/macOS-supported-8E8E93?style=flat-square&logo=apple&logoColor=white" alt="macOS">
  <img src="https://img.shields.io/badge/WeChat-ClawBot%20%2F%20iLink-07C160?style=flat-square&logo=wechat&logoColor=white" alt="WeChat ClawBot / iLink">
  <img src="https://img.shields.io/badge/network-outbound%20only-2EA44F?style=flat-square" alt="outbound only">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="MIT License"></a>
</p>

<p align="center">
  微信消息经腾讯官方 <a href="https://ilinkai.weixin.qq.com">ClawBot / iLink</a> 进入本机 <code>wx-bot</code>，
  转成 JSON <code>POST</code> 到你的 Agent（例如 Grok Bot routine）；Agent 再用 <code>wx-bot send</code> 发回微信。
</p>

---

> [!IMPORTANT]
> **🤖 Agent 请先读 [`AGENTS.md`](https://github.com/lawrencewzen/wx-bot/blob/main/AGENTS.md)，按里面的步骤安装。**
> 最重要的一条：创建 routine 后，**必须在聊天回复里直接发出「Webhook URL」和「Webhook key」两个可点击的字段链接**。用户在侧栏点不开 routine 详情，不要让用户自己去找。

## 功能

- **双向收发**：收文本、图片、语音、文件、视频；回发文本、图片、文件、视频。语音优先附带微信转写文本。
- **只出站**：走 iLink 长轮询，本地发送 API 只监听 loopback，不对外开端口。
- **默认白名单**：只放行扫码登录的微信号；其他人的消息不转发、不回复。
- **多绑定**：一个微信号对应一个 webhook，会话过期只停该绑定。

平台：Linux / macOS（amd64、arm64），不支持 Windows。

```
+--------+   iLink getupdates    +--------+   POST JSON (webhook)   +---------+
|        | --------------------> |        | ----------------------> |         |
| WeChat |                       | wx-bot |                         |  Agent  |
|        | <-------------------- |        | <---------------------- |         |
+--------+   iLink sendmessage   +--------+   wx-bot send / API     +---------+
```

## 快速开始

### 交给 Grok Bot（推荐）

把下面这句话发给你的 Grok Bot：

```
帮我用 wx-bot 把微信接入你：https://github.com/lawrencewzen/wx-bot
```

它会按 [`AGENTS.md`](AGENTS.md) 装好，中间只需要你做三件事：点它发来的 Webhook URL 链接复制贴回聊天、点 Webhook key 链接复制填进加密输入框、用微信扫码。

### 手动安装

需要 Go 1.24+。

```bash
git clone https://github.com/lawrencewzen/wx-bot
cd wx-bot
./install.sh              # 装到 ~/.local/bin；加 --service 同时装 systemd / launchd 自启
```

编辑 `~/.config/wx-bot/config.json`，把 `webhook.url` 改成你的 Agent 地址；密钥写进 `~/.config/wx-bot/env`，配置里用 `env:VAR` 引用。

```bash
wx-bot login xiaox        # 终端出二维码，微信扫码
wx-bot run                # 启动
```

然后**先用你的微信给 ClawBot 发一条消息**，之后 Agent 才能回复你。

## 配置

```json
{
  "local_api": { "listen": "127.0.0.1:18790", "token": "" },
  "bindings": [
    {
      "name": "xiaox",
      "webhook": {
        "url": "https://example.com/webhook/wx-bot",
        "key": "env:WXBOT_WEBHOOK_KEY",
        "secret": "env:WXBOT_WEBHOOK_SECRET"
      },
      "allow_wxids": []
    }
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `local_api.listen` | 本地 API 地址，必须是 loopback。 |
| `local_api.token` | 非空时 `/v1/*` 需 `Authorization: Bearer <token>`。 |
| `webhook.key` | 默认以 `Authorization: Bearer <key>` 发送，可用 `key_header` / `key_prefix` 调整。 |
| `webhook.secret` | 非空时附加 `X-WxBot-Signature: sha256=<HMAC>` 与 `X-WxBot-Timestamp`。 |
| `allow_wxids` | 为空时只放行扫码登录的微信号。 |
| `forward_unallowed` | `true` 时非白名单消息也转发，但不回复。 |

完整示例：[`examples/config.example.json`](examples/config.example.json)。

## Webhook 载荷

```json
{
  "binding": "xiaox",
  "message_id": "1001",
  "from_wxid": "wxid_xxx",
  "create_time": 1760000000,
  "text": "文本",
  "voice_text": "语音转写",
  "media": [{ "type": "image", "path": "/abs/path", "file_name": "image.jpg", "size": 123 }],
  "reply_hint": "wx-bot send --binding xiaox --to wxid_xxx --text '...'"
}
```

`media.type` 为 `image` / `voice` / `file` / `video`，`path` 是本机绝对路径。超时 15s，5xx 与网络错误最多重试 3 次。

**接 Grok Bot**：`webhook.url` 填 routine 的 webhook 地址，`key` 填 routine 的发送密钥。

**接 Muse**：后续支持。

## 回微信

```bash
wx-bot send --binding xiaox --to <wxid> --text '你好'
wx-bot send --binding xiaox --to <wxid> --image ./a.jpg --caption '见图'
wx-bot send --binding xiaox --to <wxid> --file ./a.pdf
wx-bot send --binding xiaox --to <wxid> --video ./a.mp4
wx-bot status
```

或在 `wx-bot run` 期间调用本地 API：

```bash
curl -X POST http://127.0.0.1:18790/v1/send \
  -d '{"binding":"xiaox","to":"wxid_xxx","text":"你好"}'
```

只发纯文本，Markdown 请在 Agent 侧去掉。单文件上限 100 MB。

## FAQ

**poller 停了，日志提示 `-14`？** 会话过期，重新 `wx-bot login <binding>`。

**`send` 报 `no context_token`？** 该用户还没给 bot 发过消息，先发一条再回。

**二维码过期？** 自动刷新最多 3 次，仍失败就重跑 `login`。

## 开发

```bash
go vet ./... && go test ./...
make cross                # dist/wx-bot_{linux,darwin}_{amd64,arm64}
```

---

本项目使用腾讯官方 [ClawBot / iLink](https://ilinkai.weixin.qq.com) 接口，与腾讯无关，非腾讯官方产品。
