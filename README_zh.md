# WG-Manager

基于 WireGuard 的管理层，提供 HTTP API 实现客户端自动注册与配置下发。

采用星形拓扑，Aliyun（或任意 Linux）服务器作为 WireGuard Hub，各客户端设备（Linux、macOS、Windows）通过一条命令即可加入。

## 架构

```
                          ┌──────────────────────────────────┐
                          │         Aliyun / Linux 服务器     │
                          │                                    │
                          │  ┌────────────────────────────┐   │
                          │  │   wg-mgmt-daemon (Go)       │   │
                          │  │   HTTP :58880               │   │
                          │  │                              │   │
                          │  │  管理端点 (仅 127.0.0.1)     │   │
                          │  │  GET  /api/v1/peers         │   │
                          │  │  DELETE /api/v1/peers/:name │   │
                          │  │  GET  /api/v1/status        │   │
                          │  │                              │   │
                          │  │  公网端点 (API Key 鉴权)     │   │
                          │  │  POST /api/v1/register       │   │
                          │  │  GET  /api/v1/client-script  │   │
                          │  │  GET  /api/v1/windows-config │   │
                          │  │  GET  /api/v1/health         │   │
                          │  └─────────────┬──────────────┘   │
                          │                │ wg set            │
                          │  ┌─────────────┴──────────────┐   │
                          │  │      WireGuard (wg0)         │   │
                          │  │      10.0.0.1/24             │   │
                          │  └─────────────────────────────┘   │
                          └────────┬──────────────┬────────────┘
                                   │              │
                         wg 隧道    │              │ HTTP :58880
                                   │              │
                    ┌──────────────┴─┐   ┌────────┴──────────┐
                    │  Linux / macOS  │   │     Windows        │
                    │  10.0.0.2       │   │     10.0.0.3       │
                    │                  │   │                    │
                    │  curl ... | bash │   │  curl → wg0.conf  │
                    └──────────────────┘   └────────────────────┘
```

## 特性

- **客户端零配置加入** — Linux/macOS 执行一行 curl 命令即可；Windows 下载 `.conf` 文件导入
- **加入新 peer 不中断已有连接** — 使用 `wg set` 直接操作 WireGuard 接口，不重启服务
- **全自动 peer 管理** — 密钥生成、IP 分配、配置持久化全由守护进程处理
- **连接稳定** — 双向 `PersistentKeepalive` 防止 NAT 超时；WireGuard 原生 Roaming 处理 IP 变更
- **管理 API** — 查看 peer 列表及在线状态、删除 peer、检查服务健康
- **Go 守护进程** — 单文件静态编译，无运行时依赖，systemd 管理并自动重启

## 快速开始

### 1. 服务端初始化（Ubuntu/Debian）

```bash
git clone git@github.com:yequdesu/WG-manager.git /root/wg-manager
cd /root/wg-manager

# 安装 Go（如未安装）
apt install -y golang-go

# 一次性初始化
sudo bash server/setup-server.sh
```

按提示输入公网 IP、端口、DNS。脚本自动完成：
- 安装 WireGuard
- 开启 IP 转发
- 生成服务端密钥
- 配置 iptables FORWARD 规则
- 编译并启动管理守护进程
- 打印客户端加入命令

### 2. 开放防火墙端口

在云服务商安全组添加入方向规则：

| 协议 | 端口 | 用途 |
|------|------|------|
| UDP | 51820 | WireGuard 隧道 |
| TCP | 58880 | 管理 API（客户端注册） |

```bash
ufw allow 51820/udp
ufw allow 58880/tcp
```

### 3. 客户端加入

**Linux / macOS：**
```bash
curl -sSf http://<服务器IP>:58880/api/v1/client-script | sudo bash
```

**Windows (CMD)：**
```cmd
curl -o wg0.conf "http://<服务器IP>:58880/api/v1/windows-config?name=%COMPUTERNAME%&key=<API_KEY>"
```
然后打开 WireGuard → Import Tunnel(s) → 选择 `wg0.conf` → 连接。

**Windows (PowerShell)：**
```powershell
Invoke-WebRequest "http://<服务器IP>:58880/api/v1/windows-config?name=$env:COMPUTERNAME&key=<API_KEY>" -OutFile wg0.conf
```

