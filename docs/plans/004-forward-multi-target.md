# Plan 004: Forward 多目标负载均衡与故障转移

## 状态

本文档是设计草案，只记录方案，不包含代码、数据库或前端实现。

## 背景

当前 `Forward` 只有一个 `target`。`Tunnel` 已经支持每个 stage 配置多个候选节点，并在 node relay 中按 `round_robin`、`random`、`failover` 选择 TCP/UDP 路径；UDP 使用会话粘滞，避免同一个会话在候选节点之间跳动。

本计划把 Forward 的最终目标从单个地址扩展为目标池，使一条转发可以表达：

```text
client
  -> entry node(s), 入口调度由外部 DNS/LB/客户端负责
  -> tunnel stage 1 candidates
  -> tunnel stage 2 candidates
  -> exit node candidates
  -> forward target candidates
```

每一层独立选择候选。一个连接或 UDP 会话可以因此得到一条完整路径，但不在已建立连接中途透明迁移路径。

## 目标

- 一个 Forward 可以配置多个最终目标地址。
- 目标池支持 `round_robin`、`random`、`failover`。
- TCP 按连接选择目标。
- UDP 按会话选择目标，同一会话后续数据报继续使用同一目标。
- 目标连接或首个请求失败时，可以按策略尝试其他目标。
- 目标池和 Tunnel stage 候选池可以同时工作，形成链路级和目标级的负载均衡/故障转移。
- 控制器不进入数据转发路径；控制器短时不可用时，已应用的配置和本地选择器继续工作。
- 不处理多入口 DNS、入口健康检查或入口地址发布。

## 非目标

- 不在 TCP 已建立连接中途切换到另一个目标。切换会破坏连接语义，只能由客户端重连触发。
- 不做 UDP 每数据报负载均衡。UDP 必须以会话为粒度保持粘滞。
- 不做跨 node 共享的目标健康状态。每个 node 根据自己的拨号/写入结果维护本地状态。
- 不在第一版做主动探测、延迟评分、丢包评分或全局最优路径。
- 不改变外部入口的 DNS/LB 方案。多入口仍由外部系统或客户端选择。

## 核心决策

### 1. Forward 目标池与 Tunnel 候选池是两层资源

Tunnel stage candidate 表示“下一个 relay node”；Forward target 表示“最终业务地址”。两者不能混成一个候选列表：

- Tunnel selector 负责选择下一跳 relay node，并建立 relay link。
- Forward target selector 只在 direct Forward 的入口节点，或 chain Tunnel 的出口节点选择最终地址。
- 中间节点不需要知道目标地址，也不应接收目标池配置。

示例：

```text
entry
  -> middle-a / middle-b       Tunnel stage selector
  -> exit-a / exit-b           Tunnel stage selector
  -> app-a / app-b / app-c     Forward target selector
```

每层都可以使用不同策略。例如中间层使用 `round_robin`，出口层使用 `failover`，最终目标使用 `round_robin`。

### 2. 选择策略与 Tunnel stage 保持一致

Forward 使用与 Tunnel stage 相同的字段语义：

```json
{
  "strategy": "failover",
  "tcp_strategy": "round_robin",
  "udp_strategy": "failover"
}
```

兼容规则：

- `tcp_strategy` 为空时回退到 `strategy`。
- `udp_strategy` 为空时回退到 `strategy`。
- 单目标默认等价于 `single`。
- 多目标未指定策略时默认使用 `failover`，保留配置顺序并优先保证可用性。
- `round_robin` 和 `random` 的 `weight` 只影响新 TCP 连接或新 UDP 会话，不影响已绑定会话。

### 3. 目标候选支持协议裁剪

为了与 Tunnel stage candidate 的 TCP/UDP mask 对齐，每个目标可以声明支持的协议：

```json
{
  "id": "app-a",
  "address": "10.0.0.11:443",
  "protocols": ["tcp"],
  "weight": 2,
  "enabled": true
}
```

规则：

- `protocols` 为空时兼容为同时支持 TCP 和 UDP。
- TCP 只从支持 TCP 的目标中选择。
- UDP 只从支持 UDP 的目标中选择。
- Forward 开启 TCP、UDP 或 TCP+UDP 时，保存校验必须保证目标池对每个启用协议至少有一个有效目标。
- `weight <= 0` 归一化为 `1`。
- `enabled=false` 的目标不参与选择，但保留在配置中供恢复和审计。

## 推荐数据模型

### Forward target

```go
type ForwardTarget struct {
    ID        string            `json:"id"`
    ForwardID string            `json:"forward_id,omitempty"`
    Address   string            `json:"address"`
    Protocols []ForwardProtocol `json:"protocols,omitempty"`
    Weight    int               `json:"weight,omitempty"`
    Enabled   bool              `json:"enabled"`
    Position  int               `json:"position"`
}
```

