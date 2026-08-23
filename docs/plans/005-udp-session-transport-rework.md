# Plan 005: UDP 会话化转发（直连与隧道统一）

## 状态

本文档是实现前的技术方案。当前阶段只记录问题、设计和验收标准，不包含业务代码、配置模型或数据库修改。

## 结论摘要

当前实现的 UDP session affinity 只保存了“下一跳候选/最终目标的索引”，没有保存真正可复用的 UDP socket 或隧道 stream。结果是：

- 直连 UDP：每个数据报重新 `DialUDP`，发送一次，等待一个响应，然后关闭 socket。
- 链式 UDP：每个数据报重新建立下一跳 TCP/TLS/WS stream，重新发送 `RelayHello`，发送一个 frame，等待一个响应，再关闭 stream。
- 中间节点当前只处理一个 frame；出口节点虽然有循环，但上游每次都会关闭连接，所以实际上仍然是一报一连接。
- 返回数据只能作为当前数据报的同步响应处理，无法正确承载无响应、延迟响应、多响应或主动下行的 UDP 协议。

因此，WS 隧道确实会影响 UDP，但问题不是“UDP 包在 IP 层被简单套进 TCP 包”。实际是应用层把 UDP datagram 编码成 frame，再通过 TLS/TCP stream 传输：

```text
客户端 UDP
  -> 腾讯云入口 UDP listener
  -> UDPDatagramFrame（长度前缀 + JSON）
  -> TLS/TCP 长连接（当前代码使用 nyarelay Upgrade 的 raw stream）
  -> Sharon 解码 frame
  -> Sharon 的 UDP socket 发往目标
```

直入直出绕过了中间的 frame 和 TCP stream，但当前直连路径仍然存在“每个数据报新建 UDP socket、同步等待一个响应”的问题。因此两条路径应统一改成“一个 UDP 会话对应一个持续的传输资源”。

这项改造可以修复当前的会话生命周期和返回包语义，但不能消除 WS/TCP 的固有队头阻塞。若底层 TCP 丢包，后续 frame 仍可能等待前一个 TCP segment；要彻底消除该限制，需要 UDP、QUIC 或其他 datagram transport，不属于本计划的第一阶段。

## 1. 现状与代码证据

### 1.1 入口 UDP listener

`internal/node/relay/udp.go` 的 `udpLoop` 从入口 UDP socket 读取数据报，并为每个数据报启动处理 goroutine。`handleUDPPacket` 使用：

- direct tunnel：`udpTargetRoundTrip`；
- chain tunnel：`forwardUDPOverTunnel`。

入口的 `clientAddr` 目前被拼进 `SessionID`，但没有对应的活动会话对象。每个数据报都独立进入拨号和等待响应流程。

### 1.2 链式 UDP 的 stream 生命周期

链式路径当前调用关系大致如下：

```text
handleUDPPacket
  -> forwardUDPOverTunnel
    -> forwardUDPFrameWithRetry
      -> dialForwardNext / dialForwardNextUDP
        -> dialStageCandidate
          -> dialStage
          -> WriteHello(network=udp)
      -> writeReadUDPFrame
          -> WriteUDPDatagramFrame
          -> ReadUDPDatagramFrame
      -> Close(stream)
```

这意味着：

1. `dialForwardNextUDP` 中的 `udp` 表只记住候选节点，不记住已经建立的 stream。
2. 同一 UDP client 的第二个数据报仍会重新 TCP connect、TLS handshake 或 WS-style upgrade。
3. 同一 UDP client 的第二个数据报仍会重新发送 `RelayHello`。
4. `forwardUDPFrameWithRetry` 每次只写一个 frame 并读取一个响应，然后关闭 stream。

WS transport 的实现位于 `internal/node/relay/service.go`：先建立 TCP，完成 TLS，再发送：

```text
GET / HTTP/1.1
Connection: Upgrade
Upgrade: nyarelay
```

服务端 hijack HTTP connection 后直接处理 `net.Conn`。这里的 `ws-tls` 是项目自己的 WebSocket-style stream，不是 WebSocket datagram，也不是内核层 UDP over TCP 封装。

