# Emby User Manager

面向单个 Emby 服务器的轻量级用户与订阅期管理工具。它是一个单进程 Go 应用，使用 SQLite（WAL）保存业务账号、邀请码、会话和加密凭据，并提供管理后台、用户中心和 REST API。

> 适用于可信管理员管理的单服务器部署；生产环境应部署在 HTTPS 反向代理之后。

## 功能

- 管理员登录、CSRF 防护、会话管理和管理后台。
- 创建、编辑、启用、禁用、删除 Emby 用户；自动限制受管账号的媒体功能。
- 业务账号订阅期、到期自动禁用及 Emby 状态同步重试。
- 邀请码创建、禁用、删除、公开注册和续费。
- Portal 用户中心：查询订阅状态和续费，不显示或导出明文密码。
- 独立凭据主密钥加密受管密码；支持主密钥轮换与旧版 API-Key 密文兼容读取。
- 注册/账号创建持久化 Saga：Emby 创建成功而本地写入失败时可恢复。
- `Idempotency-Key` 防止 API 创建账号或注册时的重复请求。
- 业务账号乐观锁版本控制，避免续费、管理员操作和到期任务相互覆盖。
- 内建登录、注册和续费限流；安全响应头、请求体限制、缓存保护。
- 可配置的显示时区（默认上海，后台「设置」页随时切换），时间展示统一按所选时区转换，存储始终 UTC。
- 对接 `wxpay-payment-center`：管理员可配置支付中心商户凭证，维护不限数量的激活码购买方案与订阅续费方案。
- 支付成功后自动生成一次性激活码，或为已验证的 Emby 用户完成订阅续费；支付回调验签、订单金额快照和重复回调幂等处理均在本地完成。

## 快速开始

### 前置条件

- Go 1.26.5 或更新版本（仅源码运行/构建时需要）。
- 一个可访问的 Emby 服务器及管理员 API Key。
- 生产环境建议使用 Linux、systemd 和 Caddy/Nginx/OpenResty HTTPS 反向代理。

### 本地运行

```bash
cp .env.example .env
# 编辑 .env，并将 EUM_* 变量导出到当前 shell。
go run ./cmd/emby-user-manager
```

程序不会自动加载 `.env`，这是为了让 systemd、容器平台等生产环境成为唯一的配置来源。启动后访问：

- 管理员登录：`/admin/login`
- 用户中心：`/portal/login`
- 邀请码注册：`/register`
- 邀请码续费：`/renew`
- 健康检查：`/healthz`
- 激活码购买：`/purchase`
- 订单支付页：`/payment/{token}`（由购买流程跳转）

### 微信支付中心对接

