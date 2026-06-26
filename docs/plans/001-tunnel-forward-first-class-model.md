# Plan 001: Tunnel + Forward 一等模型

## 目标

开发期可以重建配置数据，因此第一阶段不再做 `Tunnel/Forward -> generated Link/Route` 的兼容层，而是直接把 NyaRelay 改成和 flvx 更接近的产品形态：

- `Node`：承载流量的节点。
- `Tunnel`：可复用的转发路径，描述入口、中间节点、出口节点和节点间传输方式。
- `Forward`：具体转发规则，选择一个 tunnel，设置入口端口和目标地址。

保留 NyaRelay 最初的安全边界：

- controller 不保存 SSH 密钥。
- controller 不主动 SSH 到节点。
- controller 不下发任意 shell 命令。
- node 主动连接 controller，拉取签名后的结构化配置。
- node 本地运行 relay runtime，按签名配置启动/停止监听和转发。

## 设计取向

这一阶段允许破坏旧数据模型：

- 可以废弃现有 `links` 表和 `routes` 表。
- 可以删除或隐藏 `/links`、`/routes` 页面。
- 可以改 `RelayConfig` 的结构。
- 可以要求开发环境清空旧 SQLite 后重建。

不再把 `Link` 暴露给用户。节点间链路端口、secret、证书等都变成 tunnel runtime 的内部细节，由 controller 自动分配和下发。

## 非目标

- 不做 SSH、远程 shell、nftables 主动操作。
- 不做跨面板 federation。
- 不做纯 NAT 打洞或完全反向数据隧道。
- 不做成熟质量探测、best-exit、自动选路。
- 不要求第一版就完成每一跳多候选负载均衡；数据模型要为它预留，完整策略放到 Plan 002。

## 转发实现选择

第一阶段优先继续做自研 relay runtime，而不是 nftables：

- 自研 relay 更符合“节点只接收签名结构化配置”的边界。
- 不需要 SSH、root shell 或远程操作系统防火墙。
- TCP/TLS/mTLS/ws-tls 多跳更容易统一建模。
- 跨平台和测试更简单。

nftables 可以作为未来优化项，只用于单节点直入直出或高性能本机 DNAT 场景；不作为基础架构依赖。

## 新产品模型

### Node

保留现有节点能力：

- 节点注册、吊销、心跳。
- 节点主动拉取配置。
- `public_host`
- `port_min`
- `port_max`
- system info 和状态。

后续可以补充：

- IPv4/IPv6 偏好。
- 额外出口地址。
- 默认监听地址。

### Tunnel

Tunnel 是路径模板，不直接暴露用户入口端口。

第一阶段支持：

```text
direct tunnel:
  entry node -> target

chain tunnel:
  entry node -> optional middle nodes -> exit node -> target
```

数据模型预留 stage/candidate 结构，但 UI 第一版可以先限制为线性路径：

```text
entry: one node
middle: zero or more nodes, ordered
exit: one node
```

后续 Plan 002 再把每一层扩展为多个候选节点和策略。

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

同一个 forward 可以同时启用 TCP 和 UDP。实现上不是把 TCP/UDP 混进同一个 socket，而是在同一个入口地址上分别启动 TCP listener 和 UDP listener，共用同一个 tunnel、target、权限和统计维度。

如果 tunnel 是 direct，entry node 直接拨 target。  
如果 tunnel 是 chain，entry node 先拨 tunnel 的下一段，最终 exit node 拨 target。

## 数据库

开发期可以重建，因此 migration 可以简单：

- 新建目标表。
- 旧 `links`、`routes` 可以先保留不用，或者在开发环境清库。
- 不需要写复杂旧数据迁移。

建议表：

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
  protocols TEXT NOT NULL,
  listen TEXT NOT NULL,
  target TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

`role` 可选：

- `entry`
- `middle`
- `exit`

第一阶段规则：

- `direct` tunnel 只有 `entry` stage。
- `chain` tunnel 至少有 `entry` 和 `exit` stage。
- 第一阶段每个 stage 只有一个 node。
- `listen_addr/public_addr/connect_addr` 由 controller 自动生成或从节点信息推导。

## Runtime Config

替换当前以 `Links` 和 `Routes` 为中心的配置：

```go
type RelayConfig struct {
    Revision  int64             `json:"revision"`
    IssuedAt  time.Time         `json:"issued_at"`
    NodeID    string            `json:"node_id"`
    Nodes     []Node            `json:"nodes"`
    Tunnels   []TunnelRuntime   `json:"tunnels"`
    Forwards  []ForwardRuntime  `json:"forwards"`
    ExpiresAt time.Time         `json:"expires_at"`
}

type TunnelRuntime struct {
    ID        string                 `json:"id"`
    Name      string                 `json:"name"`
    Type      string                 `json:"type"`
    Transport string                 `json:"transport"`
    Stages    []TunnelRuntimeStage   `json:"stages"`
    Settings  map[string]string      `json:"settings,omitempty"`
}

type TunnelRuntimeStage struct {
    Index    int                      `json:"index"`
    Role     string                   `json:"role"`
    Strategy string                   `json:"strategy"`
    Nodes    []TunnelRuntimeNode      `json:"nodes"`
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
    ID       string        `json:"id"`
    Name     string        `json:"name"`
    TunnelID string        `json:"tunnel_id"`
    Protocols []RouteProtocol `json:"protocols"`
    Listen   string        `json:"listen"`
    Target   string        `json:"target"`
    Enabled  bool          `json:"enabled"`
}
```

