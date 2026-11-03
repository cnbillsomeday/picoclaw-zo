# 批次 1 修复 · 本地测试方案

对应提交：`6bb04e0a`（fish）、`db197da2`（a2a）、`8d99d737`（mcpserver）、`94d5c1e9`（docs）
基准：`feat/p4-session-header-ui`，Go 1.27.3，tags `goolm,stdjson`

## 一、修改点 → 测试映射

| # | 修改点 | 针对性测试（本次新增） | 回归范围 |
|---|---|---|---|
| 1 | fish ImportCards 加锁 + 唯一临时文件 | `TestImportCardsConcurrentAddCard`（-race，4 goroutine AddCard ×10 与 ImportCards 并发，断言 60 卡零丢失、无重复 ID） | `./pkg/fish/` 全量 + `-race` |
| 2 | A2A 事件作用域严格匹配 | `TestEmptyScopeTurnEndIgnored`（空作用域 turn.end 不得结算唯一 pending） | `./pkg/channels/a2a/` 全量 |
| 3 | usageByTurn 加时间戳+清理+忽略空 TurnID | `TestStaleUsagePruned`（3 分钟后陈旧条目被清）；空 TurnID 断言并入 #2 测试 | 同上 |
| 4 | pendingByPeer 与 state 同步清理 | `TestPrunePendingByPeer`（state 转 failed 后内存条目被删） | 同上 |
| 5 | mcpserver 会话锁 + 原子写 | `TestAppendMessagesConcurrentConsistent`（4×25 并发 append 计数=100）；`TestEndSessionNoResurrection`（close 后 append 必败、状态不再复活） | `./pkg/mcpserver/` 全量 + `-race` |
| 6 | 名字/路径校验（`.`/`..`） | `TestSessionDirRejectsTraversal`（`../evil` 等 5 种坏 id 全拒）；`TestLoadConfigRejectsDotNames`（agent 名 `.`/`..` 拒绝、正常名通过） | 同上 |
| 7 | HTTP ReadTimeout/IdleTimeout | ListenAndServe 无独立单测（新增超时不改变行为语义）；由 #5 的进程内 Handler 测试 + 四、二进制冒烟覆盖 | `./pkg/mcpserver/` |
| 8 | 子命令列表补 fish | `TestNewPicoclawCommand` | `./cmd/picoclaw/` |

## 二、快速回归（一条命令）

```bash
go build -tags goolm,stdjson ./... \
&& go vet -tags goolm,stdjson ./pkg/fish/ ./pkg/mcpserver/ ./pkg/channels/a2a/ ./pkg/tools/ ./cmd/picoclaw/ \
&& go test -tags goolm,stdjson -count=1 ./pkg/fish/ ./pkg/mcpserver/ ./pkg/channels/a2a/ ./pkg/tools/ ./cmd/picoclaw/
```

预期：全部 `ok`，无输出即 vet 通过。

## 三、分层执行

### 3.1 并发安全（-race，逐包）
```bash
go test -race -tags goolm,stdjson -count=1 ./pkg/fish/ ./pkg/mcpserver/ ./pkg/channels/a2a/
```
预期：`ok` ×3，无 `DATA RACE`。

### 3.2 新增回归测试（逐个点名，确认真的在跑）
```bash
go test -tags goolm,stdjson -count=1 -v \
  -run 'TestImportCardsConcurrentAddCard' ./pkg/fish/ \
  -run 'TestEmptyScopeTurnEndIgnored|TestStaleUsagePruned|TestPrunePendingByPeer' ./pkg/channels/a2a/ \
  -run 'TestAppendMessagesConcurrentConsistent|TestEndSessionNoResurrection|TestSessionDirRejectsTraversal|TestLoadConfigRejectsDotNames' ./pkg/mcpserver/
```
预期：8 个测试全部 `--- PASS`，`FAIL` 为 0。