支付中心项目见 [wxpay-payment-center](https://github.com/Rst307/wxpay-payment-center)。先在支付中心管理员页面创建一个商户应用，并将返回的 `app_id`、`secret` 和本项目的回调地址配置到本项目管理员后台「设置 → 微信支付中心」：

- 支付中心地址：支付中心的 HTTPS 根地址。
- 应用 ID / Secret：支付中心商户应用凭证；Secret 在本地使用凭据主密钥加密保存，不会渲染到页面。
- 回调地址：填写 `https://你的域名/webhooks/wxpay-payment-center`，并在支付中心应用中使用完全相同的地址。
- 支付后跳转地址（可选）：例如 `https://你的域名/payment/{token}`。支付中心约 3 秒后将用户带回；`{token}`、`{order_no}` 和 `{merchant_order_no}` 会替换为当前订单值。必须是 HTTPS 完整地址，最多 2048 个字符。
- 订单有效期：支付中心订单的有效分钟数，范围 1–1440。

管理员在「售卖方案」页面分别添加激活码方案和订阅续费方案，可以添加任意数量。价格以人民币元填写，服务端以整数分保存；修改方案不会改变已创建订单的价格和天数快照。生产环境必须使用 HTTPS，支付回调不能依赖浏览器 Cookie 或 CSRF，而是依赖支付中心 HMAC 签名。

## 配置

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `EUM_LISTEN_ADDR` | 否 | HTTP 监听地址，默认 `:8080`；生产反代时使用 `127.0.0.1:8081`。 |
| `EUM_DATABASE_PATH` | 是 | SQLite 数据库路径。 |
| `EUM_EMBY_BASE_URL` | 是 | Emby 基础 URL，可带 `/emby`；远程地址必须使用 HTTPS。本机开发可用 `http://127.0.0.1:8096/emby`。 |
| `EUM_EMBY_API_KEY` | 是 | Emby 管理员 API Key。 |
| `EUM_API_KEY` | 是 | 外部 REST API 的 `X-API-Key`。 |
| `EUM_CREDENTIAL_MASTER_KEY` | 是 | 至少 32 字符、独立于 API Key 的凭据加密密钥。 |
| `EUM_CREDENTIAL_PREVIOUS_MASTER_KEY` | 否 | 轮换主密钥时填入上一把主密钥。首次部署留空。 |
| `EUM_ADMIN_USERNAME` | 是 | 首次启动时创建的管理员用户名。 |
| `EUM_ADMIN_PASSWORD` | 是 | 首次启动时创建的管理员密码；不会覆盖已有管理员。 |
| `EUM_COOKIE_SECURE` | 否 | HTTPS 生产环境设为 `true`；仅可信局域网的直接 HTTP 可设为 `false`。 |
| `EUM_SESSION_TTL` | 否 | 会话有效期，默认 `24h`。 |
| `EUM_TIME_ZONE` | 否 | 初始显示时区（首次启动写入设置，之后可在后台「设置」页随时切换），默认 `Asia/Shanghai`。所有页面展示的时间都会按此转换；数据库和 API 仍以 UTC/RFC3339 保存和返回。 |

生成随机密钥：

```bash
openssl rand -base64 48
```

### 密钥升级与轮换

旧版本使用 `EUM_API_KEY` 派生密码加密密钥。升级时设置新的 `EUM_CREDENTIAL_MASTER_KEY` 后，旧密文仍可通过现有 API Key 读取。**不要在确认所有旧凭据已更新前轮换 API Key。**

轮换凭据主密钥时：

```env
EUM_CREDENTIAL_MASTER_KEY=新的主密钥
EUM_CREDENTIAL_PREVIOUS_MASTER_KEY=旧的主密钥
```

新写入会使用新主密钥；旧密文仍可读取。

## Linux / systemd 部署

每次代码推送到 `main` 后，GitHub Actions 会自动生成静态 Linux amd64 可执行文件，并创建一个带构建编号的 GitHub 预发布 Release。直接在仓库的 [Releases](https://github.com/Rst307/embyUserManager/releases) 页面下载最新版本；每个 Release 同时包含可执行文件和 SHA-256 校验文件。Pull Request 只执行检查，不会发布 Release；也可以在 Actions 页面通过 **Run workflow** 手动构建并发布。

本地构建静态 Linux amd64 二进制：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -buildvcs=false -ldflags='-s -w' \
  -o dist/emby-user-manager-linux-amd64 ./cmd/emby-user-manager
```

### Docker 运行

镜像为多阶段构建的静态二进制 + Alpine 运行层（含 CA 证书和 IANA 时区数据），以非 root 用户运行：

```bash
docker build -t emby-user-manager .
docker run -d --name emby-user-manager \
  -p 127.0.0.1:8081:8080 \
  -e EUM_DATABASE_PATH=/data/emby-user-manager.db \
  -e EUM_EMBY_BASE_URL=https://emby.example.com/emby \
  -e EUM_EMBY_API_KEY=... \
  -e EUM_API_KEY=... \
  -e EUM_CREDENTIAL_MASTER_KEY=... \
  -e EUM_ADMIN_USERNAME=admin \
  -e EUM_ADMIN_PASSWORD=... \
  -v eum-data:/data \
  emby-user-manager
```

数据库放在挂载卷 `/data` 下以便备份；同样建议只暴露到本机反向代理。

### systemd 部署

将二进制、`.env` 和 `scripts/install-linux-service.sh` 放到服务器。脚本会创建专用非 root 用户、保护数据库目录并安装 systemd 沙箱：

```bash
sudo mkdir -p /opt/embyUserManager
sudo install -m 0755 emby-user-manager-linux-amd64 /opt/embyUserManager/
sudo install -m 0600 .env /opt/embyUserManager/.env
sudo bash scripts/install-linux-service.sh
sudo systemctl status emby-user-manager
```

反向代理到 `127.0.0.1:8081` 并启用 HTTPS。不要让应用端口直接暴露到不可信网络。

## 外部 API

除 `GET /api/v1/health` 外，管理 API 都需要：

```http
X-API-Key: <EUM_API_KEY>
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
sudo systemctl stop emby-user-manager
sudo cp /opt/embyUserManager/data/emby-user-manager.db /root/emby-user-manager-$(date +%F).db
sudo systemctl start emby-user-manager
```

查看运行日志：

```bash
sudo journalctl -u emby-user-manager -f
```

## 开发与质量检查

```bash
make fmt      # 格式化
make test     # 单元与验收测试
make vet      # 静态检查
make race     # 竞态检测
make check    # 完整质量门禁（等价 CI 前四步）
make build    # 本机二进制
make linux    # 静态 Linux amd64 二进制
```

GitHub Actions 会运行格式检查、依赖校验、测试、race 检测、覆盖率、`staticcheck`、`govulncheck`，并上传可下载的 Linux amd64 构建产物。

## 项目结构

```text
cmd/emby-user-manager/    程序入口
internal/
├── app/                  模块装配（组合根）
├── accounts/             业务账号生命周期与注册 Saga
├── auth/                 管理员认证与会话
├── config/               环境变量配置
├── credentials/          加密凭据 Vault
├── emby/                 Emby HTTP 客户端
├── expiry/               到期和 Emby 同步 worker
├── invites/              邀请码及兑换
├── persistence/sqlite/   SQLite 连接、迁移与按领域拆分的存储
│   ├── sqlite.go         连接/迁移/共享工具（outbox 写入）
│   ├── types.go          领域模型与错误
│   ├── admins.go         管理员、会话与设置
│   ├── accounts.go       业务账号存取与乐观锁
│   ├── saga.go           创建/注册持久化 Saga
│   ├── invites.go        邀请码与兑换
│   └── sync.go           Emby 访问同步 outbox
├── portal/               用户中心
├── ratelimit/            登录/注册/续费限流
├── settings/             显示时区设置
└── web/                  HTTP 层
    ├── web.go            Server、路由、中间件与共享助手
    ├── api.go            REST API（/api/v1/*）
    ├── admin.go          管理后台页面（/admin/*）
    ├── portal.go         用户中心与公开页（/portal/*、/register、/renew）
    └── admin/            后台 HTML 模板
scripts/                  安装脚本
docs/                     API 文档与架构决策
Dockerfile                多阶段容器构建
Makefile                  常用开发命令
```

## 许可证

[MIT](LICENSE)。
