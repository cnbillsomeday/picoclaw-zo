# picoclaw-zo 自有功能代码审查报告

- 基准：`feat/p4-session-header-ui` @ `e8530fcb`（v0.5.3 合并后）
- 范围：zo 相对上游 v0.5.2 的 20 个专属提交（A2A、fish、mcpserver、session header、渠道修复、补丁点）
- 方法：L1 自动化（vet/gofmt/双 tag 构建/-race）→ L2 模式扫描 → L3 分模块审查 → L4 逐条核实 → 动态验证随修复做
- 结论：构建/vet/gofmt/race 全部干净；共确认 **P0×1、P1×10、P2 若干**；无安全越权类 P0

## 执行记录

| 层 | 内容 | 结果 |
|---|---|---|
| L1 | `go vet -tags goolm,stdjson ./pkg/... ./cmd/... ./web/backend/...` | ✅ 0 告警 |
| L1 | `gofmt -l`（自有代码） | ✅ 全部格式化 |
| L1 | 双 tag 构建：`goolm,stdjson` / `custom_channels,channel_a2a` | ✅/⚠️ custom 组合失败，但为**上游自身 bug**（v0.5.3 同样失败：weixin.go 带 tag、wecom.go 无条件引用其函数） |
| L1 | `go test -race ./pkg/channels/a2a ./pkg/fish ./pkg/fishhook ./pkg/mcpserver` | ✅ 0 DATA RACE |
| L2 | 模式扫描（锁覆盖/裸 goroutine/吞错/无超时 client/日志泄密/panic） | 命中项全部人工核实，见下表 |
| L3 | 5 个模块审查（A2A 由 Lead 自查，fish/mcpserver 由审查员完成，session header/修复/补丁由 Lead 自查） | 见下 |
| L4 | 所有 L2/L3 发现逐条对照源码核实 | fish 的 P0 与 mood/speak 断言已核实属实 |

## 问题清单

### P0（正确性，应立即修）

| # | 位置 | 问题 | 建议 | 修复成本 |
|---|---|---|---|---|
| 1 | pkg/fish/card.go:224 ImportCards | `ImportCards` 对 cards.jsonl 做 load→merge→writeJSONLines 全文件重写，**不在 withCardsLock 内**（AddCard 在锁内）。gateway/MCP 的 AddCard 与 `fish mem import` 并发时丢并发写入的卡；固定名 `.tmp` 也会互踩。已核实：函数体内无任何锁 | 主体包进 `withCardsLock(workspace, ...)` | S |

### P1（健壮性/安全，应修）

| # | 位置 | 问题 | 建议 | 成本 |
|---|---|---|---|---|
| 2 | pkg/channels/a2a/events.go:72 | 事件过滤 `evt.Scope.Channel != ""` 时才比较 → **Channel 为空的事件全部放行**。cron/fish speak 的 turn.end（无 inbound 上下文 → Scope.Channel 为空）结束时，若 A2A 恰有一个 pending，会被 `solePendingPeer` 误配对，用无关 turn 的状态结算 A2A 消息（可能假 completed + 清未读） | 改为严格匹配：`!strings.EqualFold(evt.Scope.Channel, c.Name())` 一律 return（A2A 投递的 turn 一定带 channel=a2a） | S |
| 3 | pkg/channels/a2a events.go:87-121 | `usageByTurn` 只在收到配对 turn.end 时清理；turn.end 丢失（订阅断开/事件丢弃）即**内存缓增**；空 TurnID 的事件全部堆积在 `""` 键 | 记录时间戳，pollLoop 周期清理超龄条目；空 TurnID 直接忽略 | S |
| 4 | pkg/channels/a2a events.go:123-156 | `pendingByPeer` 只在 turn.end 时删除；轮询超时路径（ExpirePendingInbound）只清理 state 不清理内存 map。事件丢失时条目滞留到同 peer 下一条消息覆盖 | ExpirePendingInbound 后同步删除对应内存条目（或统一以 state 为准、事件路径先查 state） | S |
| 5 | pkg/channels/a2a/notify.go:96-108 | reply 模式通知队列无 TTL：回复未发出时 notice 永久滞留，被之后任意一次成功 Send（可能是无关 proactive 后的普通回复）flush，内容过期 | 入队时记时间，flush/入队时丢弃超过 10 分钟的旧 notice | S |
| 6 | pkg/mcpserver/sessions.go:118-180 | meta.json 读-改-写无锁 + `writeJSONFile` 非原子：并发 append 丢计数；EndSession 落在窗口内会把已关闭会话复活；崩溃可见半截 JSON | 引入与 fish/lock.go 同款的跨进程文件锁；改原子写（tmp+rename） | M |
| 7 | pkg/mcpserver/config.go:83 sessions.go:42-76 | agent 名消毒对 `.`/`..` 放行：agent 名 `..` 时 sessionsRoot 逃逸到 workspace 根；消毒多对一（`a b`→`a_b`）可致两个 agent 共用目录 | LoadConfig 拒绝 `.`/`..`，用 sanitize 后的名字查重 | S |
| 8 | pkg/mcpserver/server.go:83-87 | http.Server 只有 ReadHeaderTimeout，缺 ReadTimeout/IdleTimeout（慢请求体挂连接） | 加 ReadTimeout 30s + IdleTimeout 120s（WriteTimeout 需先确认无长驻流） | S |
| 9 | pkg/mcpserver/config.go:98-108 server.go:64-66 | token 用 `==` 比较（非常量时间）；`CutPrefix("Bearer")` 不要求空格 | `subtle.ConstantTimeCompare`；要求 `Bearer ` 带空格 | S |
| 10 | pkg/fish/mood.go:108-118 | `LoadMoodConfig` 用 `_ = json.Unmarshal(raw, &cfg)`：损坏 JSON 得到「半文件半默认」配置且错误被吞（speak.go 的整体回落才是对的，两处不一致） | Unmarshal 到临时变量，成功才覆盖 | S |
| 11 | pkg/fish/mood.go:320-334,159 | `parseMoodTS` 解析失败回退 `now` → ageHours=0：坏 TS 事件**每次重算都按刚刚发生全额生效**，衰减对其永久失效 | 坏 TS 事件跳过 mood 贡献 | S |
| 12 | pkg/fish/speak.go:164-171,215-259 | speak state.json 读-改-写无锁：fishhook 进程（TouchInbound）与 cron/CLI（EvaluateSpeak）并发互相覆盖，"刚聊过不打扰"闸门可能失效 | 加 `withSpeakStateLock`（复用 lock.go 模式） | S |
| 13 | pkg/providers/common/common.go:64 + pkg/config/config.go:1108 | session_header 校验了名字合法性但**未拦截保留/敏感头**：`Authorization`、`Cookie`、`X-API-Key` 等可被配成 session header，把会话 ID 当凭据发出去（误配置即泄漏） | Validate() 拒绝保留头集合（Authorization/Host/Cookie/Content-Length/X-API-Key/Anthropic-Version 等） | S |