### 1.3 中间节点与出口节点

中间节点的 `handleUDPStageTransit` 当前只读取一个 `UDPDatagramFrame`，调用一次下一跳转发，然后把一个 response frame 写回上游。函数返回后，上游连接被关闭。

出口节点的 `handleUDPStageExit` 有读取循环，但 `udpTargetRoundTripOnce` 会：

1. `dialForwardTarget(..., "udp", ...)`；
2. 新建一个 UDP socket；
3. 写入一个 payload；
4. 阻塞读取一个 response；
5. 关闭 socket。

因此出口的循环不会带来真实的目标 socket 复用。

### 1.4 直入直出 UDP

direct tunnel 最终也会进入 `dialForwardTarget`，再由 `dialRelayContext` 调用 `net.Dialer.DialContext(ctx, "udp", address)`。当前每一个入口数据报都会重新选择或复用一个“目标索引”，但不会复用上一次的 `*net.UDPConn`。

因此直连路径仍可能出现：

- 每个包的源端口变化；
- 目标侧看到很多短暂 UDP flow；
- 延迟返回包到达时，原 socket 已关闭；
- 目标不回应时，入口 goroutine 等待超时并丢弃本次数据报；
- 同一个 UDP 协议的多个 datagram 被不同 goroutine 并发处理，完成顺序可能改变。

### 1.5 frame 大小的次要问题

`internal/shared/protocol/udp.go` 当前使用 `[]byte` 的 JSON 编码。Go JSON 会把 `[]byte` 编码成 base64，所以 frame 大小大约是：

```text
4 字节长度前缀 + JSON 字段 + base64(payload)
```

当前 `MaxUDPPacket = 64 * 1024`，但真实 IPv4 UDP payload 上限通常是 65507 字节；同时当前 frame 上限只有 `MaxUDPPacket + 4096`。一个接近最大长度的合法 UDP payload 在 base64 后会超过当前 frame 上限，可能被错误拒绝。这个问题通常不会影响普通 DNS、QUIC 或 SS 数据报，但应随会话化一起修正。

## 2. 问题分类

### 已由代码直接确认的问题

| 问题 | 影响 |
| --- | --- |
| affinity 只缓存候选索引，不缓存 socket/stream | UDP 会话没有真正的路径和源端口稳定性 |
| 每个 frame 都重新建 tunnel stream | WS/TLS 握手和 TCP 连接开销大，延迟高 |
| 每个 frame 都重新发送 Hello | stream 无法作为 session 使用 |
| 每个数据报同步等待一个 response | 单向 UDP、延迟 response、多 response 不成立 |
| 中间节点每条 stream 只处理一个 frame | 链路无法持续承载一个 UDP 会话 |
| 每个目标数据报重新建 UDP socket | 返回包可能在 socket 关闭后丢失，源端口变化 |
| 每个数据报并发处理 | 同一 session 的顺序和 frame writer 安全性无法保证 |
| 最大 payload 与 JSON/base64 frame 限制不一致 | 大 UDP 包可能被协议层拒绝 |

### 可能放大问题、但不应与上述代码缺陷混为一谈的因素

| 因素 | 判断方法 |
| --- | --- |
| 腾讯云到 Sharon 的隧道端口只放通 UDP、未放通 TCP | WS 路径实际需要 TCP/HTTPS 到 Sharon；这会导致隧道根本建不起来 |
| Sharon 到 SS/Xray 目标的 UDP 出站或回程受限 | 在 Sharon 抓包确认是否有目标侧 UDP 和返回包 |
| 反向代理不转发 `Upgrade: nyarelay` 或有过短 idle timeout | 检查代理的 Upgrade 规则、读写超时和长连接日志 |
| WS/TCP 队头阻塞 | 即使会话化修复后，TCP 丢包仍会阻塞同一 session 后续 frame |
| SS/Xray 对源端口、会话或延迟响应有依赖 | 会话化后观察 Sharon 到目标的源端口是否稳定，以及是否能收到延迟返回 |

