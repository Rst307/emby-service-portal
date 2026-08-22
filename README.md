# Emby Service Portal

面向 Emby 服务器的自助服务门户与订阅管理平台。它是一个单进程 Go 应用，使用 SQLite（WAL）保存业务账号、邀请码、会话和加密凭据，提供用户自助门户（激活码购买、订阅续费、状态查询）、管理后台和 REST API。

> 适用于可信管理员管理的单服务器部署；生产环境应部署在 HTTPS 反向代理之后。

## 功能

- 管理员登录、CSRF 防护、会话管理和管理后台。
- 创建、编辑、启用、禁用、删除 Emby 用户；自动限制受管账号的媒体功能。
- 业务账号订阅期、到期自动禁用及 Emby 状态同步重试。
- 邀请码创建、禁用、删除、公开注册和续费；后台记录并展示每次兑换的业务账号与时间。
- Portal 用户中心：查询订阅状态、直接进入激活码购买和订阅续费；已登录用户续费时无需重复输入用户名和密码，不显示或导出明文密码。
- 独立凭据主密钥加密受管密码；支持主密钥轮换与旧版 API-Key 密文兼容读取。
- 注册/账号创建持久化 Saga：Emby 创建成功而本地写入失败时可恢复。
- `Idempotency-Key` 防止 API 创建账号或注册时的重复请求。
- 业务账号乐观锁版本控制，避免续费、管理员操作和到期任务相互覆盖。
- 内建登录、注册和续费限流；安全响应头、请求体限制、缓存保护。
- 可配置的显示时区（默认上海，后台「设置」页随时切换），时间展示统一按所选时区转换，存储始终 UTC。
- 对接 `wxpay-payment-center`：管理员可配置支付中心商户凭证，维护不限数量的激活码购买方案与订阅续费方案。
- 支付成功后自动生成一次性激活码，或为已验证的 Emby 用户完成订阅续费；支付回调验签、订单金额快照和重复回调幂等处理均在本地完成。
- 用户中心「求剧」：登录用户可搜索 TMDB 影视库，页面按 TMDB 编号对照当前 Emby 库存标记「已在库 / 可求剧」；未入库的影视可一键提交求剧，管理员在「求剧管理」查看剧名、TMDB 编号、求剧用户与时间，并标记已入库或驳回。需要 `ESP_TMDB_API_KEY`。
- 自更新：后台自动检测 GitHub Releases 新版本（可配置镜像源），管理后台「系统设置 → 系统更新」一键检测/安装，安装前强制 SHA-256 校验，安装后自动重启；可开启自动更新，发现新版本即自动下载安装并重启。

## 快速开始

### 前置条件

- Go 1.26.5 或更新版本（仅源码运行/构建时需要）。
- 一个可访问的 Emby 服务器及管理员 API Key。
- 生产环境建议使用 Linux、systemd 和 Caddy/Nginx/OpenResty HTTPS 反向代理。

### 本地运行

```bash
cp .env.example .env
# 编辑 .env，并将 ESP_* 变量导出到当前 shell。
go run ./cmd/emby-service-portal
```

程序不会自动加载 `.env`，这是为了让 systemd、容器平台等生产环境成为唯一的配置来源。启动后访问：

- 管理员登录：`/admin/login`
- 用户中心：`/portal/login`
- 用户求剧：`/portal/request`（需登录）
- 管理员求剧管理：`/admin/requests`
- 邀请码注册：`/register`
- 邀请码续费：`/renew`
- 健康检查：`/healthz`
- 激活码购买：`/purchase`
- 订单支付页：`/payment/{token}`（由购买流程跳转）
- 管理员订单查询：`/admin/orders`（支持按订单号、商品、购买人和业务账号筛选）

### 微信支付中心对接

