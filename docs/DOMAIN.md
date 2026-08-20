# 领域模型

本文档记录模块职责、数据归属、依赖方向与公开接口。术语（ubiquitous language）见 `CONTEXT.md`；架构决策见 `docs/adr/`。

## 模块职责

| 模块 | 职责 | 拥有的数据 |
| --- | --- | --- |
| `accounts` | 业务账号生命周期：创建/更新/启用/禁用/删除、注册与创建 Saga、密码校验与加密存储、批量操作 | `accounts`（业务账号 + 乐观锁 version）、`account_credentials`（加密凭据）、`account_create_operations`（Saga 状态） |
| `invites` | 邀请码发行与兑换（注册/续费）、兑换记录 | `invite_codes`、`invite_redemptions` |
| `payments` | 售卖方案、支付订单、支付中心对账与履约、支付设置 | `payment_plans`、`payment_orders`、`payment_events`、`settings` 中的支付中心配置 |
| `auth` | 管理员身份与后台会话 | `admins`、`sessions` |
| `portal` | 用户中心会话；密码直接向 Emby 验证，不落盘 | `user_sessions` |
| `requests` | 求剧：TMDB 搜索（返回 IMDb/TMDB 结果）、对照 Emby 库存标记、提交/管理求剧记录 | `media_requests` |
| `expiry` | worker：到期标记 + Emby 访问策略同步（outbox 消费方） | 只读 `accounts`；写 `emby_access_sync_jobs`（outbox） |
| `settings` | 运行时设置（显示时区等） | `settings` |
| `credentials` | 凭据主密钥加密/解密/轮换（Vault） | `account_credentials` 的加解密契约 |
| `emby` / `paymentcenter` | 外部 HTTP 适配器 | 无 |

## 数据归属

| 数据 | 唯一写者 | 允许读 | 说明 |
| --- | --- | --- | --- |
| `accounts` | `accounts.Service`（经 sqlite） | `portal`、`payments`（经服务方法或已登记例外）、`expiry`（经 store） | `payments` 的 `FindAccountByUsername` 直读属于已登记例外（ADR-0003） |
| `invite_codes` / `invite_redemptions` | `invites.Service` | `payments`（仅生成激活码辅助函数） | 支付履约生成的一次性激活码也是邀请码 |
| `payment_plans` / `payment_orders` / `payment_events` | `payments.Service` | 管理后台只读 | 方案快照进订单，改方案不影响已建订单 |
| `sessions` / `user_sessions` | `auth` / `portal` | 各自会话校验 | 两类会话生命周期独立 |
| `media_requests` | `requests.Service` | 管理后台只读；`portal` 经服务方法 | 同一业务账号对同一 TMDB 标题只保留一条记录；驳回后可重新激活 |
| `emby_access_sync_jobs` | 所有触发状态变更的模块经 `upsertAccessSyncJob` | `expiry` | 消费方唯一 |

## 依赖规则

- 服务可依赖：`domain`、其他服务的公开方法、`credentials`、`emby`/`paymentcenter` 适配器、sqlite 仓储。
- 服务不得依赖：`web`、其他服务的仓储内部结构。
- `domain` 是唯一允许被 `web` 层引用其类型（实体/查询结构体）的模型来源。
- 新跨域读写必须经公开服务方法；确需直读时在 ADR 中登记。

## 公开接口（服务方法）

| 服务 | 公开能力 |
| --- | --- |
| `accounts` | `Create` / `CreateIdempotent` / `RegisterIdempotent` / `Update` / `Enable` / `Disable` / `Delete` / `Batch` / `VerifyPassword` / `Password` / `Get` / `List` / `SyncFromEmby` / `RestrictAllMediaFeatures` / `RecoverAccountCreates` |
| `invites` | `Create` / `List` / `SetEnabled` / `Delete` / `Register` / `RegisterIdempotent` / `Renew` / `RenewForAccount`；包级辅助 `NewActivationCode` / `HashCode`（供 payments 生成激活码） |
| `payments` | `CreateActivationOrder` / `CreateRenewalOrder` / `CreateRenewalOrderForAccount` / `HandleWebhook` / `Reconcile` / 方案 CRUD 与启停 / `ListOrders` / `Settings` / `UpdateSettings` |
| `auth` | `BootstrapAdmin` / `Login` / `Authenticated` / `Logout` |
| `portal` | `Login` / `Account` / `Logout` |
| `requests` | `Search`（TMDB 结果 + Emby 库存/本人求剧标记）/ `Create`（服务端回查 TMDB 与库存后落单）/ `List` / `SetStatus` / `Delete` |
| `settings` | `DisplayTimeZone` / `SetDisplayTimeZone` / `Ensure` |

## 领域模型文件

`internal/domain` 按业务域拆分：`account.go`（业务账号、outbox 作业）、`admin.go`（管理员、两类会话）、`invite.go`（邀请码、兑换）、`saga.go`（创建/注册 Saga）、`payment.go`（方案、订单、事件）。该包零依赖，仅标准库。