## 3. 设计目标与非目标

### 目标

- direct UDP 和 chain UDP 使用同一套 UDP session 生命周期模型。
- 同一个 `forward + client address` 在 idle timeout 内复用一个活动 session。
- direct session 复用一个目标 UDP socket。
- chain session 每一跳复用一条 TCP/TLS/WS stream；Hello 只发送一次。
- 一个 session 可以发送任意多个 datagram，不要求每个 datagram 都有 response。
- response 可以延迟、重复、多个或主动到达，并写回原始 client address。
- candidate/target 仍按 UDP session 粘滞；活动资源与其选择结果绑定。
- stream/socket 失败时安全关闭 session，下一次数据报可以重新选择候选并建立新路径。
- 配置重载、节点停止、idle timeout 和资源上限都能关闭并回收活动 session。
- 保持现有 `Forward`、`Tunnel` 配置模型，不增加控制器数据结构依赖。

### 非目标

- 不在第一阶段把 WS-style stream 改造为 RFC 6455 WebSocket。
- 不在第一阶段实现真正的 UDP/QUIC tunnel transport。
- 不通过可靠重传把 UDP 变成有序可靠协议；relay 只负责转发，不对 UDP payload 做 ACK。
- 不在一个公共 TCP stream 上复用所有 client session；第一阶段按 session 使用独立 stream，限制 TCP 队头阻塞的影响范围并简化返回包路由。
- 不根据“远端没有 response”主动判定目标故障；UDP 黑洞没有可靠的本地失败信号。

## 4. 核心设计

### 4.1 一个 UDP session 对应一个活动传输资源

入口 session key：

```text
forward_id + canonical client UDP address
```

session 至少包含以下逻辑状态：

```text
key
session_id
client_addr
path candidate / target binding
state: connecting | active | closing | closed
last_activity
bounded outbound queue
one serialized writer
one response reader
close-once and error reason
```

`SessionID` 在整个 session 生命周期中保持不变。第一阶段可以继续使用当前 wire 字段，不必为了会话化立即改变 frame JSON 结构；关键改变是同一条 stream 上连续读写多个 frame。

候选 session table 不再只是独立的索引缓存，而应当由活动 UDP session 持有候选/目标绑定。若为了重连保留索引缓存，也必须在活动 session 关闭、超时、配置重载或明确失败时同步删除，不能把“缓存命中”误认为“已有 socket/stream”。

### 4.2 入口发送采用异步 session writer

入口 UDP listener 不应为每个数据报等待整个网络 round trip。建议流程：

```text
ReadFromUDP
  -> 查找 forward + client_addr session
  -> 不存在：创建 connecting session，异步建立传输资源
  -> 存在：把 payload 放入该 session 的有界队列
  -> session writer 按顺序发送
  -> session reader 收到 response 后 WriteToUDP(client_addr)
```

关键要求：

1. 同一个 key 只能有一个 connecting/active session，避免并发首包创建两条路径。
2. 建连期间的首批 payload 放入有界队列；不能无限制地为每个数据报启动拨号 goroutine。
3. 同一 stream 只能有一个 writer；不能让多个 UDP worker 并发写长度前缀和 JSON，否则可能交错破坏 frame。
4. 默认按入队顺序写出。UDP 的可靠性和重排序语义仍由上层协议承担，但 relay 不应主动制造额外乱序。
5. 队列满时按明确策略丢弃并记录 metric；不能阻塞整个入口 UDP listener。
6. response reader 独立运行，不等待某个具体 outbound datagram 的同步 response。

### 4.3 direct UDP session

direct session 建立时按现有 target selector 选择一个 UDP target，然后：

1. 创建一个 connected UDP socket。
2. 将该 socket 绑定到 session。
3. writer 将后续 payload 写入同一 socket。
4. reader 持续读取同一 socket 的所有 response，并写回入口 client address。
5. 在 idle timeout、socket error、配置重载或 session close 时关闭 socket。

目标切换规则：

