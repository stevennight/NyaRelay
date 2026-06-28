# Plan 001: Tunnel + Forward 一等模型

## 目标

开发期可以重建配置数据，因此第一阶段直接把 NyaRelay 改成 `Node / Tunnel / Forward` 的一等模型，而不是继续维护 `Link / Route` 兼容层。

- `Node`：承载流量的节点。
- `Tunnel`：可复用的转发路径，描述入口、中间节点、出口节点和节点间传输方式。
- `Forward`：具体转发规则，选择一个 tunnel，设置入口端口和目标地址。

保留 NyaRelay 最初的安全边界：

- controller 不保存 SSH 密钥。
- controller 不主动 SSH 到节点。
- controller 不下发任意 shell 命令。
- node 主动连接 controller，拉取签名后的结构化配置。
- node 本地运行受限 runtime，按签名配置启动/停止监听和转发。

## 关键决策

- 废弃公开的 `links` 和 `routes` 概念，UI 和 API 改为 `tunnels` 和 `forwards`。
- 旧开发数据可以清空重建，不写复杂迁移。
- `nyarelay-node` 仍然是一个进程，内部包含 agent 和 dataplane runtime。
- 第一阶段优先用自研 relay runtime，不引入 nftables 作为主路径。
- direct forward、chain forward、TCP、UDP、TCP+UDP 都由 node agent 下发的签名配置管理。
- nftables 后续只作为单节点 direct forward 的可选 fast path，不进入 Plan 001 主线。
- 直入直出仍然走 `nyarelay-node` 里的 runtime：入口节点启动 forward listener，然后直接拨 target；它不启动 tunnel stage listener，也不分配内部 tunnel port。
- `tcp+udp` 只是 UI/API 展示概念，存储和 runtime config 中统一规范化为 `["tcp","udp"]`。
- UDP chain 走 datagram-over-stream framing。`direct`、`tls`、`mtls`、`ws-tls` transport 都是节点间 stream transport，不为 UDP 单独开 stage UDP port。
- controller 对 tunnel/forward 的写入必须在同一事务或同一写锁里完成校验、端口分配、对象写入和 revision bump，避免并发创建时抢占同一端口。

## 非目标

- 不做 SSH、远程 shell、nftables 主动操作。
- 不做跨面板 federation。
- 不做纯 NAT 打洞或完全反向数据隧道。
- 不做成熟质量探测、best-exit、自动选路。
- 不要求第一版完成每一跳多候选负载均衡；数据模型预留候选结构，完整策略放到 Plan 002。

## 运行形态

`nyarelay-node` 内部拆成两个逻辑模块：

```text
agent:
  - 连接 controller
  - 节点认证
  - 拉取并验签 RelayConfig
  - 上报心跳、系统状态、流量统计

runtime:
  - 应用 RelayConfig
  - 启动 forward TCP/UDP listener
  - 启动 tunnel stage listener
  - 处理 direct 和 chain 数据转发
```

agent path 永远存在。dataplane path 第一阶段都是自研 relay runtime。

## 可达性假设

agent 连接只负责认证、配置下发和状态上报，不承载普通业务数据流量。Plan 001 不做“所有数据都经 controller 反向中转”的形态。

因此数据面需要满足：

- client 必须能访问 forward 的入口地址。
- chain stage `i` 的节点必须能访问 stage `i+1` 的 `public_addr` 或 `connect_addr`。
- middle/exit stage listener 所在节点必须对上一 stage 可达。
- 纯 NAT 后、没有端口映射或可用 `connect_addr` 的节点，不能作为需要被拨入的 stage listener。
- `connect_addr` 用于私网互通、专线、内网地址覆盖；`public_addr` 用于默认公网拨入地址。

这和 flvx 可能存在的反向隧道或中心转发形态不同。NyaRelay 第一阶段先完成 signed config + node runtime + peer relay 的主路径，后续如果要支持 NAT-only 节点，需要单独设计 reverse data channel 或 controller/edge relay，不混进 Plan 001。

## 产品模型

