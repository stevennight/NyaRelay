# Plan 003: UDP 多候选会话粘滞

## 目标

在 Plan 002 已经实现 TCP 多候选 `round_robin`、`random`、`failover` 的基础上，补齐 UDP 在多候选 tunnel stage 下的选择策略。

UDP 不能简单按每个 datagram 重新选择候选，否则同一客户端会话可能在多个 middle/exit 节点之间跳动，破坏 QUIC、游戏协议、DNS relay、STUN 类协议的会话语义。本阶段目标是实现 UDP session affinity：

- 新 UDP 会话第一次出现时，按 stage strategy 选择候选节点。
- 同一 UDP 会话后续 datagram 固定走同一个候选节点。
- 候选 dial/write 失败时，清理该会话并按策略重选。
- 会话空闲超时后自动清理，避免 session map 无限增长。
- Stage 可以分别配置 TCP/UDP 策略。
- Stage candidate 可以声明支持 TCP、UDP 或 TCP+UDP，避免 UDP 表现差的节点参与 UDP 路径。
- TCP 现有选择器语义不回退。

## 非目标

- 不做 UDP 每包负载均衡。
- 不做跨节点共享的 UDP 会话状态。
- 不做基于延迟/丢包的 `best_latency`。
- 不改变外部入口多节点的调度模型；入口多节点仍依赖外部 DNS/LB 或客户端选择。
- 不把 controller 放到数据面中转路径。
- 不在第一版引入协议级独立权重；candidate 的 `weight` 先同时作用于其启用的协议。

## 当前状态

当前 TCP 下一跳选择已经通过 `candidateSelector` 实现：

- `round_robin`
- `random`
- `failover`
- failure timeout
- candidate weight

但 UDP 在 `dialForwardNext` 中遇到 `network != "tcp"` 时直接使用下一层第一个节点：

```go
nextNode := nextStage.Nodes[0]
```

因此 UDP 目前在多候选 stage 下不会真正使用 `round_robin`、`random`、`failover`。

## 核心决策

UDP 多候选必须是“按会话选候选”，不是“按包选候选”。

同时，stage 的候选节点集合不能再假设 TCP 和 UDP 完全相同。某些服务商或网络环境下，节点的 TCP 很稳定，但 UDP 丢包、限速或不可用。因此 Plan 003 同时引入两个协议维度：

1. Stage 支持 TCP/UDP 独立策略。
2. Stage candidate 支持协议 mask。

推荐兼容模型：

```json
{
  "strategy": "failover",
  "tcp_strategy": "round_robin",
  "udp_strategy": "failover",
  "nodes": [
    {
      "node_id": "hk-a",
      "weight": 1,
      "protocols": ["tcp", "udp"]
    },
    {
      "node_id": "hk-b",
      "weight": 1,
      "protocols": ["tcp"]
    },
    {
      "node_id": "hk-c",
      "weight": 1,
      "protocols": ["tcp", "udp"]
    }
  ]
}
```

兼容规则：

- `strategy` 保留为默认策略。
- `tcp_strategy` 为空时回退 `strategy`。
- `udp_strategy` 为空时回退 `strategy`。
- candidate `protocols` 为空时按 `["tcp", "udp"]` 处理，兼容旧配置。
- TCP selector 只从支持 TCP 的 candidate 中选择。
- UDP selector 只从支持 UDP 的 candidate 中选择。

候选列表建模：

- UI 可以仍然以一个 stage candidate 列表呈现，避免用户为 TCP 和 UDP 重复维护两份节点顺序、权重和配置。
- 每一行 candidate 必须可以分别勾选 TCP、UDP；运行时按协议 mask 派生出两份逻辑候选列表：
  - TCP effective candidates = `protocols` 为空或包含 `tcp` 的 candidate。
  - UDP effective candidates = `protocols` 为空或包含 `udp` 的 candidate。