- 首次建连失败可以按现有 UDP target strategy 尝试其他可用 target。
- 已建立 session 的 socket 写入失败或读取到明确 socket error 时，记录 target failure、删除 binding、关闭 session。
- 下一次 client datagram 触发新 session 和重新选 target。
- 不能因为某个 datagram 暂时没有 response 就切换 target，否则会重复发送或破坏 UDP session。
- 自动重发已写入 socket 的 payload 不是默认行为，因为无法判断写入错误发生前目标是否已经收到，重发可能造成重复。

这会使 Sharon 直连 SS/Xray 目标时保持稳定的源端口，也能接收延迟到达或多个 response。它不要求目标支持连接语义；`connect` 只用于固定 peer、简化读写和接收 ICMP 错误。

### 4.4 chain UDP session

#### Entry

entry 为每个 client session 建立一条到下一 stage 的 stream：

1. dial TCP/TLS/WS transport。
2. 发送一次 `RelayHello{network:"udp"}`。
3. writer 在该 stream 上连续写 `UDPDatagramFrame`。
4. reader 连续读取 response frame，并按 session 的 client address 写回。

现有 `forwardUDPFrameWithRetry` 的“拨号、写一个 frame、读一个 response、关闭”模式应从正常 UDP session 路径中移除，只保留为失败重连所需的底层建连逻辑，不能再作为每包 API。

#### Middle

middle 收到 entry 的 UDP Hello 后，将这条 inbound stream 视为一个 session stream。读取第一个 frame 得到 `SessionID` 后，只建立一次下一跳 stream，并发送一次下一跳 Hello。

之后 middle 做双向 frame relay：

```text
entry stream  --frames-->  middle  --frames-->  next stream
entry stream  <--frames--  middle  <--frames--  next stream
```

每个方向各自只有一个 reader 和一个 writer。middle 不应为每个 frame 重新选择 candidate 或建立下一跳连接。任一方向发生 EOF、frame 校验失败、write failure 或 context cancel 时，关闭两端并结束该 session；entry 的下一次数据报负责触发重连和重新选择。

middle 必须验证后续 frame 的 `ForwardID` 和 `SessionID` 与首 frame 一致，防止一个 stream 被错误复用给不同的 UDP session。

#### Exit

exit 收到 UDP Hello 和首个 frame 后：

1. 按 UDP target strategy 选择一个 target。
2. 建立一个目标 UDP socket，并在整个 stream 生命周期中复用。
3. stream reader 收到的每个 frame 写入目标 socket。
4. target reader 收到的每个 UDP response 编码成 frame，写回同一条 inbound stream。

exit 不等待每个目标 payload 的 response，也不因单个 payload 没有 response 而关闭 stream。所有 response 都使用原 `SessionID`，由于一个 stream 专属于一个 session，不需要在第一阶段增加全局 multiplex ID。

### 4.5 response 语义

UDP 转发必须允许以下情况：

- client 发出数据报，目标没有 response；
- response 在发送后较长时间才到达；
- 一个请求得到多个 response；
- 目标主动发送 response；
- response 到达顺序与请求顺序不同。

relay 的职责是把收到的 response 写回对应 client address，而不是把 UDP API 强行实现成 request/response RPC。一个 session 独占一个目标 socket和一条 tunnel stream，是在不引入额外 multiplex 协议的前提下保持返回路由的关键。

### 4.6 session idle 与连接保活

建议沿用现有候选 session 的默认值，并把它们提升为活动 session 的生命周期参数：

```text
udp_session_idle_timeout = 120s
udp_session_gc_interval  = 30s
```

`last_activity` 在以下事件更新：

- client payload 入队/写入；
- tunnel frame 读写；
- target UDP payload 写入；
- response 从 target 或 tunnel 读到。

GC 必须关闭实际 socket/stream，而不只是删除 map 条目。每条 persistent stream 都必须被 session manager 记录，`Service.stopRuntimeLocked`、配置 Apply 失败回滚和 context cancel 时显式关闭。

