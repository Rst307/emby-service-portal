# ADR 0005：最近更新与求剧自动入库

- 状态：已接受
- 日期：2026-08-22

## 背景

用户希望知道 Emby 库最近新增了什么，并且当新增内容命中待处理的求剧时，无需管理员手动操作就能自动闭环。这要求一个周期性监测机制（复用现有 worker 节奏），一个持久化的「最近更新」快照，以及求剧状态自动流转。

## 决策

1. 监测直接查询 Emby `Items` 端点（`Recursive=true&SortBy=DateCreated&SortOrder=Descending&IncludeItemTypes=Movie,Series&Fields=ProviderIds,DateCreated&Limit=30`），而不是依赖活动/通知日志：该端点任何版本都可用、不依赖插件、一次请求给出最近入库条目及 TMDB provider ID。`internal/emby` 新增 `LibraryWatcher.RecentlyAdded` 适配。
2. 新增 `recently_added` 表（`internal/recent` 模块、迁移 0017），以 Emby 条目 ID 为主键，扫描幂等；每次扫描后裁剪至最新 50 条。条目快照以下发为准（标题、TMDB 编号、类型、入库时间），Emby 时间戳无法解析时回退为扫描时间。
3. 自动闭环在 `UpsertRecentlyAdded` 的同一 SQLite 事务内完成：若 `(tmdb_id, media_type)` 命中 `status='pending' AND kind='full'` 的求剧，立即置为 `fulfilled` 并把该请求 ID 记入条目。**只自动完成『求整部』求剧**：催更（`kind='missing'`）针对缺失剧集，而 Series 条目新增并不等于补齐缺集，绝不自动标记。
4. 复用现有后台 worker（默认每 5 分钟一轮）执行扫描，无新增配置项；失败仅记录日志，不影响其他功能。海报由服务端代理加载（`GET /img/emby/{id}`，携带 API Key 回源 Emby，同源输出），浏览器永远看不到 API Key，也不依赖 Emby 是否允许匿名图片访问（这是初版直连匿名图片时一张都加载不出来的原因）；代理仅对已登录用户提供，Emby 404 时前端回退为首字占位。
5. 用户中心「我的订阅」页新增「最近更新」区块（登录可见）：**横向滑动海报条**（隐藏滚动条，滚动吸附，移动端触摸原生滑动、可在屏幕左右边缘溢出，桌面端提供左右按钮和拖拽），命中求剧的条目带「求剧已入库」标记；海报加载失败时由 `app.js` 的捕获阶段 error 监听回退为首字占位（避免内联脚本与 CSP 冲突）。

## 后果

- 求剧闭环延迟最多一个 worker 周期（默认 5 分钟），无需管理员介入。
- 依赖 Emby 的 `DateCreated` 排序与 `ProviderIds` 字段；部分库设置下字段缺失时条目仍展示（TMDB 编号为 0，无法求剧匹配）。
- 海报代理回源 Emby 图片端点，图片缓存 1 小时；Emby 图片不可用时卡片回退为首字占位，不阻塞功能。
- 已驳回的求剧不会被自动翻转；用户重新求剧后若该条目已在库（同一事务内再次命中）仍会按规则完成。条目首次落库时未命中、之后再求同标题会被库内校验（`ErrRequestInLibrary`）拦截，不存在补单路径。