- 因此“同一个 stage 选择 TCP/UDP 不同策略”不代表两种协议必须共用完全相同的节点集合。策略是 per-protocol，候选集合也是 per-protocol 派生。
- 某个服务商节点如果 TCP 稳定但 UDP 丢包、限速或不可用，应只保留 TCP 勾选；它继续参与 TCP 转发，但不会参与 UDP 转发。
- 如果一个 forward 启用了 TCP+UDP 并绑定到同一个 tunnel，保存或运行前应确保该 tunnel 每个必经 stage 都至少有一个 TCP candidate 和一个 UDP candidate，否则对应协议应给出明确错误。
- 权重第一版仍挂在 candidate 行上，并作用于该 candidate 已启用的协议；后续如需要再扩展 `tcp_weight` / `udp_weight`。

推荐会话键：

```text
tunnel_id
forward_id
from_stage_index
to_stage_index
network
client_addr
```

其中 `client_addr` 在入口 forward 处是用户的 UDP remote addr；在 stage 间转发时是上一跳的地址或 UDP session 的源标识。实现时可以先从入口 UDP session 开始，逐步扩展到 stage 内部 session。

## 运行时设计

新增 UDP candidate session manager：

```text
udp_candidate_session:
  key
  candidate_node_id
  candidate_index
  last_seen
  created_at
```

职责：

1. 收到 UDP datagram。
2. 根据 session key 查询已绑定候选。
3. 如果存在且未过期，继续使用该候选。
4. 如果不存在或已过期，调用现有 `candidateSelector.order(...)` 获取候选顺序。
5. selector 先按 network 过滤 candidate 协议 mask，再按对应协议策略排序。
6. 选第一个可用候选并写入 session map。
7. dial/write/hello 失败时：
   - `recordFailure(...)`
   - 删除当前 session
   - 尝试下一个候选
8. 成功时：
   - `recordSuccess(...)`
   - 更新 `last_seen`

TCP 也应使用同一套协议过滤逻辑：

- network = `tcp` 时使用 `tcp_strategy`，并过滤只支持 TCP 的 candidate。
- network = `udp` 时使用 `udp_strategy`，并过滤只支持 UDP 的 candidate。

可将 selector 输入从完整 stage 改为“按协议裁剪后的 stage view”，或让 selector 自身接收 network 并在内部过滤 candidate。

```text
candidateSelector.order(tunnelID, stage, network):
  strategy = strategyForNetwork(stage, network)
  candidates = candidatesForNetwork(stage.Nodes, network)
  return ordered candidates
```

## 策略语义

`single`:

- 首次会话绑定第一个候选。
- 候选失败时可以按现有 fallback 行为尝试同 stage 其他可用候选，或严格只使用第一个。推荐第一版严格保持 single，只用第一个。
- `single` 的“第一个候选”指该协议可用 candidate 列表里的第一个。

`failover`:

- 新会话优先绑定第一个可用候选。
- 主候选失败后，在 fail timeout 内新会话绑定备选。
- 已有会话只有在实际写入失败时才切换。
- TCP 和 UDP 可以有不同 failover 顺序，因为协议 mask 会改变可用 candidate 列表。

`round_robin`:

- 新会话按会话粒度轮询候选，而不是按 datagram 轮询。
- 同一 session 内所有 datagram 固定走第一次选中的候选。
- 支持 weight。
- TCP 和 UDP 的 round-robin cursor 应分开维护，避免 TCP 连接影响 UDP 会话分布。

`random`:

- 新会话按权重随机选择候选。
- 同一 session 内保持粘滞。

## 会话超时

第一版建议默认：

```text
udp_session_idle_timeout = 120s
udp_session_gc_interval = 30s
```

这些可以先写成 node runtime 常量，后续再暴露为配置。

清理策略：

- 每次访问 session 时顺便判断是否过期。
- 后台 goroutine 周期性清理。
- `Service.Apply(...)` 重载配置时清空所有 UDP candidate sessions，避免旧 tunnel/stage/candidate 状态污染新配置。

## 返回包路径

