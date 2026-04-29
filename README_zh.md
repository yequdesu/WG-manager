# WG-Manager

基于 WireGuard 的管理层 — 星形拓扑 VPN，客户端一行命令即可加入。服务端运行 Go 守护进程提供 HTTP API，支持 Linux / macOS / WSL / Windows。

## 设计理念

两种连接模式，适配不同信任级别：

| 模式 | 信任度 | 流程 | API Key |
|------|--------|------|:--:|
| **审批**（默认） | 低 / 公开分发 | 客户端提交申请 → 管理员批准 → 自动配置连接 | 无 |
| **直连** | 高 / 内部使用 | 管理员提供含 API Key 的 URL → 即时加入 | 有 |

```
                     ┌───────────────────────┐
                     │      服务器 (Linux)     │
                     │                        │
                     │  wg-mgmt-daemon :58880 │
                     │  ┌──────────────────┐  │
                     │  │ GET /connect     │  │ ← 统一入口
                     │  │ POST /register   │  │    所有平台
                     │  │ POST /request    │  │
                     │  └──────────────────┘  │
                     │       wg set ↓         │
                     │  WireGuard wg0 10.0.0.1 │
                     └──┬─────────────┬───────┘
                        │ WG 隧道     │ HTTP
              ┌─────────┴───┐    ┌────┴──────────┐
              │ Linux/macOS │    │    Windows      │
              │ / WSL        │    │                 │
              │ curl｜sudo   │    │ iwr → .ps1      │
              │   bash       │    │ curl → .conf    │
              └──────────────┘    └─────────────────┘
```

## 快速开始

### 1. 服务端初始化

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager
sudo bash server/setup-server.sh
```

### 2. 开放端口

| 协议 | 端口 | 用途 |
|------|------|------|
| UDP | 51820 | WireGuard 隧道 |
| TCP | 58880 | 管理 API |

### 3. 客户端加入

**审批模式（默认，无需 API Key）：**

| 平台 | 命令 |
|------|------|
| Linux / macOS / WSL | `curl -sSf http://IP:58880/connect \| sudo bash` |
| Windows PowerShell | `iwr http://IP:58880/connect -OutFile t.ps1; .\t.ps1` |
| 浏览器 | 打开 `http://IP:58880/connect` |

**直连模式（管理员分发，内含 API Key）：**

| 平台 | 命令 |
|------|------|
| Linux / macOS / WSL | `curl -sSf "http://IP:58880/connect?mode=direct&name=DEVICE" \| sudo bash` |
| Windows | `curl -o wg0.conf "http://IP:58880/connect?mode=direct&name=MYPC"` |

> **注意**：使用 `curl | sudo bash` 管道方式执行时，stdin 已被脚本内容占用，无法弹出交互式提示。需要自定义 peer 名称时，在 URL 后追加 `?name=自定义名称`。如需交互式输入名称，先下载脚本再执行：`curl -o t.sh ...; sudo bash t.sh`。

## 管理命令

```bash
wg-mgmt-tui                          # TUI 管理面板
tail -f /var/log/wg-mgmt/audit.log   # 审计日志
bash scripts/health-check.sh         # 健康检查
bash scripts/list-peers.sh           # 查看 peer
```

**TUI 快捷键：**

| 键 | 功能 |
|----|------|
| `Tab` | 切换标签页（Peers / Requests / Status / Log） |
| `↑ ↓` | 选择列表项 |
| `a` | 批准选中的请求 |
| `d` | 删除选中的 peer / 拒绝选中的请求 |
| `r` | 刷新 |
| `q` | 退出 |

**CLI 等效操作：**

```bash
# 查看待审批
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H 'Authorization: Bearer <KEY>' | python3 -m json.tool

# 批准
curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H 'Authorization: Bearer <KEY>'

# 拒绝
curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H 'Authorization: Bearer <KEY>'

# 删除 peer
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
| `GET` | `/request/{id}` | 无 | 轮询状态：pending / approved / rejected |
| `GET` | `/requests` | API Key + localhost | 查看待审批列表 |
| `POST` | `/requests/{id}/approve` | API Key + localhost | 批准申请 |
| `DELETE` | `/requests/{id}` | API Key + localhost | 拒绝申请 |
| `GET` | `/peers` | API Key + localhost | 查看 peer 及在线状态 |
| `DELETE` | `/peers/{name}` | API Key + localhost | 删除 peer |
| `GET` | `/status` | API Key + localhost | 服务状态 |
| `GET` | `/health` | 无 | 存活检查 |

鉴权方式：Header 传 `Authorization: Bearer <KEY>` 或 URL 参数 `?key=<KEY>`。管理端点同时允许 localhost 免鉴权。

## 更新服务器

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh   # 自动检测代码变更，有变动则重新编译
```

更新过程中已有 WireGuard 连接**不受影响**。

## 项目结构

```
wg-manager/
├── cmd/
│   ├── mgmt-daemon/main.go          # 守护进程入口（HTTP API + WG 操作）
│   └── mgmt-tui/main.go             # 终端 TUI 入口
├── internal/
│   ├── api/                         # Handler、中间件、路由、内嵌脚本
│   ├── audit/                       # 审计日志写入器
│   ├── store/                       # Peer / Request 状态持久化 (peers.json)
│   └── wg/                          # WireGuard CLI 操作 (wg set / genkey / show)
├── client/
│   ├── connect.sh                   # 直连脚本模板（编译进 daemon）
│   ├── request-approval.sh          # 审批脚本模板（编译进 daemon）
│   ├── request-approval.ps1         # Windows 审批脚本（编译进 daemon）
│   ├── install-wireguard.sh         # 独立 WG 安装器
│   └── lib/os-detect.sh            # 平台抽象层（可复用库）
├── server/
│   ├── setup-server.sh              # 一键初始化 / 升级
│   └── wg-mgmt.service              # systemd unit
├── scripts/
│   ├── build.sh / build.bat         # 交叉编译
│   └── list-peers.sh / health-check.sh
├── config.env
└── Makefile
```

## 编译

```bash
make build          # 守护进程 → bin/wg-mgmt-daemon
make build-tui      # TUI → bin/wg-mgmt-tui
make build-all      # 两个一起
make vet            # go vet
```

## 常见问题

| 现象 | 解决方案 |
|------|----------|
| Windows 无法 ping 通 | 防火墙放行 ICMP：`New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| 无法访问管理 API | 检查云安全组是否放行 TCP 58880 |
| 无 WireGuard 握手 | 检查云安全组是否放行 UDP 51820 |
| 注册重复（409） | 先删除旧 peer 再重新加入 |
| 守护进程启动失败 | `journalctl -u wg-mgmt -n 20` |
| WG 接口不存在 | `modprobe wireguard && ip link add wg0 type wireguard` |
| 配置端口被写坏 | `sed -i 's/MGMT_LISTEN=.*/MGMT_LISTEN=0.0.0.0:58880/' config.env` 后 `systemctl restart wg-mgmt` |
| 管道模式无交互提示 | URL 后加 `?name=自定义名称`，或下载脚本后 `sudo bash script.sh` |

## 许可证

MIT
