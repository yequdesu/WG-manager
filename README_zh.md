# WG-Manager

WG-Manager 是面向星形 WireGuard 拓扑的服务器端管理层。Go 守护进程负责 peer 生命周期、身份系统、邀请入网和 HTTPS bootstrap 分发；Rust Ratatui TUI 提供本地管理界面。此外还提供本地 CLI（`wg-mgmt`）用于服务器管理。

支持 **Linux / macOS / WSL / Windows / 移动端 QR**。当前版本只保留一种入网模型：**管理员创建一次性邀请，用户使用邀请 token 入网**。

---

## 目录

- [设计](#设计)
- [快速开始](#快速开始)
- [用户入网](#用户入网)
- [管理员操作](#管理员操作)
- [CLI 参考](#cli-参考)
- [生产部署](#生产部署)
- [构建](#构建)
- [升级旧服务器](#升级旧服务器)
- [弃用说明](#弃用说明)
- [排障](#排障)

---

## 设计

### 单一入网模型

系统使用邀请入网：`owner` 或 `admin` 创建邀请 token，用户兑换 token，服务端原子创建 peer 并返回 WireGuard 配置。没有审批队列，没有直连注册，也不会向公网分发 `MGMT_API_KEY`。

```text
owner/admin 创建邀请  ->  用户兑换邀请  ->  peer 原子创建并写入 WireGuard
```

### 能力矩阵

三种固定角色：owner、admin、user。不支持自定义角色。

| 操作 | Owner | Admin | User | 强制策略 |
|------|-------|-------|------|----------|
| 查看自身状态 (`GET /api/v1/me`) | ✅ | ✅ | ✅ | 基于 session，可远程访问 |
| 健康检查、登录、登出、兑换邀请 | ✅ | ✅ | ✅ | 公开 |
| Bootstrap、connect 页面 | ✅ | ✅ | ✅ | 公开 |
| 列出 peer | ✅ | ✅ | ❌ | LocalOnly |
| 删除 peer（按名称或公钥） | ✅ | ✅ | ❌ | LocalOnly |
| 设置 peer 别名 | ✅ | ✅ | ❌ | LocalOnly |
| 查看守护进程状态 | ✅ | ✅ | ❌ | LocalOnly |
| 列出邀请 | ✅ | ✅ | ❌ | LocalOnly |
| 创建邀请 | ✅ | ✅* | ❌ | LocalOnly |
| 邀请 QR 码 | ✅ | ✅ | ❌ | LocalOnly |
| 撤销邀请 | ✅ | ✅ | ❌ | LocalOnly |
| 删除邀请（软删除） | ✅ | ✅ | ❌ | LocalOnly |
| 列出用户 | ✅ | ❌ | ❌ | LocalOnly + Owner |
| 创建用户 | ✅ | ❌ | ❌ | LocalOnly + Owner |
| 删除用户 | ✅ | ❌ | ❌ | LocalOnly + Owner |
| 首次启动创建 owner | ✅ | ❌ | ❌ | 尚无用户时一次性执行 |

*Admin 只能创建 target_role="user" 的邀请，不能创建 owner 级别的邀请或直接创建用户账户。

- **LocalOnly** = 仅可从 `127.0.0.1` 访问。通过 SSH 或本地终端操作。
- **API 密钥**（`MGMT_API_KEY`）绕过角色检查，但受 LocalOnly 限制，防止远程滥用。

### 架构

```
┌─ Server ───────────────────────────────────────┐
│  nginx/caddy :443 (TLS 终止)                   │
│       │ proxy_pass localhost:58880              │
│  ┌────┴─────────────────────────────────────┐  │
│  │ wg-mgmt-daemon 127.0.0.1:58880           │  │
│  │ POST /api/v1/login     ← session auth    │  │
│  │ POST /api/v1/redeem    ← 兑换邀请        │  │
│  │ GET  /bootstrap        ← 加入脚本         │  │
│  │ GET  /connect          ← 浏览器入口       │  │
│  │        │ wg set                           │  │
│  │ WireGuard wg0  10.0.0.1/24               │  │
│  └──────────────────────────────────────────┘  │
└────────┬──────────────┬─────────────────────────┘
         │ WG 隧道      │ HTTPS
      ┌──┴────┐    ┌────┴──────┐
      │ Linux │    │  Windows   │
      │ macOS │    │  PS / CMD  │
      │ WSL   │    │  移动端 QR  │
      └───────┘    └────────────┘
```

---

## 快速开始

### 1. 服务端初始化

前置条件：Ubuntu/Debian 服务器，已安装 Git，生产环境建议准备一个指向服务器的域名。

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager
sudo apt install -y golang-go
sudo bash server/setup-server.sh
```

脚本会提示公网 IP、WireGuard 端口、VPN 子网、管理端口和默认 DNS。安装完成后会生成 `config.env`，其中关键项如下：

| 配置项 | 说明 |
|--------|------|
| `MGMT_LISTEN=127.0.0.1:58880` | 守护进程默认只监听本机，生产环境通过反向代理暴露 HTTPS。 |
| `MGMT_API_KEY` | break-glass 管理后路，不应分发给用户。 |
| `BOOTSTRAP_OWNER_PASSWORD` | 首次启动时创建 `admin` owner 账户的密码。 |

**升级服务器：**
```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh
# "Use existing configuration? [Y/n]" -> Y + Enter
```

#### 地址池

你可以将 VPN 子网划分为命名的地址范围（池）。分配到某个池的 peer 会从该范围获取 IP。在 `config.env` 中添加以 `POOL_` 为前缀的行：

```
POOL_CLIENTS=10.0.0.10-10.0.0.100
POOL_SERVERS=10.0.0.101-10.0.0.200
```

格式为 `POOL_<名称>=<起始IP>-<结束IP>`。创建邀请时传入 `pool_name` 将 peer 分配到对应池：

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)
curl -s -X POST http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"pool_name": "CLIENTS"}'
```

规则：
- 池之间不能重叠。
- 池不能包含服务器 IP（子网 .1 地址）。
- 整个范围必须在 `WG_SUBNET` 内。
- 无效的池配置会记录警告并禁用池功能。

查看首次 owner 密码：

```bash
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env
```

### 2. 创建邀请

可以用 Rust TUI 创建邀请：

```bash
cd ~/WG-manager/wg-tui
bash install.sh --ustc
wg-tui
```

也可以用 API 创建邀请：

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)

curl -s -X POST http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"max_uses": 1}'
```

响应会包含一次性 `token` 和 bootstrap URL，例如：

```json
{
  "token": "inv_abc123...",
  "url": "https://vpn.example.com/bootstrap?token=inv_abc123..."
}
```

---

## 用户入网

### Linux / macOS / WSL

```bash
curl -sSf "https://vpn.example.com/bootstrap?token=inv_abc123&name=my-device" | sudo bash
```

脚本会检测系统、安装 WireGuard（如需要）、兑换邀请、写入配置、启动隧道并验证连通性。

### Windows PowerShell

```powershell
Invoke-WebRequest "https://vpn.example.com/bootstrap?token=inv_abc123&name=MYPC" -OutFile join.ps1
.\join.ps1
```

### Windows CMD

```cmd
curl -o wg0.conf "https://vpn.example.com/bootstrap?token=inv_abc123&name=MYPC"
```

然后在 WireGuard 客户端中导入 `wg0.conf`。

### 移动端 QR

管理员在服务器本地生成 QR：

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)

curl -s "http://127.0.0.1:58880/api/v1/invites/qrcode?token=inv_abc123&name=phone1" \
  -H "Authorization: Bearer $API_KEY" \
  -o phone1.svg
```

将 `phone1.svg` 发给用户，用户用 WireGuard 移动端扫码导入。

---

## 管理员操作

### TUI

```bash
wg-tui
```

常用快捷键：

| 键 | 动作 |
|----|------|
| `Tab` / `←→` | 切换标签页 |
| `↑↓` / `j` `k` | 选择条目 |
| `d` / `y` | 删除 peer / 撤销邀请 |
| `r` | 刷新 |
| `?` | 帮助 |
| `q` | 退出 |

### API

```bash
# 列出邀请
curl -s http://127.0.0.1:58880/api/v1/invites \
  -H "Authorization: Bearer $API_KEY"

# 撤销邀请
curl -s -X DELETE http://127.0.0.1:58880/api/v1/invites/inv_abc123 \
  -H "Authorization: Bearer $API_KEY"

# 列出 peer
curl -s http://127.0.0.1:58880/api/v1/peers \
  -H "Authorization: Bearer $API_KEY"

# 删除 peer（按名称）
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<name> \
  -H "Authorization: Bearer $API_KEY"
```

---

## CLI 参考

`wg-mgmt` CLI 在服务器上运行，通过 localhost 与守护进程通信。默认读取 `config.env` 中的 `MGMT_LISTEN` 和 `MGMT_API_KEY`。

```bash
# 构建 CLI
make build-cli

./bin/wg-mgmt --help
# Usage: ./bin/wg-mgmt [--config FILE] <command>
```

### 全局参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--config FILE` | `config.env` | 配置文件路径 |

### 子命令

| 命令 | 说明 | 状态 |
|------|------|------|
| `peer` | 列出、别名、删除 peer | 已实现 |
| `invite` | 邀请操作 | 已搭建 |
| `user` | 用户操作 | 已搭建 |
| `status` | 守护进程和 WireGuard 状态 | 已实现 |
| `auth` | 会话认证 | 已搭建 |
| `me` | 当前用户信息 | 已实现 |

### peer list

列出所有 peer，包含公钥、别名、名称、IP、在线状态和端点。

```bash
# 表格输出（默认）
./bin/wg-mgmt peer list

# JSON 输出
./bin/wg-mgmt peer list --format json
```

表格输出示例：

```
  PUBLIC KEY       ALIAS   NAME     IP          ONLINE   ENDPOINT
  RoJ7SRMQC7Zu     my-lap  my-lap   10.0.0.2    online   112.49.240.57:16262
  aB3cDeFgHiJk     phone   phone1   10.0.0.3    offline
```

### peer alias

为 peer 设置友好别名，通过不可变的公钥标识。

```bash
./bin/wg-mgmt peer alias --id <public_key> --alias <new_name>
```

示例：

```bash
./bin/wg-mgmt peer alias --id RoJ7SRMQC7Zu... --alias "John's Laptop"
# Alias updated: "" -> "John's Laptop" (peer: my-lap)
```

### peer delete

通过公钥删除 peer。仅依赖别名的删除会被拒绝。

```bash
./bin/wg-mgmt peer delete --id RoJ7SRMQC7Zu...
```

### status

显示守护进程和 WireGuard 接口状态。

```bash
# 可读输出
./bin/wg-mgmt status

# JSON
./bin/wg-mgmt status --format json
```

可读输出示例：

```
daemon: running
wireguard: ok
interface: wg0
listen_port: 51820
peers: 3 online / 5 total
```

### me

显示当前认证用户的名称、角色和创建时间。需要 session token。

```bash
# 使用 MGMT_SESSION_TOKEN 环境变量或 --session-token 参数
export MGMT_SESSION_TOKEN=<session_token>
./bin/wg-mgmt me

# JSON
./bin/wg-mgmt me --format json
```

示例：

```
name: admin
role: owner
created_at: 2026-05-03T15:00:00Z
```

### invite、user、auth

这些命令已搭建框架，将在未来版本中实现。请使用 API 或 TUI 执行相关操作。

---

## 生产部署

守护进程默认绑定 `127.0.0.1:58880`。生产环境必须使用 nginx 或 Caddy 在公网终止 TLS，并只暴露公共路由。

```
                   ┌─────────────────────────────┐
  Internet ── TLS ─→  nginx / caddy :443         │
                   │  ┌───────────────────────┐  │
                   │  │ 终止 TLS             │  │
                   │  │ 分离公共/管理路由      │  │
                   │  └───────┬───────────────┘  │
                   │          │ localhost:58880   │
                   │  ┌───────┴───────────────┐  │
                   │  │ wg-mgmt-daemon        │  │
                   │  │ 127.0.0.1:58880       │  │
                   │  └───────────────────────┘  │
                   └─────────────────────────────┘
```

### 部署自动化

`server/setup-server.sh` 脚本自动完成守护进程安装、WireGuard 初始化和 systemd 服务配置。它**不**配置反向代理。运行脚本后，需要手动配置 nginx 或 Caddy（参考下面的示例）并使用 Let's Encrypt 提供 TLS 证书。

### 路由隔离

公共路由（可通过 HTTPS 安全暴露）：

| 路由 | 说明 |
|------|------|
| `/api/v1/health` | 健康检查 |
| `/api/v1/login` | 登录 |
| `/api/v1/logout` | 登出 |
| `/api/v1/redeem` | 兑换邀请 |
| `/bootstrap` | bootstrap 脚本 |
| `/connect` | 浏览器入网页 |

管理路由只能本机访问，不要通过反向代理暴露：

| 路由 | 说明 |
|------|------|
| `/api/v1/peers` | peer 管理 |
| `/api/v1/invites` | 邀请管理 |
| `/api/v1/users` | 用户管理 |
| `/api/v1/status` | 状态 |

nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    ssl_certificate     /etc/letsencrypt/live/vpn.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vpn.example.com/privkey.pem;

    location /api/v1/health { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/login  { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/logout { proxy_pass http://127.0.0.1:58880; }
    location /api/v1/redeem { proxy_pass http://127.0.0.1:58880; }
    location /bootstrap     { proxy_pass http://127.0.0.1:58880; }
    location /connect       { proxy_pass http://127.0.0.1:58880; }

    location /api/v1/peers   { return 403; }
    location /api/v1/invites { return 403; }
    location /api/v1/users   { return 403; }
    location /api/v1/status  { return 403; }
}
```

Caddy 示例：

```
vpn.example.com {
    reverse_proxy /api/v1/health  127.0.0.1:58880
    reverse_proxy /api/v1/login   127.0.0.1:58880
    reverse_proxy /api/v1/logout  127.0.0.1:58880
    reverse_proxy /api/v1/redeem  127.0.0.1:58880
    reverse_proxy /bootstrap      127.0.0.1:58880
    reverse_proxy /connect        127.0.0.1:58880

    respond /api/v1/peers   403
    respond /api/v1/invites 403
    respond /api/v1/users   403
    respond /api/v1/status  403
}
```

### Bootstrap URL

配置反向代理后，标准 bootstrap URL 为：

```
https://vpn.example.com/bootstrap?token=INVITE_TOKEN&name=MYDEVICE
```

用户可通过管道直接传给 bash。建议先检查脚本内容：

```bash
# 检查
curl -sSf https://vpn.example.com/bootstrap?token=TOKEN&name=my-device

# 执行（确认后）
curl -sSf "https://vpn.example.com/bootstrap?token=TOKEN&name=my-device" | sudo bash
```

bootstrap 脚本不包含全局 API 密钥，邀请 token 是唯一凭证，且一次性使用。

---

## 构建

### Go 守护进程 + CLI

```bash
make build       # 守护进程 -> bin/wg-mgmt-daemon
make build-cli   # CLI -> bin/wg-mgmt
make build-all   # 守护进程 + CLI
make vet         # go vet ./...
make clean       # 清除构建产物
```

### Rust TUI

```bash
cd wg-tui
cargo build --release
bash build-linux.sh
```

---

## 升级旧服务器

已有旧版本正在运行时，使用以下流程升级：

```bash
cd ~/WG-manager
git pull
sudo bash server/setup-server.sh
# 出现 "Use existing configuration? [Y/n]" 时输入 Y 回车

# 可选：安装/更新 Rust TUI
cd ~/WG-manager/wg-tui && bash install.sh
```

### 状态迁移

启动时，守护进程自动与运行中的 WireGuard 接口协调状态：

1. **peer 恢复** - WireGuard 中存在但 `peers.json` 中缺失的 peer 会自动恢复（使用自动生成的名称）。
2. **补充 peer** - `peers.json` 中存在但 WireGuard 接口中没有的 peer 会被重新添加。
3. **别名和邀请迁移** - 如果现有状态缺少别名或邀请字段，会自动回填。
4. **池配置** - 如果 `config.env` 中存在 `POOL_*` 条目，会在启动时解析并加载。

升级后，守护进程日志会显示迁移结果：

```
State migration complete: 5 peer alias(es), 3 invite(s) backfilled
Loaded 2 address pool(s)
```

### 升级后确认

```bash
grep MGMT_LISTEN ~/WG-manager/config.env
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env
sudo systemctl restart wg-mgmt
sudo systemctl status wg-mgmt --no-pager

# 可选：添加地址池
# 编辑 config.env 添加：POOL_NAME=startIP-endIP
# 然后热加载配置：
sudo systemctl kill -s HUP wg-mgmt
```

### 从旧审批/直连模型升级

如果从使用旧审批或直连注册流程的版本升级：

1. 旧端点（`/api/v1/register`、`/api/v1/request` 等）现已返回 `410 Gone`。
2. 已有的 peer 连接不会中断，无需操作。
3. 旧的审批请求数据不再使用，需要通过创建邀请重新入网。
4. 通过 API 或 TUI 为现有用户创建邀请。
5. 配置 HTTPS 反向代理指向 `http://127.0.0.1:58880`。
6. 删除旧脚本中对旧端点的调用。

---

## 弃用说明

以下旧端点保留为迁移提示，统一返回 `410 Gone`：

| 旧端点 | 替代方案 |
|--------|----------|
| `POST /api/v1/register` | `POST /api/v1/redeem` |
| `POST /api/v1/request` | `POST /api/v1/redeem` |
| `GET /api/v1/request/{id}` | 无需轮询 |
| `GET /api/v1/requests` | `GET /api/v1/invites` |
| `POST /api/v1/requests/{id}/approve` | 创建邀请 |
| `DELETE /api/v1/requests/{id}` | 撤销邀请 |

移除的旧行为：

- `?mode=direct`
- `?mode=approval`
- `CLEAN_PEERS_ON_EXIT`
- `scripts/create-peer.sh`
- 旧 Go TUI 和 `wg-tui --legacy`

---

## 排障

| 问题 | 处理 |
|------|------|
| API 访问失败 | 检查 nginx/Caddy 与安全组是否允许 `443/tcp` |
| 无 WireGuard handshake | 检查安全组是否允许 `51820/udp` |
| peer 名称重复 | 删除旧 peer 后重新使用邀请入网 |
| 邀请 token 无效 | token 一次性使用，检查是否已兑换或撤销 |
| 守护进程启动失败 | `journalctl -u wg-mgmt -n 50 --no-pager` |
| TUI 未安装 | `cd ~/WG-manager/wg-tui && bash install.sh` |
| 日志为空 | 日志已轮转，运行 `sudo systemctl kill -s HUP wg-mgmt` |
| WG 接口丢失 | `modprobe wireguard && ip link add wg0 type wireguard` |

---

## License

MIT