底层 TCP/TLS/WS stream 使用 TCP keepalive。由于当前 WS-style upgrade 在 hijack 后脱离 `http.Server` 的连接管理，不能依赖 HTTP server 的 idle timeout 来回收或保活。若部署的反向代理会关闭长时间无应用数据的 Upgrade connection，后续可以增加协商过的 ping/pong frame；第一阶段至少要：

- 设置 client/server 两侧 TCP keepalive；
- 给 session 设置明确 idle close；
- 记录 stream close 原因，区分 proxy idle close、对端 EOF 和本地 timeout。

### 4.7 candidate 和 target 的选择/故障转移

选择粒度保持现有设计：

- 新 UDP session 选择 candidate/target。
- 同一 session 后续 datagram 固定使用该 candidate/target。
- stream/socket 建立失败时可在当前建连阶段尝试下一个候选。
- 已建立资源的明确 I/O 失败会删除绑定，下一次数据报新建 session。
- 远端无响应不等于可检测的失败，不主动切换。

`udpCandidateSessions` 当前的职责需要重新定义：它可以作为短期“偏好候选”或重连提示，但活动 session 必须是唯一的资源所有者。推荐最终让 session manager 直接保存：

```text
candidate_node_id
candidate_index
target_id
active next-hop stream
active target UDP socket
```

避免 `s.udp`、`s.udpTargets` 与实际 session 各自维护一份可能过期的状态。

### 4.8 frame 大小和写入安全

第一阶段可以继续使用当前 4 字节 big-endian length prefix，避免引入新的 wire protocol。需要同时修正边界：

```text
MaxUDPDatagramPayload = 65507
MaxUDPFrameBytes      = 96 KiB（JSON/base64 格式的硬上限）
```

要求：

- 先校验 length prefix，再分配内存；
- 单独校验 `Payload <= MaxUDPDatagramPayload`；
- 单独校验 `ForwardID`、`SessionID` 长度；
- 写入长度和 body 时使用完整写入语义；
- 对异常 frame 关闭该 session，不让 reader 循环继续解析未知边界；
- 暂不在第一阶段把 JSON frame 改成二进制 payload。若后续 CPU/带宽成本明显，再单独设计协议版本和兼容迁移。

### 4.9 资源上限

持久化 session 会把“每包短连接”变成“每个活动 client 一个连接/一个 socket”，必须增加显式上限：

- 每个 node 的 active UDP session 上限；
- 每个 forward 的 active UDP session 上限；
- 每个 session 的 outbound queue 长度；
- 连接建连中的 session 上限；
- 每个 stream 的最大 frame size。

当前 `maxUDPSessionEntries = 100000` 只限制候选 map，并不会关闭真实资源，不能直接作为 active session 上限。达到上限时应优先拒绝/丢弃新 session，并记录原因；不能驱逐一个仍有数据流量的活动 session。idle session 才能被 GC。

现有 `maxUDPPacketWorkers` 只限制每包 goroutine。会话化后应把资源控制重点迁移到 active session、connecting session 和 per-session queue，同时保留一个轻量的入口读取保护，避免入口 listener 被慢建连拖住。

## 5. 失败与重连状态机

建议使用以下状态机，而不是每个数据报独立重试：

```text
new
  -> connecting
      -> active
      -> closed（dial/hello/target socket 失败）
active
  -> active（发送/接收多个 datagram）
  -> closing（idle、context、I/O error、配置重载）
  -> closed
closed
  -> 下一次 client datagram 创建新 session
```

细则：

1. 建连失败：当前 session 删除；在仍属于“首次建立路径”的阶段按现有 strategy 尝试备用 candidate/target。
2. Hello 或 frame 写失败：关闭当前 stream，记录 candidate failure，当前队列按明确策略丢弃或交给一次新的 session 建连；不无限重试。
3. response reader EOF：关闭 session，下一次 client datagram 重建；已经收到的 response 仍先写回 client。
4. target socket read error：关闭 session，删除 target binding；不把“无数据”当成错误。
5. idle timeout：关闭 socket 和 stream，删除 session；下一次数据报重新选择。
6. 配置更新/停止：停止接受新数据报，关闭所有活动 session，再关闭 listener；不能只清空索引 map。