UDP session affinity 的关键不是只管入口包，也要保证返回包仍回到同一条链路。

当前 UDP 已经有 datagram-over-stream 机制。补多候选时要确保：

- 一个 UDP session 对应一个已选下一跳 candidate。
- 返回 datagram 写回同一个上游 session。
- 不因为下一包重新选择候选而让返回路径漂移。

如果现有 UDP relay service 已经维护 client addr 到 stream/session 的映射，应优先把 candidate binding 挂在现有 session 对象上，而不是另起一套重复 session map。

## 失败处理

可检测失败：

- dial next stage 失败。
- hello write 失败。
- stream write 失败。
- session stream 已关闭。

处理方式：

1. 记录候选失败。
2. 删除该 UDP session 的候选绑定。
3. 对当前 datagram 尝试下一个候选。
4. 如果全部失败，丢弃当前 datagram 并记录日志/metrics。

不可检测失败：

- UDP 远端无响应。
- 应用层丢包。
- 中间网络黑洞但本地 write 未报错。

第一版不主动探测这类失败，避免引入复杂健康检查。后续可以通过 traffic/agent error 或可选探测增强。

## 统计

保持现有统计 ID 约定：

- `forward:<forwardID>`
- `tunnel:<tunnelID>:stage:<stageIndex>`
- `tunnel:<tunnelID>:stage:<stageIndex>:candidate:<nodeID>`
- `target:<forwardID>`

UDP 多候选成功选中 candidate 后，应该和 TCP 一样把流量统计到 candidate 维度。

新增可选内部指标：

- active UDP sessions
- session create count
- session expire count
- candidate reselect count
- candidate failure count

这些可以先只用于日志或测试，不急着暴露 UI。

## API 和 UI

需要在 tunnel stage 和 stage candidate 上扩展字段，同时保留旧字段兼容。

Stage 新增：

```json
{
  "strategy": "failover",
  "tcp_strategy": "round_robin",
  "udp_strategy": "failover",
  "nodes": []
}
```

Candidate 新增：

```json
{
  "node_id": "mid-a",
  "weight": 1,
  "protocols": ["tcp", "udp"]
}
```

旧配置：

```json
{
  "strategy": "round_robin",
  "nodes": [
    { "node_id": "mid-a", "weight": 1 },
    { "node_id": "mid-b", "weight": 1 }
  ]
}
```

等价于：

```json
{
  "strategy": "round_robin",
  "tcp_strategy": "",
  "udp_strategy": "",
  "nodes": [
    { "node_id": "mid-a", "weight": 1, "protocols": ["tcp", "udp"] },
    { "node_id": "mid-b", "weight": 1, "protocols": ["tcp", "udp"] }
  ]
}
```

UI 建议：

```text
TCP 策略: [round_robin]
UDP 策略: [failover]

候选节点          TCP   UDP   权重
hk-a              ✓     ✓     1
hk-b              ✓           1
hk-c              ✓     ✓     1
```

文案上明确：

- TCP 按连接应用策略。
- UDP 按会话应用策略。
- candidate 未勾选 UDP 时，不会参与 UDP 转发。
- 同一 stage 表面上是一份候选列表，但 TCP/UDP 会按勾选状态形成不同的有效候选集合。
- 入口多节点仍由外部 DNS/LB/客户端选择。

保存和校验：

- candidate 至少启用一个协议；如果用户取消 TCP 和 UDP，应阻止保存或自动恢复为 TCP+UDP。
- 对 TCP-only forward，tunnel 每个必经 stage 至少需要一个 TCP effective candidate。
- 对 UDP-only forward，tunnel 每个必经 stage 至少需要一个 UDP effective candidate。
- 对 TCP+UDP forward，tunnel 每个必经 stage 同时需要 TCP 和 UDP effective candidates；二者可以是同一批节点，也可以不同。
- 详情页和调试日志应展示每个 stage 的 TCP effective candidates / UDP effective candidates，避免排障时误以为两种协议一定共享同一候选集合。

