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
| 查看邀请链接/入网指令 | ✅ | ✅ | ❌ | LocalOnly |
| 撤销邀请 | ✅ | ✅ | ❌ | LocalOnly |
| 删除邀请（软删除） | ✅ | ✅ | ❌ | LocalOnly |
| 强制删除邀请 | ✅ | ✅ | ❌ | LocalOnly |
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

前置条件：Ubuntu/Debian 服务器，已安装 Git。域名可选，但推荐配置（可以自动启用 HTTPS）。

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager
sudo apt install -y golang-go
sudo bash server/setup-server.sh
```

脚本会提示以下信息：

| 提示 | 说明 | 示例 |
|------|------|------|
| `Server Public IP` | 公网 IP（自动检测，回车确认） | `118.178.171.166` |
| `Server Domain (optional)` | 域名；回车跳过则使用 IP + HTTP | `vpn.example.com` |
| `WireGuard Port` | WireGuard 监听端口 | `51820`（默认） |
| `VPN Subnet` | VPN 内部子网 | `10.0.0.0/24`（默认） |
| `Management API Port` | 管理 API 端口 | `58880`（默认） |
| `Default Client DNS` | 客户端 DNS | `1.1.1.1,8.8.8.8`（默认） |

安装完成后：
- CLI `wg-mgmt` 自动安装到 `/usr/local/bin/wg-mgmt`
- 守护进程 `wg-mgmt-daemon` 作为 systemd 服务运行
- 会显示 bootstrap owner 密码和后续步骤

关键配置项说明：

| 配置项 | 说明 |
|--------|------|
| `MGMT_LISTEN=127.0.0.1:58880` | 守护进程默认只监听本机，生产环境通过反向代理暴露 HTTPS。 |
| `MGMT_API_KEY` | break-glass 管理后路，不应分发给用户。 |
| `BOOTSTRAP_OWNER_PASSWORD` | 首次启动时创建 `admin` owner 账户的密码。 |

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

响应会包含一次性 `token` 和可直接使用的 bootstrap URL（协议和主机取决于你的服务器配置）：

```json
{
  "token": "inv_abc123...",
  "url": "https://YOUR_DOMAIN/bootstrap?token=inv_abc123..."
}
```

配置了域名时 URL 使用 `https://YOUR_DOMAIN/`，未配置域名时降级为 `http://YOUR_SERVER_IP/`。

---

## 用户入网

### Linux / macOS / WSL

将下方的 `BOOTSTRAP_URL` 替换为创建邀请时获得的实际 URL。

```bash
curl -sSf "BOOTSTRAP_URL" | sudo bash
```

脚本会检测系统、安装 WireGuard（如需要）、兑换邀请、写入配置、启动隧道并验证连通性。

**WSL 用户注意：** 脚本会检测 WSL 环境并给出提示，推荐在 Windows 宿主机上安装 WireGuard，而非在 WSL 内部。请改用下方的 Windows PowerShell 或 CMD 方式入网。

### Windows PowerShell

```powershell
Invoke-WebRequest "BOOTSTRAP_URL&name=MYPC" -OutFile join.ps1
.\join.ps1
```

### Windows CMD

```cmd
curl -o wg0.conf "BOOTSTRAP_URL&name=MYPC"
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
| `v` | 查看所选邀请的完整链接和入网指令 |
| `F` | 二次确认后强制删除所选邀请 |
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

`wg-mgmt` CLI 在服务器上运行，通过 localhost 与守护进程通信。默认读取 `config.env` 中的 `MGMT_LISTEN` 和 `MGMT_API_KEY`。安装脚本会自动把 CLI 安装到 `/usr/local/bin/wg-mgmt`。

```bash
wg-mgmt --help
# Usage: wg-mgmt [--config FILE] <command>
```

### 全局参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--config FILE` | `config.env` | 配置文件路径 |

### 子命令

| 命令 | 说明 |
|------|------|
| `peer` | 列出、别名、删除 peer |
| `invite` | 创建、列出、撤销、删除邀请，生成 QR 码 |
| `user` | 列出、创建、删除用户 |
| `status` | 守护进程和 WireGuard 状态 |
| `auth` | 登录和登出（会话管理） |
| `me` | 当前用户信息 |