当前数据报是否重放必须谨慎：只有在尚未向底层资源成功写出、且错误点明确发生在发送之前时才可考虑重放；默认不自动重放，以避免 SS、QUIC 等协议收到重复数据。

## 6. 观测性要求

现有的 `udp target round trip failed` 只适合旧的同步模型。会话化后应增加结构化日志或 metrics：

- `udp_session_created` / `udp_session_closed`；
- `udp_session_active`；
- `udp_session_connect_failed`；
- `udp_session_reconnect`；
- `udp_datagram_in` / `udp_datagram_out`；
- `udp_datagram_dropped_queue_full`；
- `udp_stream_read_error` / `udp_stream_write_error`；
- `udp_target_read_error`；
- `udp_session_idle_expired`。

日志字段至少包括 `forward`、`session_id`、stage/candidate 或 target、transport、close reason。不要记录 payload、secret 或完整认证信息。

需要把统计语义从“每个数据报都算一次 connection”调整为：

- connection/session count：session 建立次数；
- bytes/packets：每个 datagram 的进出字节数；
- tunnel candidate/target：归属活动 session 的实际路径。

## 7. 实现顺序

### Phase 1：协议边界和通用 session manager

- 抽象 direct UDP socket 和 tunnel stream 的共同 session 生命周期。
- 实现单 writer、response reader、idle close、close-once、bounded queue。
- 保持现有 frame wire format，先补多 frame、部分读写、最大 payload 测试。
- 增加活动 session close registry，并接入 runtime stop/apply。

### Phase 2：先替换 direct UDP

- 入口 direct UDP 使用 session manager。
- 一个 client session 复用一个 target UDP socket。
- 先覆盖无 response、延迟 response、多 response、源端口稳定和 idle 回收。
- 确认 direct 行为正确后，再共用同一抽象接入链路。

### Phase 3：替换 chain UDP

- entry：一个 client session 一条 next-stage stream，Hello 只发一次。
- middle：从单 frame handler 改为双向 persistent frame relay。
- exit：一个 inbound session stream 对应一个 target UDP socket，异步转发双向数据。
- 失败时关闭整条 session path，让下一次数据报重新建立，不在每个 frame 内重新 dial。

### Phase 4：候选选择、限流和可观测性收口

- 把 candidate/target binding 与活动 session 生命周期统一。
- 增加 active/connecting session 限制、队列限制和 metrics。
- 调整 connection counter、tunnel/target stat 和错误日志。
- 配置重载、过期、停止、候选失败和目标失败都验证实际资源已关闭。

### Phase 5：WS 长连接和部署验证

- 确认 TCP keepalive 在 client/server 两侧生效。
- 检查反向代理是否允许 `Upgrade: nyarelay`、长连接和足够的读写超时。
- 如代理必须使用标准 `Upgrade: websocket`，另立 transport compatibility 方案，不把它与 UDP session 修复混在同一个隐式变更中。
- 根据实测再决定是否增加协议层 ping/pong。

## 8. 测试与验收标准

### 单元测试

- 同一 session 连续写入多个 frame，reader 能逐个读取且没有边界串包。
- 多个并发 `send` 不会交错写坏长度前缀/body。
- partial read/partial write、EOF、timeout、context cancel 都能关闭 session。
- 最大合法 UDP payload 可以编码/解码；超过上限被拒绝。
- session idle 后实际 UDP socket 和 tunnel stream 都被关闭。
- queue 满时有界丢弃且不会阻塞 listener。

### direct UDP 集成测试

- 同一个 client 连续发送多个 datagram，目标看到同一个源端口。
- 目标不回复时，client 仍能继续发送后续 datagram，relay 不为每包等待 10 秒。
- 目标延迟回复时，socket 未关闭，延迟 response 能回到原 client。
- 目标对一个请求返回多个 response，多个 response 都能回到 client。
- 两个不同 client session 使用不同 UDP socket，互不串包。
- target failure 后下一次 datagram 能按策略重新选择 target。

### chain UDP 集成测试