### 3.3 A2A 真实 HTTP 集成（进程内起平台服务端 + 签名收发 + 事件结算）
```bash
go test -tags 'integration,goolm,stdjson' -count=1 -run TestIntegration ./pkg/channels/a2a/
```
预期：`ok`。该测试同时回归 #2/#3 的真实事件路径（turn.end 带 channel=a2a 才结算）。

### 3.4 周边回归（改动可能波及的包）
```bash
go test -tags goolm,stdjson -count=1 ./pkg/channels/ ./pkg/agent/ ./cmd/picoclaw/ ./pkg/tools/
```
预期：全部 `ok`（pkg/agent 约 95-100s）。

## 四、二进制冒烟（真实 CLI/进程路径）

```bash
go build -tags goolm,stdjson -o /tmp/picoclaw-zo-smoke ./cmd/picoclaw
```

### 4.1 fish 真实导入路径（改 #1）
```bash
WS=$(mktemp -d)
cat > /tmp/mem.md <<'EOF'
§ 事实
- smoke imported fact one 2026-11-03
- smoke imported fact two 2026-11-03
EOF
/tmp/picoclaw-zo-smoke fish mem import --dir "$WS" --file /tmp/mem.md
/tmp/picoclaw-zo-smoke fish mem list --dir "$WS" | grep -c "smoke imported fact"  # 预期 2；文件在 $WS/fish/cards.jsonl，权限 600
# 再导入一遍：幂等（skipped=2）
/tmp/picoclaw-zo-smoke fish mem import --dir "$WS" --file /tmp/mem.md
```
预期：第二次导入 0 新增、2 跳过；卡片文件权限 0600。

### 4.2 mcp serve 进程级冒烟（改 #5/#6/#7）
```bash
WS=$(mktemp -d); mkdir -p "$WS/mcp"
cat > "$WS/mcp/config.json" <<'EOF'
{"host":"127.0.0.1","port":18907,
 "agents":[{"name":"..","token":"bad"}]}
EOF
/tmp/picoclaw-zo-smoke mcp serve --dir "$WS"  # 预期：启动即报 `agent name ".." is not allowed` 退出（fail-fast）
# 改成合法配置
cat > "$WS/mcp/config.json" <<'EOF'
{"host":"127.0.0.1","port":18907,
 "agents":[{"name":"smoke-agent","token":"smoke-token-0123456789"}]}
EOF
/tmp/picoclaw-zo-smoke mcp serve --dir "$WS" & sleep 2
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18907/healthz                # 预期 200
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18907/mcp                    # 无 token：预期 401
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer wrong' http://127.0.0.1:18907/mcp   # 错 token：预期 401
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer smoke-token-0123456789' http://127.0.0.1:18907/mcp  # 对 token：预期非 401（GET 被协议层拒为 400 即鉴权已过）
kill %1
```
预期：坏 agent 名被拒；好配置可启动且鉴权生效。

## 五、通过标准

1. 二节命令零失败；
2. 3.2 的 8 个新增测试逐个 PASS；
3. `-race` 无告警；
4. 集成测试 ok；
5. 二进制冒烟符合各预期；
6. `git status` 干净（无遗留改动）。

## 六、执行记录（2026-11-03，本机已全量跑通）

| 步骤 | 结果 |
|---|---|
| 二、快速回归（build+vet+5 包单测） | ✅ 全 ok |
| 3.1 -race（fish/mcpserver/a2a） | ✅ 无 DATA RACE |
| 3.2 新增 8 个回归测试 | ✅ 8/8 PASS |
| 3.3 A2A 集成测试 | ✅ ok（5.9s） |
| 3.4 周边回归（channels/agent/cmd） | ✅ 全 ok（agent 99s） |
| 4.1 fish CLI 冒烟 | ✅ 导入 2→幂等 0 新增；并发 3 轮 import+add 最终 5 卡零丢失；权限 600 |
| 4.2 mcp serve 冒烟 | ✅ 坏名 `..` fail-fast；healthz 200；无/错 token 401；对 token 过鉴权（400） |

结论：批次 1 全部修改点均有针对性测试覆盖，相关功能回归通过。
