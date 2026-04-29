# WG-Manager

基于 WireGuard 的管理层 — 星形拓扑 VPN，客户端一行命令即可加入。服务端运行 Go 守护进程提供 HTTP API，客户端自动获取配置并连接。

## 架构

```
                          ┌───────────────────────────────┐
                          │       服务器 (Linux)            │
                          │                                 │
                          │  ┌─────────────────────────┐   │
                          │  │  wg-mgmt-daemon (Go)     │   │
                          │  │  HTTP :58880              │   │
                          │  │                           │   │
                          │  │  GET /connect             │   │ ← 统一入口
                          │  │  POST /register           │   │
                          │  │  POST /request            │   │
                          │  │  GET /peers               │   │
                          │  │  GET /health              │   │
                          │  └───────────┬───────────────┘   │
                          │              │ wg set             │
                          │  ┌───────────┴───────────────┐   │
                          │  │    WireGuard (wg0)         │   │
                          │  │    10.0.0.1/24             │   │
                          │  └───────────────────────────┘   │
                          └──────┬─────────────┬─────────────┘
                                 │             │
                     wg 隧道      │             │ HTTP :58880
                                 │             │
               ┌─────────────────┴──┐  ┌───────┴────────────┐
               │  Linux / macOS /   │  │      Windows        │
               │  WSL               │  │                     │
               │                    │  │  curl → wg0.conf    │
               │  curl | sudo bash  │  │  iwr → .ps1         │
               └────────────────────┘  └─────────────────────┘
```

## 特性

- **一行命令加入** — 所有平台统一入口 `curl http://IP:58880/connect | sudo bash`
- **审批工作流** — 低信任用户提交申请，管理员通过 `wg-mgmt-tui` 批准
- **直连模式** — 高信任用户获得含 API Key 的 URL，直接加入无需审批
- **零中断** — 添加/删除 peer 使用 `wg set`，不重启 WireGuard 接口
- **全自动管理** — 密钥生成、IP 分配、配置持久化
- **TUI 管理面板** — 终端界面：查看 peer、批准请求、查看日志
- **审计日志** — 全部事件记录到 `/var/log/wg-mgmt/audit.log`，自动 logrotate
- **Go 守护进程** — 单文件静态编译，systemd 管理，异常自动重启

## 快速开始

### 1. 服务端初始化

```bash
git clone git@github.com:yequdesu/WG-manager.git /root/wg-manager
cd /root/wg-manager
sudo bash server/setup-server.sh
```

### 2. 开放防火墙端口

| 协议 | 端口 | 用途 |
|------|------|------|
| UDP | 51820 | WireGuard 隧道 |
| TCP | 58880 | 管理 API |

### 3. 客户端加入

**所有平台 — 审批模式（默认，无需 API Key）：**

| 平台 | 命令 |
|------|------|
| Linux / macOS / WSL | `curl -sSf http://IP:58880/connect \| sudo bash` |
| Windows PowerShell | `iwr http://IP:58880/connect -OutFile t.ps1; .\t.ps1` |
| 浏览器 | 打开 `http://IP:58880/connect` → HTML 分发页面 |

客户端提交申请 → 管理员在 `wg-mgmt-tui` 中批准 → 客户端自动完成配置并连接。

**直连模式（管理员分发，内含 API Key）：**

| 平台 | 命令 |
|------|------|
| Linux / macOS / WSL | `curl -sSf "http://IP:58880/connect?mode=direct&name=DEVICE" \| sudo bash` |
| Windows | `curl -o wg0.conf "http://IP:58880/connect?mode=direct&name=MYPC"` |

## 管理命令

```bash
wg-mgmt-tui                          # TUI 管理面板
tail -f /var/log/wg-mgmt/audit.log   # 审计日志
bash scripts/health-check.sh         # 健康检查
bash scripts/list-peers.sh           # 查看 peer（CLI）
```

**审批 / 拒绝请求：**
```bash
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H 'Authorization: Bearer <KEY>' | python3 -m json.tool

curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H 'Authorization: Bearer <KEY>'

curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H 'Authorization: Bearer <KEY>'
```

**删除 peer：**
```bash
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<名称> \
  -H 'Authorization: Bearer <KEY>'
```

## API 参考

基础路径：`http://IP:58880`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/connect` | 无 | 统一分发——根据 User-Agent 返回 bash/ps1/conf/HTML |
| `POST` | `/register` | API Key / localhost | 注册 peer，返回配置 |
| `POST` | `/request` | 限流 | 提交审批申请 |
| `GET` | `/request/{id}` | 无 | 轮询申请状态（pending/approved/rejected） |
| `GET` | `/requests` | API Key + localhost | 查看待审批列表 |
| `POST` | `/requests/{id}/approve` | API Key + localhost | 批准申请 |
| `DELETE` | `/requests/{id}` | API Key + localhost | 拒绝申请 |
| `GET` | `/peers` | API Key + localhost | 查看 peer 及在线状态 |
| `DELETE` | `/peers/{name}` | API Key + localhost | 删除 peer |
| `GET` | `/status` | API Key + localhost | 服务状态 |
| `GET` | `/health` | 无 | 存活检查 |

## 项目结构

```
wg-manager/
├── cmd/
│   ├── mgmt-daemon/main.go          # 守护进程入口
│   └── mgmt-tui/main.go             # TUI 入口
├── internal/
│   ├── api/                         # HTTP 处理器、中间件、路由
│   ├── audit/                       # 审计日志模块
│   ├── store/                       # Peer/Request 状态持久化
│   └── wg/                          # WireGuard CLI 操作封装
├── client/
│   ├── connect.sh                   # 直连脚本模板
│   ├── request-approval.sh          # 审批脚本模板
│   ├── request-approval.ps1         # Windows 审批脚本
│   ├── install-wireguard.sh         # 多系统 WG 安装器
│   └── lib/os-detect.sh            # 平台抽象层
├── server/
│   ├── setup-server.sh              # 一键初始化 / 升级
│   └── wg-mgmt.service              # systemd unit
├── scripts/
│   ├── build.sh / build.bat         # 交叉编译
│   ├── list-peers.sh / health-check.sh
├── config.env
└── Makefile
```

## 编译

```bash
make build          # Linux amd64 → bin/wg-mgmt-daemon
make build-tui      # TUI 二进制 → bin/wg-mgmt-tui
make build-all      # 两个二进制一起编译
make vet            # 运行 go vet
```

## 常见问题

| 现象 | 解决方案 |
|------|----------|
| Windows 无法 ping 通 | 防火墙放行 ICMP：`New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| 无法访问管理 API | 检查云安全组是否放行 TCP 58880 |
| 握手始终失败 | 检查云安全组是否放行 UDP 51820 |
| 注册重复（409） | 先删除旧 peer 再重新加入 |
| 守护进程启动失败 | `journalctl -u wg-mgmt -n 20` |
| WG 接口不存在 | `modprobe wireguard && ip link add wg0 type wireguard` |

## 许可证

MIT