`Position` 是 failover 的配置顺序，也是 UI 展示和审计的稳定顺序。目标 ID 应保持稳定；修改地址时尽量保留 ID，以便 metrics 和故障状态可读。

### Forward 与 runtime

```go
type Forward struct {
    ID          string
    Name        string
    TunnelID    string
    Protocols   []ForwardProtocol
    Listen      string
    Targets     []ForwardTarget
    Strategy    string
    TCPStrategy string
    UDPStrategy string
    Enabled     bool
}

type ForwardRuntime struct {
    ID          string
    Name        string
    TunnelID    string
    Protocols   []ForwardProtocol
    Listen      string
    Targets     []ForwardTarget
    Strategy    string
    TCPStrategy string
    UDPStrategy string
    Enabled     bool
}
```

当前 `Target string` 应作为迁移期兼容字段保留在持久化/API 层，但 runtime 应统一使用已经归一化的 `Targets`，避免同时维护两个真相来源。

## 持久化与 API

推荐新增 `forward_targets` 表，而不是把完整目标对象继续塞进单个 JSON 字段：

```text
forward_targets
  id
  forward_id
  position
  address
  protocols_json
  weight
  enabled
  created_at
  updated_at
```

`forwards` 增加：

- `strategy`
- `tcp_strategy`
- `udp_strategy`

旧 `target` 字段在迁移期保留，或者至少支持旧 API 请求。建议：

1. 读取旧 Forward 时，将非空 `target` 转换成一个 `ForwardTarget`。
2. 新 API 使用 `targets`；只有 `targets` 缺失时才读取旧 `target`。
3. 保存后以 `forward_targets` 为唯一 runtime 来源。
4. 对同时提交 `target` 和 `targets` 的请求返回明确错误，避免用户误以为两个字段会合并。
5. 保留旧字段一段时间，用于回滚和旧客户端兼容；新 UI 不再编辑单个 `target` 字段。

Forward API 建议：

```json
{
  "name": "https-backend",
  "tunnel_id": "tun-1",
  "protocols": ["tcp"],
  "listen": ":8443",
  "strategy": "failover",
  "tcp_strategy": "round_robin",
  "targets": [
    {"id": "app-a", "address": "10.0.0.11:443", "weight": 2, "enabled": true},
    {"id": "app-b", "address": "10.0.0.12:443", "weight": 1, "enabled": true}
  ],
  "enabled": true
}
```

Forward 目标不需要端口分配，因为它们是 relay 的出站地址，不是本地 listener。现有入口端口分配规则保持不变：多入口时仍需要所有入口节点共享一个可用监听端口。

## Node 配置裁剪

目标地址只下发给真正可能发起最终连接的节点：

| 节点角色 | `listen` | `targets` | 说明 |
| --- | --- | --- | --- |
| direct Forward 的入口 | 是 | 是 | 入口节点同时负责最终拨号 |
| chain 的入口 | 是 | 否 | 只拨号到下一个 Tunnel stage |
| chain 的中间节点 | 否 | 否 | 只负责 relay transit |
| chain 的出口 | 否 | 是 | 负责最终目标选择和拨号 |

这与当前单目标 `scopeForwardRuntime` 的裁剪方向一致。`RelayHello` 不应携带目标地址或由入口指定的目标下标；出口应依据已签名配置和 `ForwardID` 自己选择目标，避免入口通过 hello 绕过出口配置。

## Runtime 行为

### TCP

1. Forward listener 接收一个 client connection。
2. Tunnel stage selector 按当前 stage 的策略选择下一 relay candidate；拨号或 hello 失败时尝试同层其他 candidate。
3. 到达 direct 入口或 chain 出口后，Forward target selector 按 TCP 策略生成目标顺序。
4. 依次拨号目标，失败目标记录本地失败状态，并尝试下一个目标。
5. 目标连接成功后开始 pipe；该 TCP connection 绑定到这个目标直到结束。

故障转移只发生在连接建立阶段。已建立的 TCP 连接无法无损迁移；目标中途故障后，客户端需要重连，新的连接再按策略选择目标。

### UDP

UDP 目标选择必须以会话为粒度：

1. 新 UDP session 第一次到达时，按 UDP 策略选择一个有效目标。
2. session key 至少包含 `forward_id`、协议和客户端地址/现有 `SessionID`。
3. 后续 datagram 复用该目标，不按包重新做 round robin。
4. 目标拨号、首个写入或可判断的 round trip 失败时，删除当前绑定并按策略尝试下一个目标。
5. 空闲超时后清理 binding；下一次数据报创建新 session 并重新选择。
6. `Service.Apply` 应清理旧配置对应的 UDP target binding，避免目标列表变更后继续使用旧下标。

