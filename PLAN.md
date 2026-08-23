# NyaRelay 多跳中转面板 MVP 计划

## Summary

从空仓库创建 Go + React 单仓库。系统由两个运行物组成：

- `controller`：面板后端 + API + SQLite + 前端静态文件，Docker 部署，放在 Caddy 后面。
- `node`：每台中转 VPS 上运行的单服务，内部包含 agent、relay、链路管理和流量统计。

第一版做正式可用的管理面板，支持 TCP/UDP 透明转发、单节点直入直出、多跳链路、直连链路、TLS/mTLS 链路、WebSocket over TLS 链路、节点主动连接、流量统计、审计日志、节点吊销、配置签名和 Docker/Caddy 部署。

## Key Changes

### 项目结构

```text
cmd/
  controller/
  node/

internal/
  controller/
    api/
    auth/
    nodehub/
    routes/
    links/
    metrics/
    audit/
    store/
  node/
    agent/
    relay/
    link/
    supervisor/
    metrics/
  shared/
    model/
    protocol/
    crypto/
    validate/
    logging/

web/
  app/

deploy/
  docker/
  caddy/
  systemd/

docs/
```

后端使用 Go、SQLite、`net/http`、WebSocket、Ed25519 配置签名。前端使用 React + TypeScript + Vite + TanStack Router + TanStack Query，按正式后台项目组织。

### 控制面

controller Docker 运行，默认监听容器内 `8080`，由 Caddy 暴露 HTTPS。

节点不暴露管理端口，不接受 SSH，不支持远程 shell。`nyarelay node` 主动连接 controller：

```text
node -> Caddy HTTPS -> controller WebSocket
```

控制面能力：

- 节点注册、吊销、心跳、版本、系统信息。
- 节点主动拉取签名配置。
- controller 只通知“配置版本变化”，不 SSH 进节点。
- node 验证 Ed25519 签名后应用配置。
- 节点主动上报流量、连接数、链路状态和错误事件。
- controller 重启不影响已有转发，node 使用最后一次有效配置继续运行；当前版本的签名配置离线租约为 30 天。

### 数据面与链路类型

第一版支持三种路由形态：

```text
single-node   单节点直入直出：client -> entry/exit node -> target
multi-hop     多跳转发：client -> entry node -> hop nodes -> target
direct-link   节点间不用隧道，直接转发到下一跳或目标
```

第一版支持四种节点间 link type：

```text
direct      裸 TCP/UDP 转发，不加隧道，适合同内网或可信线路。
tls         节点间 TLS 加密，单向服务端证书校验。
mtls        节点间 mTLS，双向证书校验，默认推荐。
ws-tls      WebSocket over HTTPS，可走 Caddy 路由，适合国内 -> 海外跨境链路。
```

UI 展示为：

```text
直连
TLS
mTLS
WebSocket TLS
```

路由是透明四层转发，不解析 SS、Trojan、VLESS、HTTP、数据库协议。VLESS/SS/Trojan 都可以作为 payload 被转发：

```text
VLESS client -> 三网海外节点 -> 落地鸡 VLESS server
SS client    -> 国内入口节点 -> ws-tls/mtls -> 海外节点 -> SS server
```

UDP 是固定端口转发，覆盖 DNS、QUIC、游戏、Shadowsocks UDP 等场景；不做 ICMP、广播、多播、任意 IP 协议或 TUN 全局 VPN。

### 路由模型

单节点直入直出：

```yaml
route:
  name: premium-vless-entry
  protocol: tcp
  entry_node: premium-oversea-1
  listen: 0.0.0.0:443
  hops: []
  target: landing-vps.example.com:443
```

单跳跨境：

```yaml
route:
  name: cn-hk-ss
  protocol: tcp
  entry_node: cn-1
  listen: 0.0.0.0:8388
  hops:
    - link: cn-1-to-hk-1
  target: 127.0.0.1:8388
```

多跳：

