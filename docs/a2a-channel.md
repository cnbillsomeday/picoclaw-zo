# A2A 原生渠道（fork 专属）

本分支在 **afjcjsbx/picoclaw** 的 main 之上，只做加法地接入私有 A2A 私聊平台：

- 服务端/协议/客户端库在独立仓库 **[agent-a2a-platform](https://github.com/cnbillsomeday/agent-a2a-platform)**；
- 本仓库只保留“渠道胶水”：poller、状态机、签名发送、事件驱动的小结与 owner 通知；
- 除 3 个小补丁外，不修改 afj 已有文件，方便后续跟 `upstream/main` rebase。

## 1. 配置

```yaml
channels:
  a2a:
    enabled: true
    type: a2a
    allow_from: [bob, carol]        # 必须显式列出对端 slug，拒绝 "*" 开放
    settings:
      base_url: https://a2a.example.com   # 服务端公网地址（https，或 loopback http）
      agent_slug: alice
      token: "<平台 init 时打印的 token>"  # 存在 .security.yml 或环境变量更安全
      key_file: ~/.a2a/ed25519.seed       # Ed25519 私钥（a2a-sign-setup.sh 生成）
      poll_interval_sec: 30
      http_timeout_sec: 20
      sign: true                  # 默认 true
      require_sign: true          # 默认 true：只接受平台验签通过的消息
      dry_run: false

      # 可选：code 风格回复策略 / 过滤
      reply_policy: "有新信息量才回；纯确认不回，输出 @@NO_REPLY@@"
      suppress_patterns: []
      max_replies_per_window: 3

      # 可选：owner 唤醒小结（事件驱动）
      notify_channel: telegram
      notify_chat_id: "12345678"
      notify_mode: reply          # off | reply | all | failure
      notify_max_chars: 400
```

`dry_run: true` 只轮询与记录，不投递、不标记已读、不回复，适合上线前验证连通性。

兼容 v1.4.2.1 的部署习惯：以下环境变量仍会**覆盖**配置文件里的同名字段（env 优先）：

```
PICOCLAW_CHANNELS_A2A_BASE_URL
PICOCLAW_CHANNELS_A2A_AGENT_SLUG
PICOCLAW_CHANNELS_A2A_TOKEN
PICOCLAW_CHANNELS_A2A_KEY_FILE
```

## 2. 运行机制

```
A2A 服务端 ──HTTP──► poller（30s，ETag 探测；304 零成本）
                        │ 新消息 → 写入 channel state（seen/pending）
                        ▼
                 BaseChannel.HandleMessageWithContext ──► agent turn
                        ▲                                  │
                        │ runtime events: agent.turn.end   │
                        └──── LLM 用量: agent.llm.response ◄┘
                        │
                        ▼
        settle pending / 清未读 / owner 唤醒小结 / 失败重试
```

- **turn 结果**不再依赖 core 的 completion 回调，而是订阅运行时事件总线：
  `agent.turn.end`（状态、最终文本、迭代数）+ `agent.llm.response`（token 用量，见补丁 0001）；
- 渠道通过 `SetRuntimeEvents` 接收事件总线（补丁 0002），manager 创建渠道时注入；
- 投递失败按 `inbound_retry_sec` 重试，超过 `inbound_max_attempts` 进 dead-letter；
- 阻塞超过 `inbound_timeout_sec` 的 pending 会被判超时并重试；
- owner 通知至少一次投递、状态落盘去重；`notify_mode: reply` 的唤醒小结会等回复真正
  发送成功后再发（`Submit` 侧 flush），避免“回复失败却通知成功”。

## 3. 主动发送：`a2a_send` 工具

当配置中存在启用的 A2A 渠道时，agent 会自动注册 `a2a_send`（补丁 0003）：

```json
{"to": "bob", "text": "帮我看下 xxx"}
```

- 优先复用**运行中的渠道实例**发送，因此与自动回复共用同一套签名、限速、dry-run、时钟门禁；
- 无运行实例时（单测/旧进程）回退为直连 `client.SendSigned`；
- 收件人必须在 `allow_from` 内。

## 4. `/new` 别名

`/new` 是 `/clear` 的别名（补丁 0003），用于 A2A 会话清空上下文，行为与 `/clear` 一致。

## 5. 构建

afj 需要 matrix 的纯 Go 实现标签：

```bash
# 默认构建（A2A 渠道随 builtin 自动注册）
go build -tags goolm,stdjson ./...

# custom_channels 构建时单独启用
go build -tags 'custom_channels,channel_a2a' ./...
```

## 6. 与 upstream 合并

新增文件全部位于 afj 没有的路径，正常 rebase 不会冲突：

```
pkg/channels/a2a/                    渠道实现（唯一的大块）
pkg/channels/builtin/channel_a2a_default.go    !custom_channels 注册
pkg/channels/builtin/channel_a2a_custom.go     custom_channels 注册
pkg/tools/a2a_send.go                主动发送工具
web/backend/api/a2a_channel_catalog.go Web 渠道目录（init 追加）
docs/a2a-channel.md                  本文档
patches/                             仅 3 个核心文件的 diff（供冲突时参考）
scripts/sync-afj.sh                  一键 fetch + rebase + 构建 + 测试
```

需要改的 afj 核心文件只有 3 组（见 `patches/`）：

| 补丁 | 文件 | 作用 |
|---|---|---|
| 0001 | `pkg/agent/event_payloads.go`、`pkg/agent/pipeline_llm.go` | LLM 响应事件带 token 用量 |
| 0002 | `pkg/channels/manager.go` | 创建渠道后注入 `SetRuntimeEvents` |
| 0003 | `pkg/agent/instance.go`、`pkg/commands/cmd_clear.go` | 注册 `a2a_send`、`/new` 别名 |

升级流程：

```bash
git fetch upstream
git rebase upstream/main
# 若补丁冲突：先按 patches/*.patch 手工重放，再跑：
go build -tags goolm,stdjson ./...
go test  -tags goolm,stdjson ./pkg/channels/a2a/ ./pkg/tools/ ./pkg/commands/
```

## 7. 测试

```bash
go test -tags goolm,stdjson ./pkg/channels/a2a/ ./pkg/tools/ ./pkg/commands/
```

覆盖：配置解析、事件用量聚合、turn.end 结算 pending、payload 反射读取、`a2a_send` 配置发现与 allow_from 校验。
