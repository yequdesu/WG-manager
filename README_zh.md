# WG-Manager

基于 WireGuard 的管理层 — 星形拓扑 VPN，客户端一行命令即可加入。服务端运行 Go 守护进程提供 HTTP API。可选安装基于 Ratatui (Rust) 的增强 TUI 仪表盘。

支持 **Linux / macOS / WSL / Windows / 移动端（QR 码）**。

---

## 目录

- [设计理念](#设计理念)
- [快速开始](#快速开始)
  - [1. 服务端初始化](#1-服务端初始化)
  - [2. 开放端口](#2-开放端口)
  - [3. 客户端加入](#3-客户端加入)
  - [4. 管理员操作](#4-管理员操作)
- [增强 TUI 仪表盘](#增强-tui-仪表盘)
- [日志与诊断](#日志与诊断)
- [API 参考](#api-参考)
- [更新服务器](#更新服务器)
- [编译](#编译)
- [常见问题](#常见问题)

---

## 设计理念

两种连接模式，适配不同信任级别：

| 模式 | 信任度 | 适用场景 | 是否需要 API Key |
|------|--------|----------|:--:|
| **审批**（默认） | 低 / 公开 | 对外分发、访客接入、不愿暴露 API Key 的场景 | 否 |
| **直连** | 高 / 内部 | 管理员直接分发给信任设备，即时加入 | 是（服务端嵌入脚本） |

```
┌─ 服务器 ──────────────────────────────────┐
│  wg-mgmt-daemon :58880                     │
│  GET /connect   ← 所有平台统一入口          │
│  POST /register ← 直连注册                  │
│  POST /request  ← 审批提交                  │
│         │ wg set                            │
│  WireGuard wg0  10.0.0.1/24                │
└────┬──────────────┬────────────────────────┘
     │ WG 隧道       │ HTTP
  ┌──┴────┐    ┌────┴──────┐
  │ Linux │    │  Windows   │
  │ macOS │    │  PS / CMD  │
  │ WSL   │    │  Mobile QR │
  └───────┘    └────────────┘
```

---

## 快速开始

### 1. 服务端初始化

**前置条件：** 一台 Ubuntu/Debian Linux 服务器，已安装 Git。

```bash
git clone git@github.com:yequdesu/WG-manager.git ~/WG-manager
cd ~/WG-manager

# 安装 Go（如未安装）
sudo apt install -y golang-go

# 一键初始化
sudo bash server/setup-server.sh
```

脚本会依次提示：

| 提示 | 说明 | 示例 |
|------|------|------|
| `Server Public IP` | 服务器公网 IP（自动检测，回车确认） | `118.178.171.166` |
| `WireGuard Port` | WG 监听端口 | `51820`（回车默认） |
| `VPN Subnet` | VPN 内网子网 | `10.0.0.0/24`（回车默认） |
| `Management API Port` | 管理 API 端口 | `58880`（回车默认） |
| `Default Client DNS` | 客户端 DNS | `1.1.1.1,8.8.8.8`（回车默认） |

完成后输出摘要，包含连接命令和管理命令。

**后续升级服务器**只需执行：
```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh
# 提示 "Use existing configuration? [Y/n]" → 输入 Y 回车
```

---

### 2. 开放端口

在云服务商控制台的安全组中添加**入方向**规则：

| 协议 | 端口 | 用途 |
|------|------|------|
| UDP | 51820 | WireGuard 隧道 |
| TCP | 58880 | 管理 API（客户端注册 / 脚本下载） |

服务器本地防火墙（如有 UFW）：
```bash
sudo ufw allow 51820/udp
sudo ufw allow 58880/tcp
```

---

### 3. 客户端加入

以下命令中，把 `118.178.171.166` 替换为你的服务器 IP。

---

#### 3.1 审批模式（默认，无需 API Key）

> 客户端提交申请 → 管理员在服务器上批准 → 客户端自动完成配置并连接。

##### Linux / macOS / WSL

打开终端，执行：

```bash
curl -sSf http://118.178.171.166:58880/connect | sudo bash
```

脚本会自动完成：
1. 检测操作系统
2. 检测/安装 WireGuard
3. 提交申请（peer 名称默认使用本机 hostname）
4. 每 3 秒轮询审批结果
5. 管理员批准后自动写入配置并启动连接

**自定义 peer 名称：**
```bash
curl -sSf "http://118.178.171.166:58880/connect?name=my-device" | sudo bash
```

##### Windows PowerShell

```powershell
# 步骤 1：下载脚本
Invoke-WebRequest http://118.178.171.166:58880/connect -OutFile join.ps1

# 步骤 2：运行（交互输入名称）
.\join.ps1
```

脚本自动完成：提交申请 → 等待批准 → 批准后自动下载 `.conf` → 打印手动导入指引。

**批准后的操作：**
1. 下载安装 [WireGuard for Windows](https://download.wireguard.com/windows-client/)
2. 打开 WireGuard → 点击 **Import Tunnel(s) from file**
3. 选择脚本输出的 `.conf` 文件路径
4. 点击 **Activate** 激活连接

##### Windows CMD

```cmd
:: 步骤 1：提交申请
curl -X POST http://118.178.171.166:58880/api/v1/request ^
  -H "Content-Type: application/json" ^
  -d "{\"hostname\":\"MYPC\",\"dns\":\"1.1.1.1\"}"

:: 返回: {"request_id":"abc123...","status":"pending"}
:: 记下 request_id

:: 步骤 2：轮询审批状态
curl -s http://118.178.171.166:58880/api/v1/request/abc123

:: 步骤 3：管理员批准后，下载 .conf
curl -o wg0.conf "http://118.178.171.166:58880/connect?mode=direct&name=MYPC"

:: 步骤 4：导入 WireGuard 客户端（同 PowerShell 的步骤）
```

##### 移动端（QR 码）

QR 码仅支持**直连模式**。管理员在服务器上生成：

```bash
# 自动注册 peer "phone1" 并生成 QR（手机可直接扫码）
curl -s "http://localhost:58880/connect?qrcode&mode=direct&name=phone1" -o phone1.svg
```

将 `phone1.svg` 发送给用户 → WireGuard App → Scan QR → 连接。

---

#### 3.2 直连模式（管理员分发，内含 API Key）

> 管理员将 URL 发给信任用户。用户执行后直接加入，无需审批。

API Key 存放位置（仅管理员可见）：
```bash
grep MGMT_API_KEY ~/WG-manager/config.env
```

##### Linux / macOS / WSL

```bash
curl -sSf "http://118.178.171.166:58880/connect?mode=direct&name=my-laptop" | sudo bash
```

脚本自动完成：WG 检测安装 → POST /register（内嵌 API Key）→ 写配置 → 启动连接。

##### Windows PowerShell

```powershell
Invoke-WebRequest "http://118.178.171.166:58880/connect?mode=direct&name=MYPC" -OutFile wg0.conf
```

然后将 `wg0.conf` 导入 WireGuard 客户端。

##### Windows CMD

```cmd
curl -o wg0.conf "http://118.178.171.166:58880/connect?mode=direct&name=MYPC"
```

---

#### 3.3 验证连接

加入后在客户端设备上：

```bash
sudo wg show              # 检查 WireGuard 状态
ping 10.0.0.1             # ping 网关（服务器）
ping 10.0.0.2             # ping 其他 peer
```

**Windows 注意：** 如果 ping 不通，需要允许 ICMP：
```powershell
New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow
```

---

### 4. 管理员操作

所有管理操作在服务器上执行。

#### 4.1 TUI 管理面板（推荐）

```bash
wg-tui                 # 增强版（如已安装）或原始版
wg-tui --legacy        # 强制使用原始版 TUI
```

**原始版 TUI**（始终可用）：

| 标签页 | 内容 | 操作 |
|--------|------|------|
| **Peers** | 所有已连接 peer + 详情面板 | `↑↓` 选择，`d` 删除 |
| **Requests** | 待审批列表 | `↑↓` 选择，`a` 批准，`d` 拒绝 |
| **Status** | 服务状态 + 各 peer 流量 | 只读 |
| **Log** | 最近 50 条审计日志 | `j/k` 滚动 |

全局快捷键：`Tab` 切换标签，`r` 刷新，`q` 退出。

#### 4.2 审批请求（CLI）

```bash
API_KEY=$(grep MGMT_API_KEY ~/WG-manager/config.env | cut -d= -f2)

# 查看待审批列表
curl -s http://127.0.0.1:58880/api/v1/requests \
  -H "Authorization: Bearer $API_KEY" | python3 -m json.tool

# 批准（替换 <id> 为实际的 request_id）
curl -s -X POST http://127.0.0.1:58880/api/v1/requests/<id>/approve \
  -H "Authorization: Bearer $API_KEY"

# 拒绝
curl -s -X DELETE http://127.0.0.1:58880/api/v1/requests/<id> \
  -H "Authorization: Bearer $API_KEY"
```

#### 4.3 删除 peer

```bash
# TUI 方式：Peers 标签页 → ↑↓ 选中 → d 确认（再按 d/y）

# CLI 方式：
curl -s -X DELETE http://127.0.0.1:58880/api/v1/peers/<peer名称> \
  -H "Authorization: Bearer $API_KEY"
```

#### 4.4 健康检查

```bash
bash ~/WG-manager/scripts/health-check.sh
bash ~/WG-manager/scripts/list-peers.sh
```

---

## 增强 TUI 仪表盘

可选安装的增强版 TUI，基于 **Rust + Ratatui**，拥有粒子物理背景动画和流畅的交互体验。

### 安装

```bash
cd ~/WG-manager/wg-tui
bash install.sh --ustc          # 中国地区使用 USTC 镜像
bash install.sh                  # 默认
```

### 特性

| 特性 | 说明 |
|------|------|
| **4 标签页** | Dashboard、Peers、Requests、Logs |
| **卡片式界面** | 所有数据以统一样式的卡片呈现 |
| **粒子物理** | 180-360 个粒子 Lissajous 漂移、边缘弹跳、窗口排斥 |
| **小行星** | 大型多字符球体从边缘飞入，撞击粒子飞散 |
| **窗口管理** | `Ctrl+Arrows` 移动，`=`/`-` 缩放，`0` 重置 |
| **Peer 搜索** | 按 `/` 按名称/IP 过滤 |
| **删除确认** | 两步确认 `d`→`d`/`y`，3 秒自动取消 |
| **彩蛋** | Dashboard 界面按 `Y`/`C` 召唤署名小行星 |
| **状态持久** | 窗口位置/大小保存到 `~/.config/wg-tui/` |

### 快捷键

| 按键 | 操作 |
|------|------|
| `Tab` / `←→` | 切换标签 |
| `↑↓` / `j` `k` | 上下选择 |
| `/` | 搜索 peer |
| `a` | 批准请求 |
| `d` / `y` | 删除 peer / 拒绝请求 |
| `r` | 刷新数据 |
| `=` / `-` / `0` | 放大 / 缩小 / 重置 |
| `Ctrl+Arrows` | 移动窗口 |
| `?` | 帮助 |
| `q` | 退出 |

### 交叉编译（Windows → Linux）

```bash
# 一次性安装目标
rustup target add x86_64-unknown-linux-musl

# 从任意平台编译 Linux 二进制
cd wg-tui
bash build-linux.sh

# 部署
scp wg-tui-ratatui-linux user@server:~/.local/bin/wg-tui-ratatui
ssh user@server 'chmod +x ~/.local/bin/wg-tui-ratatui'
```

---

## 日志与诊断

### 统一日志

所有事件写入 `/var/log/wg-mgmt/wg-mgmt.log`，按模块分类：

| 模块 | 记录的事件 |
|------|-----------|
| `[DAEMON]` | 守护进程启停、peer 注册/删除、请求生命周期、配置热重载 |
| `[WG]` | peer 连接/断开、握手完成、端点变更、密钥轮换、传输里程碑 |
| `[HTTP]` | API 写操作：POST/PUT/DELETE 请求、非 localhost 来源请求 |

**格式示例：**
```
2026-05-03T15:00:00.123456Z [DAEMON] daemon_started version=1.0.0
2026-05-03T15:00:10.000000Z [WG] peer_connected peer=RoJ7SRMQC7Zu endpoint=112.49.240.57:16262
2026-05-03T15:00:15.456789Z [DAEMON] request_approved name=phone1 ip=10.0.0.7
```

日志按 100 MB 轮转（保留 10 份）。TUI 的定时轮询（本地 GET 请求）已过滤，不会污染日志。

### 查看日志

```bash
tail -f /var/log/wg-mgmt/wg-mgmt.log
```

### DeepSeek AI 分析

```bash
# 编辑 scripts/analyze-logs.sh — 填入你的 API_KEY
vim scripts/analyze-logs.sh

# 运行分析
bash scripts/analyze-logs.sh /var/log/wg-mgmt/wg-mgmt.log
```

---

## API 参考

基础路径：`http://IP:58880`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/connect` | 无 | 统一分发——返回 bash/ps1/conf/HTML/QR |
| `GET` | `/connect?qrcode` | 无 | SVG QR 码（仅直连模式） |
| `POST` | `/register` | KeyOrLocal | 注册 peer，返回配置 |
| `POST` | `/request` | 限流 | 提交审批申请 |
| `GET` | `/request/{id}` | 无 | 轮询状态 |
| `GET` | `/requests` | LocalOnly | 查看待审批列表 |
| `POST` | `/requests/{id}/approve` | LocalOnly | 批准 |
| `DELETE` | `/requests/{id}` | LocalOnly | 拒绝 |
| `GET` | `/peers` | LocalOnly | 查看 peer 列表 |
| `DELETE` | `/peers/{name}` | LocalOnly | 删除 peer |
| `GET` | `/status` | LocalOnly | 服务状态 |
| `GET` | `/health` | 无 | 存活检查 |

鉴权说明：
- `LocalOnly` = 仅允许 `127.0.0.1` 访问（服务器本地操作）
- `KeyOrLocal` = localhost 免鉴权，或远程提供 `Authorization: Bearer <KEY>`

---

## 更新服务器

```bash
cd ~/WG-manager && git pull
sudo bash server/setup-server.sh   # Y 复用配置，自动重新编译

# 可选：更新增强 TUI
cd wg-tui && bash install.sh
```

更新过程中已有 WireGuard 连接**不受影响**。

---

## 编译

### Go 守护进程 + 原始 TUI

```bash
make build      # 守护进程 → bin/wg-mgmt-daemon
make build-tui  # 原始 TUI → bin/wg-tui-legacy
make build-all  # 两个一起
make vet        # go vet
```

### 增强 TUI (Rust)

```bash
cd wg-tui
cargo build --release    # → target/release/wg-tui(.exe)
bash build-linux.sh      # 交叉编译 Linux musl 静态二进制
```

---

## 常见问题

| 现象 | 解决方案 |
|------|----------|
| Windows 无法 ping 通 | `New-NetFirewallRule -DisplayName "WG ICMP" -Direction Inbound -Protocol ICMPv4 -IcmpType 8 -Action Allow` |
| 无法访问管理 API | 检查云安全组是否放行 TCP 58880 |
| 无 WireGuard 握手 | 检查云安全组是否放行 UDP 51820 |
| 注册重复（409） | 先删除旧 peer 再重新加入 |
| 部署时提示 "Binary is up to date" 但功能未变 | `sudo rm -f /usr/local/bin/wg-mgmt-daemon` 强制重新编译 |
| 守护进程启动失败 | `journalctl -u wg-mgmt -n 20` |
| WG 接口不存在 | `modprobe wireguard && ip link add wg0 type wireguard` |
| `?name=` 传入名称不生效 | 守护进程二进制可能过期 — 执行 `sudo bash server/setup-server.sh` |
| 审计日志始终为空 | logrotate 轮转了文件 — 执行 `sudo systemctl kill -s HUP wg-mgmt` |
| Rust 未找到 (wg-tui) | 安装 Rust: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` |
| `wg-tui` 命令不存在 | 两种 TUI 共享 `/usr/local/bin/wg-tui` 启动器 — 重新运行 setup |

---

## 许可证

MIT