### P2（性能/健壮性，可选修）

| # | 位置 | 问题 | 建议 |
|---|---|---|---|
| 14 | pkg/channels/a2a/poller.go:43 | 每次轮询无条件 `state.Save()`（含 SetTick 变化），全量 JSON+rename 每 30s 一次；对嵌入式 SD 卡不友好 | 脏标记：仅 LastTick 外的数据变化才落盘，或 Tick 降频 |
| 15 | pkg/fish/card.go:154-157 | imp 无上限（文档 0~1，传 10 原样入库，得分因子放大 3.7 倍） | imp clamp 到 [0,1] |
| 16 | pkg/fish/card.go:176-209 | AddCard 不去重：同 text+t 产生重复 ID（ImportCards 有去重），superseded_by 歧义 | 同 ID 未取代行存在时幂等返回 |
| 17 | pkg/fish/score.go:341-344 | 单个汉字产不出 token（bigram 需两字），查"茶"永远空召回 | 单 Han 字也入 token |
| 18 | pkg/fish/speak.go:186-196 | IsPeak 不支持跨午夜（{22,6} 永假），与 InQuiet 语义不一致；PeakLine 等配置无校验 | 补跨午夜分支 + 配置 clamp |
| 19 | pkg/fish/speak.go:144-147 | LoadSpeakState 损坏 JSON 静默清零（Misses/LastSpoke 全丢） | 损坏时备份改名再回零值 |
| 20 | pkg/mcpserver/sessions.go:145-158 | 角色校验在写循环内：中途坏消息前半已落盘，meta 不更新 | 校验提到写文件前 |
| 21 | pkg/mcpserver/tools.go:35-38 | argString 对 content TrimSpace，与 verbatim 存储契约矛盾 | content 不 trim |
| 22 | 上游 bug（非 zo 引入） | `custom_channels` 组合构建失败：web/backend/api/weixin.go 带 `!custom_channels || channel_weixin`，wecom.go 无条件引用其 `generateQRDataURI`（v0.5.3 同样失败） | 可给 wecom.go 打同款 tag 或上抛上游；zo 侧未受影响 |

### P3（记录，不急）