### Node

保留现有节点能力：

- 节点注册、吊销、心跳。
- 节点主动拉取配置。
- `public_host`
- `port_min`
- `port_max`
- system info 和状态。

后续可补充：

- IPv4/IPv6 偏好。
- 额外出口地址。
- 默认监听地址。

### Tunnel

Tunnel 是路径模板，不直接暴露用户入口端口。

第一阶段支持两种 tunnel type：

```text
direct:
  entry node -> target

chain:
  entry node -> optional middle nodes -> exit node -> target
```

第一阶段 UI 限制为线性路径：

```text
entry: one node
middle: zero or more nodes, ordered
exit: one node
```

数据模型仍使用 stage/node 结构，为 Plan 002 的每层多候选预留空间。

Tunnel transport 表示节点间 stage 连接方式：

- `direct`：原始 TCP tunnel stream。
- `tls`：TLS tunnel stream。
- `mtls`：mTLS tunnel stream。
- `ws-tls`：HTTP Upgrade over TLS tunnel stream。

注意这里的 `TunnelTransportDirect` 只表示“节点间 stream 不包 TLS”。它不是 `TunnelDirect`。  
`TunnelDirect` 没有节点间 stage listener；`TunnelChain` 才会使用 transport 建立 stage 之间的 stream。

校验规则：

- `direct` tunnel type 不需要节点间 transport，存储时可固定为 `direct`。
- `chain` tunnel type 必须有 entry 和 exit。
- 第一阶段每个 stage 只有一个 node。
- 同一个 tunnel 内节点不能重复。
- `ws-tls` 需要 `server_name` 或可由 public host 推导。

第一阶段 stage 语义固定为：

- direct tunnel：只保存一个 `entry` stage，`stage_index = 0`，不分配内部端口、secret 或证书。
- chain tunnel：保存 `entry -> middle... -> exit`，`stage_index` 从 0 连续递增。
- chain entry stage 不监听内部 tunnel port，只启动 forward listener。
- chain middle/exit stage 启动 stage listener，供上一 stage 拨入。
- stage `i` 只允许拨向 stage `i+1`，第一阶段不允许跳 stage。

### Forward

Forward 是具体入口服务：

```text
client -> forward listen on entry node -> tunnel -> target
```

字段：

- name
- tunnel_id
- protocols: tcp / udp / tcp+udp
- listen: 留空自动分配，也允许自定义
- target: host:port
- enabled

同一个 forward 可以同时启用 TCP 和 UDP。实现上在同一个入口地址上分别启动 TCP listener 和 UDP listener，共用同一个 forward、tunnel、target、权限和统计维度。

`listen` 是 entry node 本地 bind 地址。留空时 controller 自动分配为 `:port`；UI 展示入口地址时用 entry node 的 `public_host` 加该端口生成 public endpoint。自定义 `listen` 允许 `:port`、`0.0.0.0:port`、`[::]:port` 或明确的本机地址。

如果 tunnel 是 direct，entry node 直接拨 target。  
如果 tunnel 是 chain，entry node 先拨下一段 tunnel，最终 exit node 拨 target。

## 数据库

开发期可以重建数据库。建议直接新增目标表，旧 `links`、`routes` 可删除或保留不用。

```sql
tunnels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  transport TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

tunnel_stages (
  id TEXT PRIMARY KEY,
  tunnel_id TEXT NOT NULL,
  stage_index INTEGER NOT NULL,
  role TEXT NOT NULL,
  strategy TEXT NOT NULL DEFAULT 'single',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

tunnel_stage_nodes (
  id TEXT PRIMARY KEY,
  tunnel_id TEXT NOT NULL,
  stage_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  listen_addr TEXT NOT NULL DEFAULT '',
  public_addr TEXT NOT NULL DEFAULT '',
  connect_addr TEXT NOT NULL DEFAULT '',
  weight INTEGER NOT NULL DEFAULT 1,
  settings_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

forwards (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  tunnel_id TEXT NOT NULL,
  protocols_json TEXT NOT NULL,
  listen TEXT NOT NULL,
  target TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

port_allocations (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  owner_kind TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  protocol TEXT NOT NULL,
  port INTEGER NOT NULL,
  bind_addr TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(node_id, protocol, port)
);
```