---

### peer list

列出所有 peer，包含公钥、别名、名称、IP、在线状态和端点。

```bash
# 表格输出（默认）
wg-mgmt peer list

# JSON 输出
wg-mgmt peer list --format json
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
wg-mgmt peer alias --id <public_key> --alias <new_name>
```

示例：

```bash
wg-mgmt peer alias --id RoJ7SRMQC7Zu... --alias "John's Laptop"
# Alias updated: "" -> "John's Laptop" (peer: my-lap)
```

### peer delete

通过公钥删除 peer。仅依赖别名的删除会被拒绝。

```bash
wg-mgmt peer delete --id RoJ7SRMQC7Zu...
```

---

### invite create

创建新的邀请 token，可设置可选约束。

```bash
wg-mgmt invite create [flags]
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--ttl N` | `72` | 有效期（小时） |
| `--pool NAME` | - | 地址池名称 |
| `--dns SERVERS` | - | 自定义 DNS 服务器 |
| `--role ROLE` | - | 目标角色（`user` 或 `admin`） |
| `--max-uses N` | `1` | 最大使用次数 |
| `--labels K=V,...` | - | 逗号分隔的键值对标签 |
| `--name-hint TEXT` | - | 邀请显示名称提示 |
| `--device-name NAME` | - | 预绑定的设备名称 |
| `--format FORMAT` | `human` | 输出格式（`human` 或 `json`） |

可读输出包含 token、过期时间、可直接复制的 bootstrap URL 和一键执行命令：

```
Invite created: inv_abc123...
Token:       inv_abc123...
Expires:     2026-05-10T15:00:00Z

Bootstrap URL (share this with the client):
  https://YOUR_DOMAIN/bootstrap?token=inv_abc123...

Copy-and-paste command:
  curl -sSf "https://YOUR_DOMAIN/bootstrap?token=inv_abc123..." | sudo bash
```

### invite list

列出所有邀请及其状态、提示、创建者和兑换信息。

```bash
wg-mgmt invite list [--format human|json] [--show-deleted]
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--format` | `human` | 输出格式 |
| `--show-deleted` | `false` | 包含软删除的邀请 |

可读输出示例：

```
ID           STATUS    NAME HINT   ISSUED BY   EXPIRES             REDEEMED
inv_abc123   created   my-laptop   admin       2026-05-10T15:...   -
inv_def456   redeemed  phone1      admin       -                   2026-05-08T10:... by john
```

### invite revoke

按 ID 撤销邀请。已兑换的邀请无法撤销。

```bash
wg-mgmt invite revoke --id <invite_id>
# 也支持类似 Docker 的简写：
wg-mgmt invite revoke <id_prefix_or_name_hint>
```

### invite delete

软删除邀请（标记为已删除但保留历史记录）。

```bash
wg-mgmt invite delete --id <invite_id>
# 也支持类似 Docker 的简写：
wg-mgmt invite delete <id_prefix_or_name_hint>
```

### invite link

再次查看已发出邀请的完整 bootstrap URL 和可复制入网指令。新邀请可通过 invite ID 查询；旧版本创建、未保存 raw token 的邀请需要使用原始 token，或重新创建邀请。

如果创建邀请时指定了 `--device-name`，生成链接会自动复用该设备名；否则链接不包含 `name`，bootstrap 脚本会使用客户端 hostname。显式传入 `--name` 时会覆盖已保存的设备名。

```bash
# 也支持使用 invite list 里显示的短 ID 或唯一名称：
wg-mgmt invite link <id_prefix_or_name_hint>
# 如需覆盖 peer 名称：
wg-mgmt invite link <id_prefix_or_name_hint> --name <device_name>
```

| 参数 | 必需 | 说明 |
|------|------|------|
| `--id` | 否 | 邀请 ID 或原始 token；也可作为位置参数传入 |
| `--name` | 否 | 覆盖写入 bootstrap URL 的设备名 |
| `--format` | 否 | 输出格式（`human` 或 `json`） |

### invite force-delete

