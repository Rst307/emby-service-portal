# ADR-0003：领域模型独立于持久化适配器

- 状态：已接受
- 日期：2026-08-11

## 背景

实体、领域错误与输入/查询结构体（`Account`、`InviteCode`、`PaymentOrder`、`PaymentPlan`、`PaymentOrderFilter` 等）原本定义在 `internal/persistence/sqlite/types.go` 中。ADR-0001 承诺"持久化留缝以便将来接 PostgreSQL 适配器"，但该接缝并未真正兑现：`web` 层与各服务直接引用 `sqlite.*` 类型，任何第二个持久化适配器都必须 import sqlite 包才能拿到领域模型；存储细节（分页过滤器等）也泄漏到了 HTTP 层。

## 决策

1. 新建 `internal/domain` 包（零依赖，仅标准库），按业务域拆分实体、领域错误与输入/查询结构体。
2. `internal/persistence/sqlite` 成为纯粹的适配器：只保留 `Store`/`Open`、迁移与 SQL 实现，方法签名与返回值全部改用 `domain.*` 类型。
3. `web` 层与各服务对领域类型的引用一律改为 `domain.*`；`sqlite` 包名不再出现在 `web` 的 import 中（`web` 只依赖 `domain` 与服务层）。
4. `payments` 通过 `store.FindAccountByUsername` 直读账号表属于已登记例外：该读取只用于续费订单绑定账号，写权仍在 `accounts`；若将来出现第二个持久化适配器，应优先把此类读取改为 `accounts` 服务的公开方法。

## 后果

### 正面

- ADR-0001 的持久化接缝兑现：未来 PostgreSQL 适配器只需实现同一批 `domain.*` 契约，无需 import sqlite 包。
- `web` 与持久化解耦：HTTP 层契约基于领域类型，不再依赖存储包。
- 领域模型可独立演进与测试（纯结构体，无驱动依赖）。

### 负面

- 全仓 27 个文件发生机械性改名（`sqlite.Account` → `domain.Account` 等），提交历史中的类型引用将指向旧包名。
- 单文件 `types.go` 拆为 5 个文件，定位类型时需知道其所属业务域。

## 何时重开

- 若引入第二个持久化适配器：先按此 ADR 收窄各服务的仓储接口（按领域声明所需能力），并消除 `payments` 直读账号表的已登记例外。
- 若进行多实例部署：进程内限流与 worker 互斥需迁移到共享存储；在此之前保持单实例。
