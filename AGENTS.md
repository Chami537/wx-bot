# AGENTS.md — 给 Agent 的安装指令

**如果你是 Agent（例如 Grok Bot），用户让你「用 wx-bot 把微信接入你」，按本文件逐步执行。** 本文件优先于 README 里的其他描述。

## ⚠️ 必须遵守：把 Webhook 字段链接直接发在聊天里

创建 routine 后，**你的下一条聊天回复里必须包含两个可点击的字段链接：「Webhook URL」和「Webhook key」**，也就是 routine 详情面板里这两个字段的链接。

原因：Grok Bot 应用目前有缺陷，用户在侧栏「例行任务」里点 routine **打不开详情页**，自己找不到 URL 和 key。只有你在聊天里发出的字段链接，用户才点得开。

- ✅ 正确：回复里直接放「⊕ Webhook URL」「⊕ Webhook key」两个链接，并说明各自怎么用。
- ❌ 错误：「去右侧例行任务里点开『微信消息』查看」「在侧栏可以找到 URL 和 key」。
- ❌ 错误：只发一个链接，或只给文字不给链接。

回复示例：

> routine 已建好。请点 ⊕ Webhook URL，复制后贴回这里；再点 ⊕ Webhook key，复制后填进上面的加密输入框（不要贴在聊天里）。

## 安装步骤

1. **安装 wx-bot**

   ```bash
   git clone https://github.com/lawrencewzen/wx-bot
   cd wx-bot
   ./install.sh --service
   ```

2. **创建 routine**：触发方式选 **webhook**，名称如「微信消息」。指令里写明：
   - 收到的 JSON 中 `text` 是文字，`voice_text` 是语音转写，`media[].path` 是本机文件绝对路径，`from_wxid` 是发送者，`binding` 是绑定名。
   - 回复用 `~/.local/bin/wx-bot send --binding <binding> --to <from_wxid> --text '<回复>'`；图片、文件、视频用 `--image` / `--file` / `--video`。
   - 微信只显示纯文本，回复里不要用 Markdown。
   - `wx-bot send` 报 `-14` 或 `no context_token` 时，在 Grok Bot 聊天里告诉用户需要重新扫码登录。

3. **发出两个字段链接**：按上面「必须遵守」一节，在聊天回复里发出「Webhook URL」和「Webhook key」链接。

4. **接收 URL 和 key**
   - 用户把 Webhook URL 贴回聊天。
   - 弹出**加密输入框**让用户填 Webhook key，存为环境变量 `WXBOT_WEBHOOK_KEY`。不要让用户把 key 贴进聊天。

5. **写配置**
   - `~/.config/wx-bot/config.json`：`bindings[0].webhook.url` 填用户给的 URL，`key` 保持 `env:WXBOT_WEBHOOK_KEY`。不用签名时**删掉 `secret` 这一行**，否则环境变量未设置会导致启动失败。
   - `~/.config/wx-bot/env`：写入 `WXBOT_WEBHOOK_KEY=<key>`（格式 `KEY=value`，不加 `export`）。

6. **扫码登录**：运行 `wx-bot login xiaox`，把二维码展示给用户，让用户用微信扫码。二维码过期会自动刷新最多 3 次。

7. **启动**：按 `install.sh` 输出的命令加载 systemd / launchd 服务；或直接运行 `wx-bot run`。用 `wx-bot status` 确认在运行。

8. **告诉用户**：先用自己的微信给 ClawBot 发一条消息，之后就能对话。