支付中心项目见 [wxpay-payment-center](https://github.com/Rst307/wxpay-payment-center)，在线文档见 `https://pay.rst307.cn/api-docs`。先在支付中心管理员页面创建一个商户应用，并将返回的 `app_id`、`secret` 和本项目的回调地址配置到本项目管理员后台「设置 → 微信支付中心」：

- 支付中心地址：支付中心的 HTTPS 根地址。
- 应用 ID / Secret：支付中心商户应用凭证；Secret 在本地使用凭据主密钥加密保存，不会渲染到页面。
- 回调地址：填写 `https://你的域名/webhooks/wxpay-payment-center`，并在支付中心应用中使用完全相同的地址。
- 支付后跳转地址（可选）：例如 `https://你的域名/payment/{token}`。支付中心约 3 秒后将用户带回；`{token}`、`{order_no}` 和 `{merchant_order_no}` 会替换为当前订单值。必须是 HTTPS 完整地址，最多 2048 个字符。
- 订单有效期：支付中心订单的有效分钟数，范围 1–1440。

### 求剧功能（可选）

在 TMDB（https://www.themoviedb.org）申请一个 API Key/读写令牌，配置为环境变量 `ESP_TMDB_API_KEY` 后，登录用户中心的用户即可在「求剧」页搜索影视（返回 TMDB 结果）。页面会通过 Emby 的 `AnyProviderIdEquals` 按 TMDB 编号实时对照当前库存：**电影**已在库显示「已在库」且不能求剧，未存在的可一键提交；**电视剧/动漫**若全集都在库显示「已在库」，若在库但缺集（对已播出但未入库的集次）会在卡片上标明「缺 N 集」并提供「催更」，提交后会快照缺失的集次清单。管理员在「求剧管理」页面查看每条记录（剧名、原始片名、TMDB 编号、类型——求整部/催更、缺失集清单、求剧用户、求剧时间、状态），入库后标记「已入库」，无法提供的可标记「已驳回」（用户可重新求剧）或删除。未配置该变量时，求剧搜索不可用，但历史求剧记录仍可在后台管理。

管理员在「售卖方案」页面分别添加激活码方案和订阅续费方案，可以添加任意数量。激活码购买页允许用户填写购买人或联系方式，管理员可在「支付订单」中查询；续费订单自动记录被续费的业务账号，同时会将购买人/业务账号作为 R Pay 的订单备注，方便在支付中心后台核对。价格以人民币元填写，服务端以整数分保存；修改方案不会改变已创建订单的价格和天数快照。生产环境必须使用 HTTPS，支付回调不能依赖浏览器 Cookie 或 CSRF，而是依赖支付中心 HMAC 签名。

## 配置

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `ESP_LISTEN_ADDR` | 否 | HTTP 监听地址，默认 `:8080`；生产反代时使用 `127.0.0.1:8081`。 |
| `ESP_DATABASE_PATH` | 是 | SQLite 数据库路径。 |
| `ESP_EMBY_BASE_URL` | 是 | Emby 基础 URL，可带 `/emby`；远程地址必须使用 HTTPS。本机开发可用 `http://127.0.0.1:8096/emby`。 |
| `ESP_EMBY_API_KEY` | 是 | Emby 管理员 API Key。 |
| `ESP_API_KEY` | 是 | 外部 REST API 的 `X-API-Key`。 |
| `ESP_CREDENTIAL_MASTER_KEY` | 是 | 至少 32 字符、独立于 API Key 的凭据加密密钥。 |
| `ESP_CREDENTIAL_PREVIOUS_MASTER_KEY` | 否 | 轮换主密钥时填入上一把主密钥。首次部署留空。 |
| `ESP_ADMIN_USERNAME` | 是 | 首次启动时创建的管理员用户名。 |
| `ESP_ADMIN_PASSWORD` | 是 | 首次启动时创建的管理员密码；不会覆盖已有管理员。 |
| `ESP_COOKIE_SECURE` | 否 | HTTPS 生产环境设为 `true`；仅可信局域网的直接 HTTP 可设为 `false`。 |
| `ESP_SESSION_TTL` | 否 | 会话有效期，默认 `24h`。 |
| `ESP_TIME_ZONE` | 否 | 初始显示时区（首次启动写入设置，之后可在后台「设置」页随时切换），默认 `Asia/Shanghai`。所有页面展示的时间都会按此转换；数据库和 API 仍以 UTC/RFC3339 保存和返回。 |
| `ESP_TMDB_API_KEY` | 否 | TMDB API Key，启用用户中心「求剧」功能；留空则求剧搜索不可用（后台仍可管理历史求剧记录）。 |
| `ESP_TMDB_BASE_URL` | 否 | TMDB API 根地址（镜像/反代），默认 `https://api.themoviedb.org/3`。中国大陆网络下直连官方 API 常被限速或阻断，可指向可达的 TMDB API 镜像或自建反代（值需包含镜像实际服务的 `/3` 路径段）。设置后请求**先走镜像，镜像失败自动回退到官方端点**（经 `ESP_TMDB_HTTP_PROXY` 或环境代理）。 |
| `ESP_TMDB_IMAGE_BASE_URL` | 否 | TMDB 海报 CDN 根地址，默认 `https://image.tmdb.org/t/p/w342`。「求剧」页海报由浏览器直接加载，官方图片 CDN 在国内同样难以访问，可指向可达的图片镜像/反代。 |
| `ESP_TMDB_HTTP_PROXY` | 否 | TMDB API 请求使用的 HTTP(S)/SOCKS5 代理（服务端出网）。不设置时使用进程环境变量 `HTTP(S)_PROXY`；设置了则强制走该代理。 |
| `ESP_TMDB_TIMEOUT` | 否 | 每次 TMDB API 请求超时，默认 `10s`。链路慢时可适当调大（如 `15s`），避免慢镜像被误报为“无结果”。 |

#### 中国大陆网络加速求剧访问

直连 `api.themoviedb.org`（搜索）与 `image.tmdb.org`（海报）在国内通常很慢且不稳定。**方案 A（镜像）与方案 B（代理）可以组合使用**，两者不冲突：配了镜像后求剧搜索先走镜像，镜像超时/失败时自动回退到“经代理访问官方端点”，互为兜底：

- **方案 A（指向镜像/反代）**：`ESP_TMDB_BASE_URL=https://tmdb-api.mirror.example.com/3`、`ESP_TMDB_IMAGE_BASE_URL=https://tmdb-image.mirror.example.com/t/p/w342`。镜像需与 TMDB v3 API、图片路径兼容。
- **方案 B（代理出网）**：`ESP_TMDB_HTTP_PROXY=http://127.0.0.1:7890`（HTTP 代理）或 `socks5://127.0.0.1:1080`，服务端通过代理访问官方 API，也是镜像失效时的兜底路径。注意海报是**浏览器**直接加载的：代理只解决服务端搜索，海报仍需镜像 `ESP_TMDB_IMAGE_BASE_URL` 或用户侧代理。
- **方案 C（调大超时）**：`ESP_TMDB_TIMEOUT=15s` 避免慢链路被 10 秒默认超时截断返回空结果。

生成随机密钥：

```bash
openssl rand -base64 48
```

### 密钥升级与轮换

旧版本使用 `ESP_API_KEY` 派生密码加密密钥。升级时设置新的 `ESP_CREDENTIAL_MASTER_KEY` 后，旧密文仍可通过现有 API Key 读取。**不要在确认所有旧凭据已更新前轮换 API Key。**

轮换凭据主密钥时：

```env
ESP_CREDENTIAL_MASTER_KEY=新的主密钥
ESP_CREDENTIAL_PREVIOUS_MASTER_KEY=旧的主密钥
```

新写入会使用新主密钥；旧密文仍可读取。

## Linux / systemd 部署

每次代码推送到 `main` 后，GitHub Actions 会自动生成静态 Linux amd64 与 Windows amd64 可执行文件，并创建一个带构建编号的 GitHub 预发布 Release。直接在仓库的 [Releases](https://github.com/Rst307/emby-service-portal/releases) 页面下载最新版本；每个 Release 同时包含可执行文件和对应的 SHA-256 校验文件。Pull Request 只执行检查，不会发布 Release；也可以在 Actions 页面通过 **Run workflow** 手动构建并发布。
### Docker 运行

镜像为多阶段构建的静态二进制 + Alpine 运行层（含 CA 证书和 IANA 时区数据），以非 root 用户运行：

```bash
docker build -t emby-service-portal .
docker run -d --name emby-service-portal \
  -p 127.0.0.1:8081:8080 \
  -e ESP_DATABASE_PATH=/data/emby-service-portal.db \
  -e ESP_EMBY_BASE_URL=https://emby.example.com/emby \
  -e ESP_EMBY_API_KEY=... \
  -e ESP_API_KEY=... \
  -e ESP_CREDENTIAL_MASTER_KEY=... \
  -e ESP_ADMIN_USERNAME=admin \
  -e ESP_ADMIN_PASSWORD=... \
  -v eum-data:/data \
  emby-service-portal
```

数据库放在挂载卷 `/data` 下以便备份；同样建议只暴露到本机反向代理。

### systemd 部署

将二进制、`.env` 和 `scripts/install-linux-service.sh` 放到服务器。脚本会创建专用非 root 用户、保护数据库目录并安装 systemd 沙箱：

```bash
sudo mkdir -p /opt/emby-service-portal
sudo install -m 0755 emby-service-portal-linux-amd64 /opt/emby-service-portal/
sudo install -m 0600 .env /opt/emby-service-portal/.env
sudo bash scripts/install-linux-service.sh
sudo systemctl status emby-service-portal
```

反向代理到 `127.0.0.1:8081` 并启用 HTTPS。不要让应用端口直接暴露到不可信网络。

## 自更新（系统设置 → 系统更新）

程序内置更新检测与安装：后台周期性查询 GitHub Releases，找出非草稿的最新发布（含预发布构建，本项目每合并一次 `main` 就发布一个），按平台匹配安装包（`linux-amd64` / `windows-amd64.exe`），下载时先与 Release 附带的 `.sha256` 校验和比对，一致后才替换程序并重启。

- **检测**：默认每 6 小时一次（`ESP_UPDATE_INTERVAL`），也可在后台点「立即检测」。检测结果缓存在内存中，管理后台显示当前版本、最新版本、发布时间与更新说明。
- **手动更新**：后台「立即更新」按钮下载 → 校验 → 替换 → 自动重启，期间服务中断约 5–10 秒。
- **自动更新**：后台勾选「自动更新」（或首次运行设置 `ESP_UPDATE_AUTO=true`），发现新版本即自动完成上面的流程。
- **重启机制**：Linux 在进程内完成二进制替换后以退出码 17 退出，由 systemd（`Restart=on-failure`）或其他守护进程拉起新版本；Windows 由程序生成的独立更新助手脚本在旧进程退出后完成替换并重新启动。
- **安全**：安装前强制校验 SHA-256；校验失败或发布缺少校验文件时中止更新。
- **受限环境**：程序目录不可写时（如 Docker 镜像内 `/usr/local/bin`、未授权的 systemd 沙箱），更新会明确报错，建议改用重建镜像或手动替换的方式。Linux systemd 部署请使用更新过的 `scripts/install-linux-service.sh`（已开放程序目录写权限用于自更新）。Docker 部署可通过 `ESP_UPDATE_INTERVAL=0` 完全关闭检测。

中国大陆网络无法直连 GitHub 时，可配置镜像源：

```bash
# 例：API 走镜像，安装包仍走官方源（或同时配置下载代理）
ESP_UPDATE_API_BASE=https://api.github.com
ESP_UPDATE_DOWNLOAD_BASE=
```

其余变量（`ESP_UPDATE_API_BASE`、`ESP_UPDATE_DOWNLOAD_BASE`、`ESP_UPDATE_INTERVAL`、`ESP_UPDATE_AUTO`）说明见 [.env.example](.env.example)。

## 外部 API

除 `GET /api/v1/health` 外，管理 API 都需要：

```http
X-API-Key: <ESP_API_KEY>
```

创建业务账号和邀请码注册还必须带唯一、可重试的请求键：

```http
Idempotency-Key: <随机请求标识>
```

重复相同键和相同请求会返回原来的业务结果；以相同键提交不同请求会返回 `409 Conflict`。账号更新、启用和禁用请求必须带上 API 返回的 `version`，版本冲突也返回 `409 Conflict`。

完整端点、请求示例和字段说明见 [docs/api.md](docs/api.md)。

## 安全与运维

- 不要把 `.env`、数据库、密钥或生产日志提交到 Git。
- 立即轮换任何被粘贴到聊天、工单或提交记录中的 API Key、Emby Key、管理员密码。
- Portal 和管理端响应禁止浏览器缓存；页面不显示已保存的 Emby 密码。
- 到期、启用、禁用状态变更先持久化，再由可重试 outbox 同步 Emby。
- 定期备份 SQLite 文件。升级前先停止服务或使用 SQLite 在线备份工具。

示例停止服务后的备份：

```bash
sudo systemctl stop emby-service-portal
sudo cp /opt/emby-service-portal/data/emby-service-portal.db /root/emby-service-portal-$(date +%F).db
sudo systemctl start emby-service-portal
```

查看运行日志：

```bash
sudo journalctl -u emby-service-portal -f
```

## 开发与质量检查

```bash
make fmt      # 格式化
make test     # 单元与验收测试
make vet      # 静态检查
make race     # 竞态检测
make check    # 完整质量门禁（等价 CI 前四步）
make build    # 本机二进制（内嵌 git 版本）
make linux    # 静态 Linux amd64 二进制
make windows  # 静态 Windows amd64 二进制
```

GitHub Actions 会运行格式检查、依赖校验、测试、race 检测、覆盖率、`staticcheck`、`govulncheck`，并为每个 `main` 推送发布带版本号的 Linux/Windows 构建产物。

## 项目结构

```text
cmd/emby-service-portal/    程序入口
internal/
├── app/                  模块装配（组合根）
├── accounts/             业务账号生命周期与注册 Saga
├── auth/                 管理员认证与会话
├── buildinfo/            构建版本信息（-ldflags 注入）
├── config/               环境变量配置
├── credentials/          加密凭据 Vault
├── domain/               领域模型（实体、领域错误、输入/查询结构体）
├── emby/                 Emby HTTP 客户端
├── settings/             运行时设置（时区、自动更新开关）
├── update/               自更新：Release 检测、SHA-256 校验、安装与重启
├── tmdb/                 TMDB 客户端（求剧搜索与海报）
├── expiry/               到期和 Emby 同步 worker
├── invites/              邀请码及兑换
├── paymentcenter/        R Pay 支付中心 HTTP 适配器
├── payments/             售卖方案、支付订单与履约
├── persistence/sqlite/   SQLite 适配器（连接、迁移与按领域拆分的仓储实现）
│   ├── sqlite.go         连接/迁移/共享工具（outbox 写入）
│   ├── admins.go         管理员、会话与设置存取
│   ├── accounts.go       业务账号存取与乐观锁
│   ├── saga.go           创建/注册持久化 Saga
│   ├── invites.go        邀请码与兑换存取
│   ├── requests.go       求剧记录存取
│   └── sync.go           Emby 访问同步 outbox
├── portal/               用户中心
├── requests/             求剧（TMDB 搜索与求剧记录）
├── ratelimit/            登录/注册/续费限流
└── web/                  HTTP 层（按页面域拆分）
    ├── web.go            Server、路由、中间件与共享助手
    ├── api.go            REST API（/api/v1/*）
    ├── public.go         公开门户页（/、/register、/renew、结果页）
    ├── portal.go         用户中心（/portal/*）
    ├── requests.go      求剧页与求剧管理（/portal/request、/admin/requests）
    ├── payment.go        购买与支付（/purchase、/payment/*、回调）
    ├── admin_login.go    管理员登录与登出
    ├── admin_dashboard.go 工作台
    ├── admin_accounts.go 账号管理
    ├── admin_invites.go  邀请码管理
    ├── admin_plans.go    售卖方案
    ├── admin_orders.go   支付订单
    ├── admin_settings.go 系统设置
    └── admin/            后台 HTML 模板scripts/                  安装脚本
docs/                     API 文档与架构决策
Dockerfile                多阶段容器构建
Makefile                  常用开发命令
```

## 许可证

[MIT](LICENSE)。