永久删除任意状态的邀请。该操作不可恢复，必须通过 `--confirm` 再次输入相同 invite ID。

```bash
wg-mgmt invite force-delete --id <invite_id> --confirm <invite_id>
# 前缀/名称会像 Docker 一样解析，但仍必须二次确认：
wg-mgmt invite force-delete <id_prefix_or_name_hint> --confirm <same_prefix_or_full_id>
```

### invite qrcode

根据邀请 token 生成 SVG 格式的 QR 码。

```bash
wg-mgmt invite qrcode --id <token> --name <device_name> --output <file.svg>
```

| 参数 | 必需 | 说明 |
|------|------|------|
| `--id` | 是 | 邀请 token（来自 create 输出） |
| `--name` | 否 | QR URL 中的设备名称；默认省略，由 bootstrap 使用客户端 hostname |
| `--output` | 是 | 输出 SVG 文件路径 |

---

### user list

列出所有用户，包含名称、角色和创建时间。需要 API 密钥（仅 owner）。

```bash
# 表格输出（默认）
wg-mgmt user list

# JSON 输出
wg-mgmt user list --format json
```

### user create

创建新用户账户。需要 API 密钥（仅 owner）。

```bash
wg-mgmt user create --name <username> --password <password> [--role <role>]
```

| 参数 | 必需 | 默认值 | 说明 |
|------|------|--------|------|
| `--name` | 是 | - | 用户名 |
| `--password` | 是 | - | 密码 |
| `--role` | 否 | `user` | 角色（`owner`、`admin`、`user`） |

### user delete

按名称删除用户。需要 API 密钥（仅 owner）。

```bash
wg-mgmt user delete --name <username>
```

---

### auth login

与守护进程认证并获取 session token。

```bash
wg-mgmt auth login [--name <username> --password <password>]
```

不带参数运行时，会交互式提示输入用户名和密码。如果 CLI 已配置 API 密钥，会提示已认证。

输出包含 session token，可存入 `MGMT_SESSION_TOKEN` 环境变量。

### auth logout

登出当前会话。

```bash
wg-mgmt auth logout
```

---

### status

显示守护进程和 WireGuard 接口状态。

```bash
# 可读输出
wg-mgmt status

# JSON
wg-mgmt status --format json
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
wg-mgmt me

# JSON
wg-mgmt me --format json
```

示例：

```
name: admin
role: owner
created_at: 2026-05-03T15:00:00Z
```

---

## 生产部署

守护进程默认绑定 `127.0.0.1:58880`。生产环境通过 nginx 或 Caddy 反向代理暴露 HTTPS，守护进程本身只监听本机。

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

`server/setup-server.sh` 脚本自动完成守护进程安装、WireGuard 初始化和 systemd 服务配置。之后运行引导式反向代理部署脚本：

```bash
# nginx（默认）：
sudo bash server/deploy-proxy.sh

# Caddy：
sudo bash server/deploy-proxy.sh --caddy
```

`deploy-proxy.sh` 脚本自动处理：

- **配置了域名**：自动配置 nginx 或 Caddy 并获取 Let's Encrypt TLS 证书，生成 HTTPS 配置，验证后重新加载。
- **未配置域名**：生成纯 HTTP 配置，并提示 HTTPS 不可用。bootstrap URL 使用 `http://SERVER_IP/`。
- **安全措施**：备份现有配置，验证生成的配置（`nginx -t` 或 `caddy validate`），验证失败自动回滚。
- **路由隔离**：在代理层面拦截管理路由（返回 403），只暴露公共路由。

### TLS 策略

守护进程本身使用纯 HTTP 与本机通信。TLS 由反向代理处理。

| 场景 | 行为 |
|------|------|
| 配置了域名且 DNS 可解析 | 自动通过 Let's Encrypt 获取证书（nginx：certbot standalone；Caddy：自动 ACME） |
| 未配置域名或 DNS 未解析 | 纯 HTTP 降级，显示明确警告。Bootstrap URL 使用 `http://` 加公网 IP |

### 手动安装证书（阿里云 SSL）

