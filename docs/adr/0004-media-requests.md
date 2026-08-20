# ADR 0004：用户中心求剧（TMDB 搜索 + Emby 库存标记 + 求剧记录）

- 状态：已接受
- 日期：2026-08-12

## 背景

登录用户希望能直接查询想看但可能尚未入库的影视：搜索 TMDB，对照当前 Emby 库存判断是否已有，没有则提交求剧；管理员需要看到求剧信息（剧名、TMDB 编号、求剧用户、时间）并跟踪处理结果。

## 决策

1. 新增 `internal/tmdb` 适配器封装 TMDB v3 `search/multi` 与 `movie|tv/{id}`（zh-CN），与 `internal/paymentcenter` 一样不把外部 JSON 细节散落到 Web 层。API Key 通过可选环境变量 `ESP_TMDB_API_KEY` 提供；未配置时求剧搜索不可用并展示设置提示，其余功能不受影响（不设为必填，避免升级即破坏现有部署）。
2. 库存判断复用 Emby `Items` 端点的 `AnyProviderIdEquals=tmdb:…`，一次批量请求核对整组搜索结果（`internal/emby.HTTPClient.AnyProviderIDExists`），把 Emby 类型（Movie/Series）归一化为 TMDB 词汇（movie/tv）。查询失败只导致本次搜索结果缺少库存标记，不阻塞搜索。
3. 新增 `media_requests` 表（`internal/requests` 模块、迁移 0014）保存剧名、原始片名、TMDB 编号、类型、海报、上映日期，以及求剧业务账号快照。同一业务账号对同一 TMDB 标题唯一（`UNIQUE(account_id, tmdb_id, media_type)`）。
4. 提交求剧时服务端通过 `tmdb.Details` 回查权威条目（不信任浏览器回传的标题等字段），并再次调用 Emby 校验库存，已存在则拒绝（`ErrRequestInLibrary`）。落盘走 UPSERT：驳回过的求剧可被同一用户重新激活为「待处理」。
5. 后台「求剧管理」支持按状态筛选、关键词搜索（标题/用户/TMDB 编号）、分页，以及标记「已入库」「已驳回」和删除；求剧总数/待处理/已入库三个汇总卡仅在未筛选状态下展示全量口径。
6. 求剧提交按「IP+业务账号」限流（20 次/小时），页面 POST 全部走 CSRF 校验；求剧记录落盘只在本地 SQLite 事务内完成，不发起外部调用。

## 后果

- 部署需要申请 TMDB API Key 并配置 `ESP_TMDB_API_KEY` 后用户端才可使用搜索；历史求剧记录在后台始终可管理。
- Emby/TMDB 故障只影响求剧功能本身；库存标记按 TMDB 编号匹配，标题重命名或同一内容换用其他编号时可能出现误标，属可接受误差。
- 同一标题可被多个用户各求一次（每人一条），管理员可同时看到多个求剧信号。