Config scoping 原则：

- entry node 收到相关 forward listener 和 tunnel next-hop 信息。
- middle node 收到自己的 tunnel listener 和 next-hop 信息。
- exit node 收到自己的 tunnel listener 和 target dial 所需信息。
- TLS/mTLS 私钥、client cert、secret 只下发给对应节点。
- 不相关 tunnel/forward 不下发。

## Relay 协议

替换或扩展当前 `RelayHello`：

```go
type RelayHello struct {
    TunnelID   string `json:"tunnel_id"`
    ForwardID  string `json:"forward_id"`
    StageIndex int    `json:"stage_index"`
    Network    string `json:"network"`
    Target     string `json:"target"`
    Secret     string `json:"secret,omitempty"`
}
```

TCP 流程：

```text
entry forward listener
  -> dial next tunnel stage
  -> write RelayHello
  -> middle stage listener
  -> dial next tunnel stage
  -> exit stage listener
  -> dial target
```

Direct tunnel：

```text
entry forward listener -> target
```

TCP/UDP 同时启用：

- `protocols=["tcp"]` 时只启动 TCP listener。
- `protocols=["udp"]` 时只启动 UDP listener。
- `protocols=["tcp","udp"]` 时在同一个 `listen` 地址上同时启动 TCP 和 UDP listener。
- TCP 和 UDP 走各自的数据处理路径，但复用同一个 forward ID 和 tunnel ID。

UDP 第一版：

- direct tunnel 支持 UDP。
- chain tunnel 支持单路径 UDP relay。若 tunnel transport 是 TCP-based TLS/mTLS/ws-tls，需要以 UDP frame 形式封装转发。
- 多候选 UDP 和 session affinity 放到 Plan 002。

## Controller 行为

创建 Tunnel：

1. 校验节点存在、未 revoked。
2. 校验 stage 顺序。
3. 根据节点 `port_min/port_max` 给 middle/exit stage 分配内部监听端口。
4. 根据 transport 生成 secret、TLS/mTLS 证书材料。
5. 写入 tunnel/stage/stage_nodes。
6. bump revision 并 push config。

创建 Forward：

1. 校验 tunnel 存在且 enabled。
2. 如果 listen 为空，从 entry node 端口范围分配未占用端口。
3. 校验 protocols 至少包含 `tcp` 或 `udp`。
4. 校验 listen 不与 tunnel 内部端口或其他 forward 的同协议监听冲突。
5. 允许同一个 forward 使用同一端口同时监听 TCP 和 UDP。
6. 写入 forwards。
7. bump revision 并 push config。

更新 Tunnel：

1. 重新分配新增 stage 的内部端口。
2. 尽量保留未变 stage 的端口和证书材料。
3. 若路径改变，所有引用该 tunnel 的 forwards 自动使用新路径。
4. bump revision 并 push config。

更新 Forward：

1. 更新 listen、target、protocols、enabled。
2. 校验端口冲突。
3. bump revision 并 push config。

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
- `POST /api/tunnels/{id}/disable`
- `GET /api/forwards`
- `GET /api/forwards/{id}`
- `POST /api/forwards`
- `PATCH /api/forwards/{id}`
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

## 测试

Go unit:

- tunnel stage 校验。
- tunnel 内部端口分配。
- forward 入口端口分配。
- forward TCP+UDP 同端口冲突校验。
- config scoping 不泄露无关 secret/cert。
- direct tunnel 编译。
- chain tunnel 编译。

Go integration:

- direct TCP forward。
- direct UDP forward。
- direct TCP+UDP forward，同端口同时工作。
- chain TCP forward。
- chain UDP forward，单路径工作。
- controller 重启后 node 使用缓存配置继续运行。
- 禁用 tunnel 后相关 forward 停止监听。
- 更新 target 后无需重启 controller 即可生效。

前端：

- node 创建。
- tunnel 创建和编辑。
- forward 创建和编辑。
- 自动端口展示。

## 风险

- 这是数据模型和 runtime config 的直接重构，比兼容层改动大。
- node relay 主路径会变，需要集成测试兜住。
- TLS/mTLS 证书材料下发必须严格按节点裁剪。
- UDP chain 需要处理 datagram framing；多候选 UDP 还需要 session affinity，放到 Plan 002。

## 交付顺序

1. 修改 shared model，定义 tunnel/forward runtime config。
2. 重建 store schema，新增 tunnel/forward 表。
3. controller API：tunnels。
4. controller API：forwards。
5. config compiler：按 node scope 输出 runtime config。
6. node relay runtime：direct TCP。
7. node relay runtime：direct UDP 和 TCP+UDP 同端口。
8. node relay runtime：chain TCP。
9. node relay runtime：chain UDP 单路径。
10. metrics 改成 forward/tunnel 维度。
11. 前端 Nodes/Tunnels/Forwards 页面。
12. 集成测试和文档更新。