与 Tunnel UDP candidate 相同，UDP 目标故障的检测存在边界：远端没有响应可能是业务行为，也可能是网络黑洞。第一版可以只利用拨号、写入、读取超时等可观察失败，不做主动健康探测。

### 目标选择器状态

目标状态应与 Tunnel candidate 状态分开，至少按以下维度隔离：

```text
forward_id
target_id
network
```

每个 node 本地维护：

```text
target_state:
  consecutive_failures
  last_failure_at
  disabled_until
```

第一版可以复用现有 selector 的默认值和状态算法：

- `max_fails = 1`
- `fail_timeout = 60s`
- `dial_timeout` 沿用当前拨号超时策略

这些状态不需要写入 controller，也不要求各 exit node 共享。配置更新、node 重启或状态过期后重新建立即可。

## 组合后的故障转移边界

一条 chain Forward 的新连接可能经历多层独立选择：

```text
entry
  -> middle candidate A/B
  -> exit candidate X/Y
  -> target candidate P/Q
```

每层选择成功后才进入下一层。某一层的拨号失败只在该层尝试其他候选；不会回溯并重新计算整条全局路径。这样可以控制复杂度，但也意味着：

- 一次连接可能选择 `middle-A -> exit-Y -> target-Q`。
- 如果 exit 已经建立，但其所有 target 都不可用，连接最终失败，不会自动改选入口或前面的 middle。
- 如果 Tunnel candidate 在连接建立后断开，现有连接终止；客户端重连时才会触发新的 Tunnel/target 选择。

这已经能覆盖中间链路、Tunnel 多中间节点/多出口、Forward 多目标三个层面的负载均衡和故障转移，但不是全局有状态流量调度器。

## 入口范围

本计划不改变入口调度：

- 多入口节点仍监听同一个 Forward 端口。
- controller 继续负责为多入口分配共同可用端口。
- 哪个入口接到客户端连接，仍由 DNS、外部 LB、Anycast 或客户端自行决定。
- controller 不作为入口反向代理，也不在入口和 node relay 之间转发业务流量。

因此，入口节点本身全部故障时，Forward target pool 不能补救；需要外部 DNS/LB 的健康检查和切换。

## Controller 是否必须持续在线

### 结论

不需要。对于已经成功启动、已经应用有效签名配置的 node，controller 不应是数据转发链路的硬依赖。现有架构也已经按这个方向实现：node 本地保存最后一份有效配置，controller WebSocket 断开后继续运行 relay，并在后台重连。

因此，在 controller 暂时宕机时，下面这些能力仍可以工作：

- 已有 TCP 连接继续转发，直到连接自身结束或链路故障。
- 新 TCP 连接使用 node 本地的 Tunnel candidate 和 Forward target selector。
- Tunnel 中间节点/出口节点的本地故障转移。
- Forward target 的拨号失败转移。
- UDP 已建立 session 的会话粘滞，以及可观察失败后的本地重选。

### Controller 不可用时会失去的能力

| 能力 | controller 离线时的结果 |
| --- | --- |
| 修改 Forward target、策略或 Tunnel 候选 | 不会下发到 node；继续使用旧配置 |
| 新 node 首次获取配置 | 不能完成 bootstrap |
| 新 Tunnel/Forward 生效 | 不能完成编译、签名和下发 |
| pause/resume、吊销 node | 不能即时推送控制变更 |
| 心跳、metrics、审计写入 | 暂时无法回传或落库，取决于接口是否可达 |
| 外部入口 DNS/LB 调度 | 不属于 controller 当前数据面；由外部系统决定 |

### 当前实现的特别注意

当前 node 的有效配置流程是“HTTP 拉取失败后尝试本地缓存，WebSocket 断线后持续重连”。`RelayConfig.ExpiresAt` 已存在于模型，但当前签名校验路径主要校验签名和 `NodeID`，没有把过期时间作为运行拦截条件。因此在现状下，已缓存配置可能在 controller 长时间离线时继续运行。

这有利于可用性，但会带来控制面撤销延迟：controller 离线时不能立即撤销旧配置或旧 node。后续应单独确定离线宽限策略，例如：

- 允许旧配置在 `offline_grace_period` 内继续接收新连接；
- 宽限期后停止新 listener，但是否保留已有连接需要明确；
- 或继续保持无限离线运行，把撤销和安全边界交给 node 本地策略。

这不应与本计划的目标池选择器绑定实现，应作为 controller/node 生命周期策略单独设计。

## 校验规则

保存 Forward 时：