Recommended indexes and constraints:

```sql
CREATE UNIQUE INDEX idx_tunnel_stage_order ON tunnel_stages(tunnel_id, stage_index);
CREATE INDEX idx_tunnel_stage_nodes_tunnel ON tunnel_stage_nodes(tunnel_id);
CREATE INDEX idx_tunnel_stage_nodes_stage ON tunnel_stage_nodes(stage_id);
CREATE INDEX idx_forwards_tunnel ON forwards(tunnel_id);
CREATE INDEX idx_port_allocations_owner ON port_allocations(owner_kind, owner_id);
```

`port_allocations` 是端口占用的来源：

- forward TCP listener：`owner_kind = 'forward'`，`protocol = 'tcp'`。
- forward UDP listener：`owner_kind = 'forward'`，`protocol = 'udp'`。
- chain stage listener：`owner_kind = 'tunnel_stage_node'`，`protocol = 'tcp'`。
- direct tunnel 不写入内部 tunnel port allocation。

`protocols_json` 统一存 JSON array，且写入前规范化、去重、排序，只允许：

- `["tcp"]`
- `["udp"]`
- `["tcp","udp"]`

Stage role:

- `entry`
- `middle`
- `exit`

Stage index:

- direct tunnel: `entry` is `0`。
- chain tunnel: `entry` is `0`，middle stages are `1..n`，exit is `n+1`。

`tunnel_stage_nodes` address fields:

- `listen_addr`：当前 stage node 实际 bind 的内部 tunnel 地址，entry stage 通常为空。
- `public_addr`：上一段 stage 应该拨入的地址，默认由 node `public_host` 和内部端口生成。
- `connect_addr`：拨号覆盖地址，通常为空；用于手动指定内网地址或特殊线路。

`settings_json` 只给 controller 和 config compiler 使用，不作为普通 API 原样回显。API 返回 tunnel/stage/forward 时默认隐藏或脱敏以下字段：

- `secret`
- `server_key`
- `client_key`
- `server_cert`
- `client_cert`
- `ca_cert`

TLS/mTLS/ws-tls 推荐沿用当前证书材料形态：

- listener node 获取 `secret`、`server_cert`、`server_key`。
- dialing node 获取 `secret`、`ca_cert`。
- mTLS dialing node 额外获取 `client_cert`、`client_key`。
- `server_name` 可存放在 stage node settings，默认由 `public_addr` host 推导。

## 类型定义

不要继续复用旧 `RouteProtocol` 命名。改为 forward/tunnel 语义：

```go
type TunnelType string

const (
    TunnelDirect TunnelType = "direct"
    TunnelChain  TunnelType = "chain"
)

type TunnelTransport string

const (
    TunnelTransportDirect TunnelTransport = "direct"
    TunnelTransportTLS    TunnelTransport = "tls"
    TunnelTransportMTLS   TunnelTransport = "mtls"
    TunnelTransportWSTLS  TunnelTransport = "ws-tls"
)

type ForwardProtocol string

const (
    ForwardProtocolTCP ForwardProtocol = "tcp"
    ForwardProtocolUDP ForwardProtocol = "udp"
)

type TunnelStageRole string

const (
    TunnelStageEntry  TunnelStageRole = "entry"
    TunnelStageMiddle TunnelStageRole = "middle"
    TunnelStageExit   TunnelStageRole = "exit"
)
```

## Runtime Config

替换当前以 `Links` 和 `Routes` 为中心的配置：