- fish：score.go 注释/常量/测试三处门槛数字不一致（1.2/1.6/2.0）；Dropped.Reason 不区分"低于门槛/超额"；writeJSONLines 无 fsync；锁文件 60s 陈锁回收在极端慢写下可能双写者；speak/mood 配置 0o644 含个人数据建议 0600
- mcpserver：agentFrom 忽略 ok（缺纵深）；sessionID 4 字节随机同秒碰撞；呈现端 token trim 与配置端不一致；serve.go --host 非回环无警告；mood 加载错误被吞
- A2A：`handleRuntimeEvent` 中 `evt.Scope.Channel==""` 放行（与 #2 同源，随 #2 一并修）；payload 反射读取（性能可忽略，保持以避免 import cycle）
- 文档：`patches/0002` 描述的 initChannel 注入已被 bd359fe4 重构为 `attachRuntimeEvents`（StartAll/启动路径两处调用，均在 Start 之前，行为正确），patch 文档需要同步更新，否则未来 rebase 会按过期说明操作

### 测试缺口（修复时一并补）

1. ImportCards×AddCard 跨进程并发测试（对应 P0 #1）
2. mcpserver AppendMessages/EndSession 并发测试 + HTTP 级鉴权端到端测试（现有权限测试直接构造 ctx，绕过 authenticate 接线）
3. A2A：空作用域 turn.end 不结算 pending 的回归测试（对应 #2）
4. fish：坏 TS 事件、损坏 mood 配置、损坏 speak state 的降级测试
5. cmd/picoclaw/internal/fish 五个文件零测试

## 上游兼容性评估（补丁点）

| 补丁点 | v0.5.3 合并状态 | 评级 |
|---|---|---|
| event_payloads/pipeline_llm token 字段 | 上游未触碰，干净共存 | 好 |
| manager SetRuntimeEvents 注入 | 已被 bd359fe4 重构为 attachRuntimeEvents（Start 前注入，幂等）；上游未触碰 | 好（patches/0002 文档需更新） |
| instance.go a2a_send 注册 | 上游未触碰 | 好 |
| cmd_clear /new 别名 | 上游无同名命令；测试期望已在合并时适配 | 好 |
| web catalog init 追加 | 干净 | 好 |

## 建议修复批次

- **批次 1（P0 + 高危 P1）**：#1 ImportCards 加锁；#2 事件作用域收紧；#3/#4 A2A 内存条目清理；#6 mcpserver meta 锁+原子写；#7 名字校验；#8 HTTP 超时
- **批次 2（其余 P1）**：#5 通知队列 TTL；#9 token 恒时比较；#10/#11 mood 防脏数据；#12 speak state 锁；#13 保留头拦截
- **批次 3（P2 选择性）**：#14 状态写盘降频；#15-#21 按需
- 每批验证：`go build -tags goolm,stdjson ./...` + 相关包测试 + `-race` + A2A 集成测试

## 修复记录

### 批次 1（2026-11-03，commit e8530fcb 之后）

| 问题 | 状态 |
|---|---|
| #1 fish ImportCards 绕锁 | ✅ 已修（withCardsLock + CreateTemp 唯一临时文件） |
| #2 A2A 事件作用域过滤过松 | ✅ 已修（严格匹配，空作用域事件一律忽略） |
| #3 usageByTurn 无界增长 | ✅ 已修（时间戳 + 2×inboundTimeout 清理 + 忽略空 TurnID） |
| #4 pendingByPeer 与 state 不同步 | ✅ 已修（每轮 poll 前 prunePendingByPeer） |
| #6 mcpserver meta 竞态/非原子写 | ✅ 已修（withSessionLock + CreateTemp/rename） |
| #7 mcpserver 名字逃逸（`.`/`..`） | ✅ 已修（sessionDir + LoadConfig 双重拒绝） |
| #8 HTTP 超时缺失 | ✅ 已修（ReadTimeout 30s + IdleTimeout 120s） |
| 附带 | 上游子命令列表测试补 `fish`（既有漂移，非批次 1 引入） |

验证：build / vet / gofmt / 相关包单测 / `-race` / A2A 集成测试 / `cmd/picoclaw` 全部通过。

### 批次 2（2026-11-03）

| 问题 | 状态 |
|---|---|
| #5 A2A notify reply 队列无 TTL | ✅ 已修（queuedAt + 10 分钟 TTL，用渠道时钟判断，测试顺带暴露并修复了 wall-clock 错误） |
| #9 mcpserver token 非恒时比较 / Bearer 无空格 | ✅ 已修（ConstantTimeCompare 全候选扫描 + 强制 "Bearer " 前缀） |
| #10 fish LoadMoodConfig 半解析 | ✅ 已修（坏文件整体回落默认，字段级合并仅收正值） |
| #11 fish 坏 TS 事件全额生效 | ✅ 已修（parseMoodTS 返回 ok，坏事件跳过） |
| #12 fish speak state 无锁 | ✅ 已修（withSpeakStateLock + 原子写 + 0600） |
| #13 session header 保留头 | ✅ 已修（18 个保留头大小写不敏感拒绝） |

验证：build / vet / gofmt / fish+mcpserver+a2a+config 单测 / -race / A2A 集成 / web/backend/api 全部通过。