## 测试

Go unit:

- `strategyForNetwork` 正确回退到 `strategy`。
- candidate `protocols` 为空时默认支持 TCP+UDP。
- TCP selector 不选择只支持 UDP 的 candidate。
- UDP selector 不选择只支持 TCP 的 candidate。
- TCP 和 UDP round-robin cursor 互不影响。
- UDP session 首包按 `round_robin` 选择候选。
- 同一 session 后续 datagram 继续使用同一候选。
- 不同 client addr 产生不同 session，可按策略分散。
- session idle timeout 后重新选择候选。
- candidate write/dial failure 后删除 session 并重选。
- `failover` 主候选失败后新 session 使用备选。
- `random` 不按 datagram 抖动。

Go integration:

- UDP forward 经过多候选 middle stage，同一 client 多包固定到同一 middle。
- 两个 client 在 round robin 下分布到不同候选。
- TCP+UDP forward 使用同一个 tunnel 时，TCP 和 UDP 分别使用自己的策略和候选协议 mask。
- UDP 不会选中只启用 TCP 的候选节点。
- 关闭一个候选节点后，新 UDP session 切换到备选。
- session 超时后，恢复候选可以重新被选择。
- TCP 现有多候选行为不回归。

前端:

- stage 表单可以分别保存 TCP/UDP 策略。
- candidate 行可以勾选 TCP/UDP 支持协议。
- 未勾选任何协议时应阻止保存，或自动恢复为 TCP+UDP。
- 旧 tunnel 打开后默认显示候选支持 TCP+UDP。
- TCP+UDP forward 绑定 tunnel 时，如果某个必经 stage 缺少 TCP 或 UDP 有效候选，应显示明确校验错误。

## 风险

- UDP session map 泄漏会造成内存增长，需要超时清理和 Apply 清理。
- 每包选路会破坏协议，必须确保按 session 粘滞。
- failover 不应过度积极，否则短暂丢包会导致路径频繁漂移。
- UDP 无连接，很多失败无法立即检测，不要承诺强健康检查语义。
- 如果 session key 设计过粗，会把不同客户端粘到同一候选；过细则负载分散不足。
- 协议 mask 会让同一 stage 的 TCP/UDP 实际候选集合不同，详情页和调试日志必须展示清楚，否则排障容易误判。
- 旧配置兼容必须明确，避免已有 tunnel 因缺少 `protocols` 被当成不可用。

## 交付顺序

1. 扩展模型：stage 增加 `tcp_strategy` / `udp_strategy`，candidate 增加 `protocols`。
2. 做旧配置兼容：空策略回退 `strategy`，空 protocols 默认 TCP+UDP。
3. 更新 selector：按 network 选择策略并过滤 candidate 协议 mask。
4. 梳理当前 UDP relay session/stream 生命周期。
5. 设计并实现 UDP candidate session manager。
6. 将 UDP 下一跳选择从固定 `Nodes[0]` 改为 session-affinity selector。
7. 接入 failure record、success record 和 candidate metrics。
8. 加 session idle timeout 和 GC。
9. 更新 UI：TCP/UDP 策略下拉、candidate TCP/UDP 勾选。
10. 补 unit tests。
11. 补 UDP 多候选 integration tests。
12. 更新 UI/文档说明：TCP 按连接、UDP 按会话，candidate 可按协议启用。

## 验收标准

- TCP 多候选测试全部保持通过。
- UDP 多候选不再固定使用第一个候选。
- TCP 和 UDP 可以在同一个 stage 使用不同策略。
- TCP 和 UDP 可以在同一个 stage 使用不同 candidate 子集。
- 同一 stage 只维护一份 candidate 配置时，运行时仍能正确派生 TCP/UDP 两份有效候选集合。
- 同一 UDP client session 不会在候选间逐包跳动。
- 候选失败后，新 UDP session 能切到可用候选。
- session 超时后能被清理并允许重新选择。