```go
type RelayConfig struct {
    Revision  int64            `json:"revision"`
    IssuedAt  time.Time        `json:"issued_at"`
    NodeID    string           `json:"node_id"`
    Nodes     []Node           `json:"nodes"`
    Tunnels   []TunnelRuntime  `json:"tunnels"`
    Forwards  []ForwardRuntime `json:"forwards"`
    ExpiresAt time.Time        `json:"expires_at"`
}

type TunnelRuntime struct {
    ID        string               `json:"id"`
    Name      string               `json:"name"`
    Type      TunnelType           `json:"type"`
    Transport TunnelTransport      `json:"transport"`
    Stages    []TunnelRuntimeStage `json:"stages"`
    Settings  map[string]string    `json:"settings,omitempty"`
}

type TunnelRuntimeStage struct {
    Index    int                 `json:"index"`
    Role     TunnelStageRole     `json:"role"`
    Strategy string              `json:"strategy"`
    Nodes    []TunnelRuntimeNode `json:"nodes"`
}

type TunnelRuntimeNode struct {
    NodeID      string            `json:"node_id"`
    ListenAddr  string            `json:"listen_addr,omitempty"`
    PublicAddr  string            `json:"public_addr,omitempty"`
    ConnectAddr string            `json:"connect_addr,omitempty"`
    Weight      int               `json:"weight,omitempty"`
    Settings    map[string]string `json:"settings,omitempty"`
}

type ForwardRuntime struct {
    ID        string            `json:"id"`
    Name      string            `json:"name"`
    TunnelID  string            `json:"tunnel_id"`
    Protocols []ForwardProtocol `json:"protocols"`
    Listen    string            `json:"listen,omitempty"`
    Target    string            `json:"target,omitempty"`
    Enabled   bool              `json:"enabled"`
}
```

`ForwardRuntime` 可按节点裁剪：

- direct tunnel entry node 需要 `Listen`、`Target`、`Protocols`，用于启动入口 listener 并直接拨 target。
- chain entry node 需要 `Listen`、`Protocols`，用于启动入口 listener；它不需要 `Target`。
- chain middle node 至少需要 `ID`、`TunnelID`、`Protocols`、`Enabled`，用于校验 hello 和统计；它不需要 `Target`。
- chain exit node 需要 `Target`，最终拨号时必须从签名配置查 target。

第一阶段开发目标是最小必要裁剪：不参与该 tunnel/forward 的 node 不下发；chain middle 不下发 target；私钥和 secret 只按 stage 方向下发。无论裁剪粒度如何，exit 都不能信任来自 entry 的任意 target。

## 配置裁剪

一个 node 收到的配置只包含它参与的数据面对象：

- direct tunnel entry node：收到对应 tunnel 和 forwards。
- chain entry node：收到对应 forwards、当前 stage、下一 stage 拨号信息。
- chain middle node：收到自己的 stage listener、下一 stage 拨号信息、关联 forward 最小元数据。
- chain exit node：收到自己的 stage listener、关联 forwards 和 target。

证书和 secret 裁剪：

- stage listener node 收到 server cert/key 和 listener secret。
- dialing node 收到 CA cert、必要的 client cert/key，以及下一 stage public/connect addr。
- `mtls` 只给需要拨号的上一段节点下发 client cert/key。
- 不相关 tunnel/forward 不下发。

## Relay 协议

`RelayHello` 只传递转发身份和 stage 身份，不传递授权 target。exit 必须通过 `ForwardID` 在签名配置里查 target。

```go
type RelayHello struct {
    Magic          string `json:"magic"`
    Version        int    `json:"version"`
    TunnelID       string `json:"tunnel_id"`
    ForwardID      string `json:"forward_id"`
    FromStageIndex int    `json:"from_stage_index"`
    ToStageIndex   int    `json:"to_stage_index"`
    Network        string `json:"network"`
    Secret         string `json:"secret,omitempty"`
}
```

Stage index 语义：

- `FromStageIndex` 是拨出方所在 stage。
- `ToStageIndex` 是本次连接要进入的 stage。
- stage listener 必须校验 `ToStageIndex == local stage index`。
- stage listener 必须校验 `FromStageIndex == local stage index - 1`。
- stage listener 必须校验 `TunnelID`、`ForwardID`、`Network`、`Secret` 均匹配签名配置。
- `RelayHello` 不允许出现 target 字段；实现和测试都要覆盖这一点。