以上命令在 `setup-server.sh` 运行结束后会直接输出，可直接复制使用。

## API 参考

基础路径：`http://<服务器IP>:58880`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `POST` | `/api/v1/register` | API Key / localhost | 注册新 peer，返回 JSON（含密钥和配置） |
| `GET` | `/api/v1/client-script` | 无 | 返回 Linux/macOS 一键脚本 |
| `GET` | `/api/v1/windows-config?name=...&key=...` | API Key / localhost | 自动注册并返回 Windows 的 `.conf` 文件 |
| `GET` | `/api/v1/peers` | API Key + 仅 localhost | 列出所有 peer 及在线状态 |
| `DELETE` | `/api/v1/peers/:name` | API Key + 仅 localhost | 删除指定 peer |
| `GET` | `/api/v1/status` | API Key + 仅 localhost | 服务端和 daemon 状态 |
| `GET` | `/api/v1/health` | 无 | 存活检查（返回 `{"status":"ok"}`） |

鉴权方式：Header 中传 `Authorization: Bearer <API_KEY>` 或 URL 参数 `?key=<API_KEY>`。

## 管理命令

在服务器上执行：

```bash
# 查看所有 peer 及在线状态
curl -s http://127.0.0.1:58880/api/v1/peers \
  -H 'Authorization: Bearer <API_KEY>' | python3 -m json.tool

# 删除 peer（如客户端重装前需先删除旧记录）
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<名称> \
  -H 'Authorization: Bearer <API_KEY>'

# 服务端状态
curl -s http://127.0.0.1:58880/api/v1/status \
  -H 'Authorization: Bearer <API_KEY>'
```

或使用项目自带脚本：
```bash
bash scripts/list-peers.sh      # 查看 peer 列表
bash scripts/health-check.sh    # 健康检查
```

## 更新服务器

```bash
cd /root/wg-manager
git pull
sudo bash server/setup-server.sh   # 自动检测已有配置，源码有变更则重新编译
```

更新过程中已有 WireGuard 连接**不受影响**。

## 项目结构

```
wg-manager/
├── cmd/mgmt-daemon/main.go         # 守护进程入口
├── internal/
│   ├── api/                        # HTTP 处理器、中间件、路由
│   │   ├── handler.go              # 所有 API handler
│   │   ├── server.go               # 路由注册 + 鉴权中间件
│   │   ├── middleware.go            # Auth + AdminOnly 中间件
│   │   └── template.go             # 客户端脚本模板加载器
│   ├── wg/manager.go               # WireGuard CLI 操作封装（wg set / genkey / show）
│   └── store/peers.go              # Peer 状态持久化（JSON）
├── server/
│   ├── setup-server.sh             # 服务端一键初始化 / 升级
│   └── wg-mgmt.service             # systemd unit 模板
├── client/
│   ├── connect.sh                  # Linux/macOS 一键脚本模板
│   ├── connect.ps1                 # Windows PowerShell 辅助脚本
│   └── install-wireguard.sh        # 多系统 WireGuard 安装器
├── scripts/
│   ├── build.sh / build.bat        # 交叉编译 Linux amd64
│   ├── list-peers.sh               # 查看 peer 列表
│   └── health-check.sh             # 健康监控
├── config.env                      # 集中配置文件模板
└── Makefile
```

## 编译

```bash
# 需要 Go 1.21+
make build          # 编译 Linux amd64 → bin/wg-mgmt-daemon
make build-win      # 编译 Windows amd64（本地测试用）
make vet            # 运行 go vet
```

## 常见问题

| 现象 | 解决方案 |
|------|----------|
| Windows 无法 ping 通服务器 | 在 Windows 防火墙放行 ICMP：`New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| 无法访问管理 API | 检查云安全组是否放行 TCP 管理端口 |
| Peer 握手始终失败 | 检查云安全组是否放行 UDP WireGuard 端口 |
| 注册时提示重复（409） | 先删除旧 peer 再重新注册 |
| 守护进程启动失败 | `journalctl -u wg-mgmt -n 20` 查看日志 |
| WireGuard 接口不存在 | `sudo modprobe wireguard && sudo ip link add wg0 type wireguard` |

## 许可证

MIT