- 同一个 client 的多个 datagram 只建立一条 entry-to-middle/exit stream，并只发送一次 Hello。
- 两节点和三节点链路都能连续发送多个 datagram。
- middle 不会每个 frame 重新 dial 下一跳。
- exit 到目标只使用一个 UDP socket，目标源端口在同一 session 内保持稳定。
- 无 response、延迟 response、多 response、主动 response 均能正确返回。
- entry、middle、exit 任一 stream 断开后，下一次 client datagram 能重建路径。
- 多 candidate 只在新 session 或失败重连时选择，不按 datagram 轮换。

### WS-TLS 测试

- `ws-tls` 链路连续承载同一 UDP session 的多个 frame。
- 同一个 session 只有一个 TLS/TCP/Upgrade connection，连接不会每包重新握手。
- Upgrade 被拒绝、代理关闭、idle close 时日志能区分握手失败和 session 中途断开。
- 验证 TCP keepalive 或后续 ping/pong 的行为。

### 真实环境验收

不要求通过被墙环境测试直连 SS，但可用本地 UDP fake target 或 Xray 测试端点验证相同 socket/session 语义。重点抓包位置：

```text
腾讯云入口：看到 client -> forward 的 UDP
腾讯云到 Sharon：WS 隧道看到一条或少量持续 TCP connection，不应看到 UDP payload 直接发往 Sharon
Sharon 到 SS/Xray：看到持续的 UDP source port 和对应回包
```

## 9. 部署检查清单

对于 `腾讯云 -> WS 隧道 -> Sharon -> SS/Xray`：

1. 腾讯云入口 forward 的 UDP listener 端口允许客户端访问。
2. 腾讯云节点到 Sharon 隧道监听地址/代理地址允许 TCP/HTTPS；仅放通 UDP 不足以建立 WS 隧道。
3. Sharon 的隧道监听或反向代理允许 `Upgrade: nyarelay` 并保持长连接。
4. Sharon 到最终 SS/Xray 地址允许 UDP 出站和回程。
5. 如果目标通过安全组按源地址限制，规则应允许 Sharon 的出口地址，而不是只允许腾讯云地址。
6. 代理的 idle timeout 大于 UDP session idle，或后续启用应用层 keepalive。

这份清单不能替代代码修复：即使所有端口都开放，当前“一报一 socket/stream + 同步等待 response”的实现仍会产生上述会话问题。

## 10. 风险与取舍

- 每个活动 UDP client session 会占用一个目标 UDP socket；链式场景还会占用每一跳一个 TCP/TLS/WS stream。必须设置活动 session 上限和 idle timeout。
- WS/TCP 的队头阻塞仍然存在，但按 session 分 stream 后不会把所有 client session 复用在同一条全局 TCP stream 上。
- UDP 没有可靠的远端失败反馈；无法仅凭“没有 response”安全地自动 failover。
- stream 断开后的自动重发可能造成重复包，因此默认只重建路径，不重放已经可能写出的 payload。
- 保持 JSON/base64 frame 可以降低首阶段协议迁移风险，但会增加带宽和 CPU；二进制 frame 应单独做兼容性设计。
- 当前自定义 `Upgrade: nyarelay` 不是标准 RFC 6455 WebSocket。如果生产代理只支持标准 WebSocket，需要独立处理 transport 兼容性；不能用“UDP 改成 session”掩盖 Upgrade 层失败。

## 最终验收条件

只有同时满足以下条件，才认为本计划完成：

1. direct UDP 同一 client session 复用同一个目标 UDP socket。
2. chain UDP 同一 client session 复用每一跳 tunnel stream，Hello 只发送一次。
3. 多个 datagram 不再同步等待单个 response，延迟/多响应/无响应场景行为正确。
4. stream/socket 失败、idle、配置重载和停止都会实际回收资源。
5. candidate/target 只按 session 选择，不按 datagram 轮换。
6. WS 路径的 TCP 依赖、Sharon 的 UDP 出口和反向代理 Upgrade 行为都有可观测验证。
7. 相关单元测试、direct/chain/WS 集成测试和 `go test ./...` 全部通过。