实现上 `ReadHello` 应拒绝未知字段，或显式检查原始 JSON 中不存在 `target`、`target_host`、`target_port` 等字段。不能只依赖 Go JSON unmarshal 的默认忽略行为。

TCP chain 流程：

```text
entry forward listener
  -> dial next tunnel stage
  -> write RelayHello{tunnel_id, forward_id, from_stage_index=0, to_stage_index=1}
  -> middle stage listener validates signed config and secret
  -> dial next tunnel stage
  -> exit stage listener validates signed config and secret
  -> exit resolves target from ForwardRuntime
  -> dial target
```

Direct tunnel 流程：

```text
entry forward listener -> target
```

## TCP/UDP 行为

Forward protocol modes:

- `["tcp"]`：只启动 TCP listener。
- `["udp"]`：只启动 UDP listener。
- `["tcp","udp"]`：同一个 `listen` 地址上同时启动 TCP 和 UDP listener。

TCP:

- direct tunnel: entry listener 直接 TCP dial target。
- chain tunnel: entry/middle/exit 通过 tunnel stream 串接。

UDP:

- direct tunnel: entry UDP listener 维护 client session，直接 UDP dial target。
- chain tunnel: 单路径 UDP relay，datagram 通过 tunnel stream 封装转发。
- UDP session key: `forward_id + client_addr`。
- UDP session idle timeout: 默认 60 秒。
- 多候选 UDP、跨候选 session affinity 放到 Plan 002。
- `ws-tls` 是 stream transport；UDP over ws-tls 也是 datagram-over-stream，不是原生 WebSocket datagram，也不在 stage 上监听 UDP。

UDP over stream framing:

```go
type UDPDatagramFrame struct {
    ForwardID string `json:"forward_id"`
    SessionID string `json:"session_id"`
    Payload   []byte `json:"payload"`
}
```

实现时可以使用二进制长度前缀，JSON 结构只是说明字段语义。不要为每个 UDP packet 重新建立一条 TCP/TLS/ws-tls tunnel connection；应按 UDP session 复用上游 tunnel stream 或维护短期连接池。

UDP chain session 行为：

- entry 为每个 client addr 建立或复用一个 upstream stream，先发送 `RelayHello{network="udp"}`。
- exit 为每个 `SessionID` 建立或复用到 target 的 UDP socket。
- response frame 使用相同 `SessionID` 反向回到 entry。
- middle stage 不解析 UDP payload，只按 stream pipe。

## 端口分配和冲突

端口占用必须区分用途和协议：

- forward TCP listener 占用 entry node 的 TCP port。
- forward UDP listener 占用 entry node 的 UDP port。
- 同一个 forward 可以同时占用同一数字端口的 TCP 和 UDP。
- 不同 forward 不能占用同一 node 上相同协议的同一端口。
- chain stage listener 占用 middle/exit node 的内部 tunnel TCP port。
- direct tunnel 不分配内部 tunnel port。
- 所有端口占用都写入 `port_allocations`，用 `(node_id, protocol, port)` 唯一约束兜底。

自动分配：

- forward `listen` 为空时，从 entry node `port_min..port_max` 找空闲端口。
- TCP+UDP forward 自动分配时，必须找到 TCP 和 UDP 都可用的同一个端口。
- chain stage 内部端口从对应 stage node 的端口范围分配。
- `public_addr` 默认用 `node.public_host:port`，如果缺少 `public_host` 且不能从 system IP 推导，应拒绝创建 chain tunnel。
- 自定义 forward listen port 也必须落在 entry node `port_min..port_max` 内。
- 自定义 chain stage listen port 也必须落在对应 stage node `port_min..port_max` 内。
- IPv6 地址生成 public endpoint 时必须使用 bracket hostport 形式。

## Controller 行为

创建 Tunnel：