如果你从阿里云下载了证书文件，请把证书和私钥文件直接放进同一个文件夹，不需要再建一个域名同名子文件夹。推荐命名为 `<domain>.pem` 和 `<domain>.key`（例如 `wg.yequdesu.top.pem`、`wg.yequdesu.top.key`）；如果没有精确匹配的文件名，脚本也可以自动识别唯一的一份证书文件（`*.pem`、`*.crt` 或 `*.cer`）和唯一的一份 `*.key` 文件。然后一条命令完成安装、校验和回滚：

```bash
sudo bash server/install-cert.sh /path/to/certs wg.yequdesu.top
make install-cert CERT_DIR=/path/to/certs DOMAIN=wg.yequdesu.top
```

脚本会校验证书和私钥、检查公钥是否匹配、在证书缺失或候选文件不唯一时提示、在 subject/SAN 不一致时给出警告、改动前展示目标路径，覆盖前先备份 `/etc/letsencrypt/live/<domain>/` 现有文件，并在 `nginx -t` 或重载失败时自动回滚。

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

邀请管理仅限本机访问，常用管理端点包括：

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/invites/{id}/link` | 查看完整 bootstrap URL 和入网指令 |
| `DELETE` | `/api/v1/invites/{id}` | 撤销邀请 |
| `DELETE` | `/api/v1/invites/{id}?action=force-delete` | 永久强制删除邀请 |

### nginx 示例（由 deploy-proxy.sh --nginx 生成）

```nginx
server {
    listen 443 ssl http2;
    server_name YOUR_DOMAIN;

    ssl_certificate     /etc/letsencrypt/live/YOUR_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/YOUR_DOMAIN/privkey.pem;

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

未配置域名时，脚本生成纯 HTTP 配置（无 TLS，无重定向）。

### Caddy 示例（由 deploy-proxy.sh --caddy 生成）

配置域名时（Caddy 自动申请 TLS）：

```
YOUR_DOMAIN {
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

未配置域名时，脚本生成 `:80` 配置块（纯 HTTP）。

### Bootstrap URL

配置反向代理后，bootstrap URL 使用你的域名或服务器 IP：

| 场景 | Bootstrap URL 格式 |
|------|-------------------|
| 域名 + HTTPS | `https://YOUR_DOMAIN/bootstrap?token=TOKEN` |
| 仅 IP + HTTP | `http://YOUR_SERVER_IP/bootstrap?token=TOKEN` |
| 显式设备名 | `https://YOUR_DOMAIN/bootstrap?token=TOKEN&name=DEVICE` |

用户可通过管道直接传给 bash。建议先检查脚本内容：

```bash
# 检查
curl -sSf "https://YOUR_DOMAIN/bootstrap?token=TOKEN"

# 执行（确认后）
curl -sSf "https://YOUR_DOMAIN/bootstrap?token=TOKEN" | sudo bash
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

已有旧版本正在运行时，使用以下流程升级。现有 WireGuard 隧道不会中断，因为升级会保留 `/etc/wireguard/wg0.conf` 和 `server/peers.json`。

```bash
cd ~/WG-manager
git pull
sudo bash server/setup-server.sh
# 出现 "Use existing configuration? [Y/n]" 时输入 Y 回车
```

升级脚本自动执行：
- 源码变更时自动重新编译并安装守护进程（`wg-mgmt-daemon`）
- 自动重新编译并安装 CLI（`wg-mgmt` 到 `/usr/local/bin/wg-mgmt`）
- 保留现有配置、API 密钥和 bootstrap owner 密码
- 重启 systemd 服务而不中断现有 WireGuard 连接

**可选：** 安装或更新 Rust TUI：
```bash
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
| WSL bootstrap 问题 | 脚本会自动检测 WSL 并提示在 Windows 宿主机上安装 WireGuard，而非 WSL 内部。请使用 Windows PowerShell 或 CMD 方式入网。 |
| Bootstrap 解析错误 | bootstrap 脚本会依次尝试 `jq`、`python3`、基础 `grep`/`sed` fallback。若全部失败，按提示安装 `jq` 或 `python3`；若服务端已返回 HTTP 200，则需要管理员重新发放邀请并保留原始响应用于排查。 |

---

## License

MIT