- `targets` 至少一个。
- 每个目标的 `address` 必须是合法 `host:port`。
- 目标 ID 在同一个 Forward 内唯一。
- 目标地址不能是空字符串。
- 目标至少启用一个协议；协议为空按 TCP+UDP 兼容处理。
- 每个 Forward 启用的协议，必须至少有一个 `enabled` 且支持该协议的目标。
- Tunnel 每个必经 stage 的候选校验继续沿用现有规则。
- chain Forward 只允许在出口节点真正使用目标；中间节点配置不应依赖目标地址。

运行时也应再次保护：

- 只接受本地签名配置中的 `ForwardID`。
- 不信任 `RelayHello` 携带的目标地址或目标下标。
- 不允许目标 selector 跨 Forward 复用状态。
- 配置 revision 改变后清理旧 UDP target binding。

## Metrics 与可观测性

保留现有统计维度，并增加目标维度：

```text
forward:<forward_id>
tunnel:<tunnel_id>:stage:<stage_index>
tunnel:<tunnel_id>:stage:<stage_index>:candidate:<node_id>
target:<forward_id>:<target_id>
```

至少需要能区分：

- 每个 target 的 bytes、connections。
- target dial failure 次数。
- target 当前是否处于本地 disabled/fail timeout。
- UDP active sessions、session expire、session reselect。

第一版不必把所有本地健康状态做成 controller UI；但日志和测试必须能定位“没有目标”“目标被暂时禁用”“所有目标拨号失败”。

## 测试计划

### Unit

- 单目标配置兼容旧 `target`。
- `strategy` 回退到 `tcp_strategy`/`udp_strategy`。
- `protocols` 过滤正确，TCP 不选 UDP-only，UDP 不选 TCP-only。
- weighted round robin 和 random 的候选顺序符合预期。
- failover 按 position 顺序选择，并跳过 disabled/暂时禁用目标。
- target failure timeout 到期后可以再次选择。
- TCP selector 和 UDP selector 的状态互不干扰。
- UDP 同一 session 后续 datagram 保持同一 target。
- UDP session 过期或写入失败后删除 binding 并重选。
- runtime config revision 改变时清理旧 target session。

### Integration

- direct TCP Forward 在两个目标之间 round robin 分配连接。
- chain TCP Forward 在 exit 节点对多个目标做 failover。
- Tunnel middle/exit candidate 与 Forward target pool 同时启用时，失败可以在各自层内转移。
- TCP 已建立连接在目标故障后不伪造透明迁移；客户端重连后可以选择健康目标。
- direct UDP 多目标对同一客户端保持 target affinity。
- chain UDP 多目标对同一客户端保持 exit/target affinity。
- 一个 target 只支持 TCP 时，不参与 UDP Forward。
- controller 断开后，node 使用缓存配置继续处理新连接和本地故障转移。
- controller 恢复后，新的 revision 能更新 target pool，并清理旧 UDP binding。
- config scoping 不把 target pool 下发给 chain middle node。

### 前端

- Forward 编辑器支持添加、删除、排序目标。
- 每个目标显示 address、权重、启用状态和 TCP/UDP 支持。
- TCP/UDP 策略可分别选择，并给出空候选校验。
- 详情页展示目标池和当前策略，而不是只展示单个目标地址。
- 单目标旧 Forward 打开后显示为一个目标行，不改变用户现有配置。

## 实施顺序

1. shared model 增加 `ForwardTarget`、target pool 和策略字段。
2. store 增加 target 持久化、旧 `target` 迁移和事务写入。
3. validation 与 controller API 支持目标池及协议裁剪。
4. config compiler 按 entry/direct/exit 角色裁剪目标池。
5. node relay 抽象 target selector，先完成 TCP direct/chain。
6. 增加 TCP target failure retry 和 metrics。
7. 增加 UDP target session affinity，并与现有 UDP candidate affinity 对齐。
8. 集成测试覆盖 controller 离线、配置 revision、目标故障和链路故障。
9. 最后实现 Forward UI 和迁移提示。

## 风险与待确认项

- UDP “无响应”不一定表示目标故障；首版超时重试可能增加单个数据报延迟。
- 目标池在每个 exit node 本地选择，同一时刻不同 exit 可能有不同的健康视图，这是预期行为。
- 目标列表变更是否关闭已有 TCP/UDP session，需要与现有 `Service.Apply` 的 listener 生命周期一起确认。
- 是否要把 `max_fails`、`fail_timeout`、`dial_timeout` 暴露到 Forward 级配置，建议第一版沿用现有默认值。
- 是否在后续增加 target 权重按 TCP/UDP 分离，目前建议共用 `weight`，与 Tunnel candidate 保持一致。