1. 校验节点存在、未 revoked。
2. 校验 type、transport、stage 顺序。
3. 校验 direct/chain 规则和节点不重复。
4. 给 middle/exit stage 分配内部监听端口，并写入 `port_allocations`。
5. 根据 transport 生成 secret、TLS/mTLS 证书材料。
6. 写入 tunnel/stage/stage_nodes。
7. bump revision 并 push config。

创建 Forward：

1. 校验 tunnel 存在且 enabled。
2. 校验 protocols 至少包含 `tcp` 或 `udp`。
3. 如果 listen 为空，从 entry node 端口范围分配未占用端口。
4. 校验 listen 不与 tunnel 内部端口或其他 forward 的同协议监听冲突，并写入 `port_allocations`。
5. 允许同一个 forward 使用同一端口同时监听 TCP 和 UDP。
6. 写入 forwards。
7. bump revision 并 push config。

更新 Tunnel：

1. 尽量保留未变 stage 的端口、secret 和证书材料。
2. 为新增 stage 分配内部端口和证书材料。
3. 删除不再使用的 stage runtime 和对应 `port_allocations`。
4. 若路径改变，所有引用该 tunnel 的 forwards 自动使用新路径。
5. bump revision 并 push config。

更新 Forward：

1. 更新 listen、target、protocols、enabled。
2. 校验端口冲突。
3. 重建该 forward 的 `port_allocations`，保留仍然相同的占用。
4. bump revision 并 push config。

删除 Forward：

1. 删除 forward 记录。
2. 删除对应 `port_allocations`。
3. node runtime 收到新配置后关闭对应 TCP/UDP listener 和 UDP sessions。
4. bump revision 并 push config。

删除 Tunnel：

1. 如果仍有 forwards 引用，默认拒绝删除。
2. 可提供 `force=true` 级联删除相关 forwards。
3. 删除对应 stage 和 forward 的 `port_allocations`。
4. node runtime 收到新配置后关闭 tunnel stage listener。
5. bump revision 并 push config。

所有 create/update/delete 必须做到：

- 失败不 bump revision。
- 成功写库、端口分配和 revision bump 在同一事务或同一写锁保护下完成。
- API 响应默认脱敏 secret 和私钥材料。

## Node Apply 语义

node apply 必须幂等：

- 同 revision 重复下发不应重启 listener。
- 新配置删除对象后，应关闭旧 listener 和相关 session。
- 新配置新增对象后，应启动新 listener。
- 如果新配置应用失败，尽量保留上一份已成功 runtime，不要先全量关闭再失败退出。
- 本地缓存最后一份验签成功配置，controller 不可用时继续运行。

第一版可以先用“计算 desired set -> close stale -> start missing/update changed”的方式实现，避免大规模重构事务化 apply。

listener key 建议：

- forward TCP listener：`forward:<id>:tcp:<listen>`。
- forward UDP listener：`forward:<id>:udp:<listen>`。
- tunnel stage listener：`tunnel:<id>:stage:<index>:<listen_addr>:<transport>`。

handler 不应永久闭包捕获旧的 target 或下一跳信息。已有 listener 如果 bind 地址没变，但 target、enabled、secret 或下一跳信息变了，应更新 runtime state map；TLS 证书、transport、listen addr 这类影响 listener 本身的字段变化时才需要重启 listener。

## API

保留：

- `GET /api/nodes`
- `GET /api/nodes/{id}`
- `POST /api/nodes`
- `PATCH /api/nodes/{id}`
- `POST /api/nodes/revoke`

新增：

- `GET /api/tunnels`
- `GET /api/tunnels/{id}`
- `POST /api/tunnels`
- `PATCH /api/tunnels/{id}`
- `DELETE /api/tunnels/{id}`
- `POST /api/tunnels/{id}/enable`
- `POST /api/tunnels/{id}/disable`
- `GET /api/forwards`
- `GET /api/forwards/{id}`
- `POST /api/forwards`
- `PATCH /api/forwards/{id}`
- `DELETE /api/forwards/{id}`
- `POST /api/forwards/{id}/pause`
- `POST /api/forwards/{id}/resume`

移除或隐藏：

