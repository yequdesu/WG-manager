# WG-Manager

WG-Manager 是面向星形 WireGuard 拓扑的服务器端管理层。Go 守护进程负责 peer 生命周期、身份系统、邀请入网和 HTTPS bootstrap 分发；Rust Ratatui TUI 提供本地管理界面。

支持 **Linux / macOS / WSL / Windows / 移动端 QR**。当前版本只保留一种入网模型：**管理员创建一次性邀请，用户使用邀请 token 入网**。

---

## 目录

- [设计](#设计)
- [快速开始](#快速开始)
- [用户入网](#用户入网)
- [管理员操作](#管理员操作)
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

### 角色模型

| 角色 | 权限 |
|------|------|
| `owner` | 最高管理员。管理用户、邀请、peer 和系统配置。首次启动时通过 bootstrap 密码创建。 |
| `admin` | 创建/撤销邀请，管理 peer，查看状态。 |
| `user` | 通过邀请入网，后续为客户端侧能力预留。 |

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
```

---

## 生产部署

守护进程默认绑定 `127.0.0.1:58880`。生产环境必须使用 nginx 或 Caddy 在公网终止 TLS，并只暴露公共路由。

公共路由：

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

---

## 构建

### Go 守护进程

```bash
make build
make build-all
make vet
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

升级后确认：

```bash
grep MGMT_LISTEN ~/WG-manager/config.env
grep BOOTSTRAP_OWNER_PASSWORD ~/WG-manager/config.env
sudo systemctl restart wg-mgmt
sudo systemctl status wg-mgmt --no-pager
```

注意事项：

- 已有 WireGuard 连接不会中断，现有 peer 会继续保留。
- 旧的审批请求数据不再使用；需要让新用户通过邀请重新入网。
- 删除旧脚本或 cron 中对 `/api/v1/register`、`/api/v1/request`、`/connect?mode=direct` 的调用。
- 确保公网只开放 HTTPS `443/tcp` 和 WireGuard `51820/udp`；管理端口 `58880` 应只监听本机。

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

---

## License

MIT
