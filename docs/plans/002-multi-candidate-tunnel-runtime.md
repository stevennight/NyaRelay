# Plan 002: 多候选隧道运行时

## 目标

在 Plan 001 的一等 `Node / Tunnel / Forward` 模型稳定后，把 tunnel stage 从“每层一个节点”升级为“每层多个候选节点”，支持更接近 flvx 的负载均衡、容灾和多入口/多出口隧道。

目标能力：

- 一个 tunnel 可以有多个入口节点。
- 每一层 hop 可以配置多个候选节点。
- 出口节点可以有多个候选。
- 每层支持策略：`round_robin`、`random`、`failover`，后续可加 `best_latency`。
- 失败时可以重试同层其他候选。
- traffic stats 能按 forward、tunnel、link candidate 聚合。

## 非目标

- 不做 SSH、远程 shell、nftables 主动变更。
- 不做跨面板 federation。
- 不做纯 NAT 反向数据通道。
- 不在第一版里实现复杂质量评分或全局最优路径。

## 为什么这是第二阶段

Plan 001 会先把公开模型切换为 `Tunnel / Forward`，但第一版 runtime 可以只执行每个 stage 的单节点路径。要支持多候选，需要继续扩展 node dialer、selector、统计和错误处理。这个改动会碰到数据面主路径，适合作为第二步单独做。

## 配置模型升级

Plan 001 的 runtime config 已经预留 stage/candidate 结构：

```go
type TunnelRuntimeStage struct {
    Index    int                 `json:"index"`
    Role     string              `json:"role"`
    Strategy string              `json:"strategy"`
    Nodes    []TunnelRuntimeNode `json:"nodes"`
}
```

Plan 002 主要把 `Nodes` 从“实际只允许一个”升级为“允许多个候选”，并让 node runtime 真正执行策略选择和失败重试。

## Tunnel 模型升级

Plan 001 的 tunnel 从每层单节点为主，升级成：

```text
tunnel:
  entry_groups:
    - nodes: [cn-1, cn-2]
      strategy: failover
  hop_groups:
    - nodes: [hk-1, hk-2]
      strategy: round_robin
    - nodes: [jp-1, jp-2]
      strategy: failover
  exit_group:
    nodes: [us-1, us-2]
    strategy: random
```

controller 仍然可以将 tunnel 编译成 link candidates：

```text
entry candidates -> hop group 1 candidates -> hop group 2 candidates -> exit candidates -> target
```

## Relay 数据面

node relay 新增：

- hop selector
- failure tracker
- retry budget
- per-candidate metrics

TCP 流程：

1. route listener 接收 client connection。
2. `dialRouteNext(route, hopIndex)` 读取当前 hop group。
3. selector 根据 strategy 选择 candidate link。
4. dial 失败时记录失败，并按策略尝试下一 candidate。
5. 成功后写入 hello，包含 route ID、hop index、selected candidate 信息。
6. 下一跳重复相同逻辑，直到最终 target。

UDP 流程：

- 第一版可只支持单 candidate，或者对 UDP 做固定会话粘滞。
- 若要支持多 candidate，需要按 client addr 维护 session affinity，避免同一 UDP 会话在候选间跳动。

建议第二阶段先把多候选限制为 TCP，UDP 继续沿用单路径，直到 session affinity 完成。

## 策略

`round_robin`:

- 每个 hop group 在 node 内维护递增 index。
- dial 失败则尝试下一 candidate。

`random`:

- 随机候选。
- dial 失败则随机剩余候选。

`failover`:

- 按配置顺序尝试。
- 主 candidate 失败后，在 fail timeout 内优先备选。

后续 `best_latency`:

- controller 或 node 定期探测候选质量。
- selector 使用最近探测结果排序。

## 健康状态

node 本地维护：

```text
candidate_state:
  consecutive_failures
  last_failure_at
  disabled_until
```

配置项：

- `max_fails`
- `fail_timeout`
- `dial_timeout`
- `retries`

第一版默认：

- `max_fails = 1`
- `fail_timeout = 60s`
- `dial_timeout = 10s`

## Controller 配置裁剪

当前 `scopeConfigForNode` 需要升级：

- 当前节点是 route entry，则需要 route。
- 当前节点属于任意 hop group candidate 的 from/to node，则需要 route。
- 当前节点可能接收某条 link，则需要对应 link server settings。
- 当前节点可能拨出某条 link，则需要对应 link client settings。

注意：

- 候选 links 的证书材料仍然按 from/to node 裁剪。
- 不应把不相关 link secret 或私钥下发给节点。

## 统计

新增维度：

- forward stats
- tunnel stats
- hop group stats
- candidate link stats

node report 可以扩展：

```go
type TrafficStat struct {
    ID          string
    Kind        string
    BytesIn     int64
    BytesOut    int64
    Connections int64
}
```

或保持当前 report 字段，新增 `TunnelStats`、`ForwardStats`。

推荐 ID 约定：

- `forward:<id>`
- `tunnel:<id>`
- `hop:<routeID>:<hopIndex>`
- `link:<linkID>`
- `target:<routeID>`

## API 和 UI

Tunnel 表单升级：

- 入口节点支持多个。
- 中间层支持“添加一层”，每层多个节点。
- 出口节点支持多个。
- 每层可选策略。
- 每个候选可选权重。

Forward 表单升级：

- 如果 tunnel 有多个入口节点：
  - 自动分配入口端口时，需要在所有入口节点上找共同可用端口。
  - 自定义端口也必须在所有入口节点可用。
- 显示所有入口地址：
  - `entry-1.example.com:port`
  - `entry-2.example.com:port`

## 数据迁移

1. 保留 Plan 001 的 `tunnels` 和 `forwards`。
2. 新增 tunnel group JSON 字段或独立表：
   - `tunnel_entry_nodes`
   - `tunnel_hop_groups`
   - `tunnel_hop_nodes`
   - `tunnel_exit_nodes`
3. 给 `routes` 新增 `hop_groups_json`。
4. 旧 `hops_json` 迁移为 `hop_groups_json`。
5. 保留旧字段一段时间，用于回滚和兼容。

## 测试

Go unit:

- selector round robin/random/failover。
- failure tracker fail timeout。
- config scoping 不泄露无关证书/secret。
- old route hops 兼容迁移。

Go integration:

- TCP 多候选 hop，主链路关闭后备选成功。
- 多入口 forward 共同端口分配。
- 多出口 round robin 能分散连接。
- disabled candidate 不再被选择，timeout 后可恢复。

UDP:

- 单 candidate 兼容。
- 若实现 session affinity，测试同一 client addr 固定候选。

前端:

- tunnel 多层编辑。
- 策略选择。
- 多入口地址展示。

## 风险

- 数据面主路径变复杂，必须有集成测试兜住。
- UDP 多候选容易破坏会话语义，建议延后。
- 配置裁剪一旦做错，可能泄露 link 私钥或 secret。
- 多入口共同端口分配要处理并发保存。
- 失败重试会增加连接建立延迟，需要合理 timeout。

## 交付顺序

1. Route hop group model 和旧 hops 兼容。
2. node selector 和 TCP 多候选 dial。
3. controller config scoping 升级。
4. tunnel 多候选编译到 route hop groups。
5. stats 扩展。
6. API tests 和 TCP integration tests。
7. 前端多层 tunnel 编辑。
8. UDP session affinity 或明确限制 UDP 单候选。