- `/api/links`
- `/api/routes`

## 前端

主导航改成：

- Dashboard
- Nodes
- Tunnels
- Forwards
- Traffic
- Audit
- Settings

删除或隐藏：

- Links 页面
- Routes 页面

Tunnel 表单：

- 名称
- 类型：direct / chain
- 传输：direct / tls / mtls / ws-tls
- 入口节点
- 中间节点列表
- 出口节点
- 启用

Forward 表单：

- 名称
- 协议：TCP / UDP / TCP+UDP
- 隧道
- 监听地址，留空自动分配
- 目标地址
- 启用

详情页展示：

- tunnel 路径图。
- forward 的入口地址。
- 自动分配的端口。
- 当前 revision 和下发状态。

Dashboard 改为展示：

- nodes
- tunnels
- forwards
- active forwards
- traffic
- recent errors

## Metrics

统计维度改成 forward/tunnel 为主：

```go
type MetricsReport struct {
    NodeID       string        `json:"node_id"`
    ObservedAt   time.Time     `json:"observed_at"`
    ForwardStats []TrafficStat `json:"forward_stats,omitempty"`
    TunnelStats  []TrafficStat `json:"tunnel_stats,omitempty"`
    Runtime      RuntimeStat   `json:"runtime"`
    AgentErrors  []AgentError  `json:"agent_errors,omitempty"`
}
```

推荐 stat ID：

- `forward:<forward_id>`
- `tunnel:<tunnel_id>:stage:<stage_index>`
- `target:<forward_id>`

## 测试

Go unit:

- tunnel type/transport/stage 校验。
- tunnel 内部端口分配。
- forward 入口端口分配。
- forward TCP+UDP 同端口冲突校验。
- port allocation 唯一约束和并发写入保护。
- target/listen/public_addr host:port 校验。
- config scoping 不泄露无关 secret/cert/client key。
- chain middle 不下发 target。
- exit 不信任 RelayHello target，只从签名 config 查 target。
- RelayHello from/to stage index 校验。
- API 响应脱敏 secret 和私钥材料。
- direct tunnel 编译。
- chain tunnel 编译。
- apply stale listener cleanup。

Go integration:

- direct TCP forward。
- direct UDP forward。
- direct TCP+UDP forward，同端口同时工作。
- chain TCP forward。
- chain UDP forward，单路径 session 工作。
- controller 重启后 node 使用缓存配置继续运行。
- 禁用 tunnel 后相关 forward 停止监听。
- 删除 forward 后 listener 关闭。
- 更新 target 后无需重启 controller 即可生效。

前端：

- node 创建。
- tunnel 创建和编辑。
- forward 创建和编辑。
- 自动端口展示。
- TCP/UDP/TCP+UDP 协议切换。

## 风险

- 这是数据模型和 runtime config 的直接重构，比兼容层改动大。
- node relay 主路径会变，需要集成测试兜住。
- TLS/mTLS 证书材料下发必须严格按节点裁剪。
- UDP chain 需要 datagram framing 和 session 生命周期管理。
- `RelayHello` 不能携带可被 exit 信任的 target，否则 entry 节点可绕过签名配置让 exit 拨任意目标。

## 交付顺序

1. 修改 shared model，定义 tunnel/forward runtime config 和协议枚举。
2. 修改 shared protocol，定义 RelayHello from/to stage index 和 UDP frame。
3. 重建 store schema，新增 tunnel/forward/port allocation 表。
4. validation 和 controller 端口分配事务。
5. controller API：tunnels。
6. controller API：forwards。
7. config compiler：按 node scope 输出 runtime config。
8. node runtime apply desired set，支持关闭 stale listeners。
9. node relay runtime：direct TCP。
10. node relay runtime：direct UDP 和 TCP+UDP 同端口。
11. node relay runtime：chain TCP。
12. node relay runtime：chain UDP 单路径 session。
13. metrics 改成 forward/tunnel 维度。
14. 前端 Nodes/Tunnels/Forwards 页面。
15. 集成测试和文档更新。