```yaml
route:
  name: cn-hk-us-web
  protocol: tcp
  entry_node: cn-1
  listen: 0.0.0.0:8443
  hops:
    - link: cn-1-to-hk-1
    - link: hk-1-to-us-1
  target: 10.66.3.8:443
```

`hops: []` 表示入口节点直接连接 target，不需要任何节点间隧道。

### 隧道扩展

第一版内置 `direct`、`tls`、`mtls`、`ws-tls`，先不强依赖 sing-box。

数据库、API、UI 和 node supervisor 预留：

```text
managed-singbox
```

后续可接入成熟协议：

- VLESS over WebSocket + TLS：贴近 `ws -> http -> tls` 的常见跨境用法，可走 Caddy。
- Hysteria2：适合 UDP/443 和弱网，但通常不能只靠普通 HTTP 反代。
- TUIC：作为高级 QUIC/UDP 方案预留。
- Shadowsocks/Trojan：作为被托管的代理核心或透明目标服务均可。

### 前端面板

前端做多路由后台：

```text
/login
/setup
/dashboard
/nodes
/nodes/:id
/routes
/routes/new
/routes/:id
/links
/links/new
/traffic
/audit
/settings/security
/settings/controller
```

页面能力：

- Dashboard：节点在线、活跃路由、今日流量、异常事件。
- Nodes：注册、吊销、标签、版本、延迟、最近心跳。
- Routes：TCP/UDP 路由列表、启停、入口节点、可选多跳链路、目标地址。
- Links：节点间链路，选择 `direct`、`tls`、`mtls`、`ws-tls`。
- Traffic：按 route/node/link 查看流量。
- Audit：登录、配置变更、节点注册、吊销、失败连接。
- Settings：管理员密码、TOTP、随机 base path、Caddy 部署提示。

## Security Defaults

- controller Docker 不挂宿主 Docker socket。
- controller 不保存 SSH root 密钥。
- node 不支持任意命令、任意脚本或远程 shell。
- 所有节点配置必须验签。
- 每台节点独立 keypair，可单独吊销。
- 默认推荐节点间使用 `mtls`，跨境 HTTPS 场景使用 `ws-tls`。
- 单节点直入直出不需要 link，但仍必须来自 controller 签名配置。
- 登录支持密码哈希、TOTP、登录限速、会话过期。
- API 与 UI 默认同源，通过 Caddy HTTPS 暴露。
- 审计日志记录所有配置变更。
- 面板 base path 可随机化，但只作为附加层。

## Test Plan

- Go 单元测试：配置验签、路由校验、节点吊销、权限校验、流量计数。
- Go 集成测试：启动 controller + 本地 node，验证单节点直入直出 TCP 转发。
- Go 集成测试：启动 controller + 3 个本地 node，验证 TCP 多跳转发。
- UDP 集成测试：固定 UDP echo 转发，验证收发与统计。
- Link 测试：分别验证 `direct`、`tls`、`mtls`、`ws-tls`。
- 协议透明测试：用 TCP/UDP echo 模拟 VLESS/SS payload，不解析协议内容。
- 断线恢复测试：controller 重启后 node 自动重连，旧配置继续运行。
- 安全测试：伪造配置被拒绝，被吊销节点无法连接，非白名单 route 无法打开监听。
- 前端测试：路由创建、节点详情、链路编辑、登录/TOTP、空状态和错误状态。
- Docker 验证：`docker compose up` 后可完成初始化、登录、添加节点和创建 route。

## Assumptions

- 第一版部署目标是 Linux VPS，controller 用 Docker，node 用 systemd 单二进制。
- 第一版做端口级 TCP/UDP 转发，不做 TUN 全局 VPN。
- VLESS、SS、Trojan 等协议第一版作为透明 payload 转发，不在 NyaRelay 内部解析。
- 跨 GFW 第一版优先实现 `ws-tls`，后续用 `managed-singbox` 扩展成熟协议核心。
- `hops: []` 明确表示单节点直入直出。
