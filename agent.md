# Agent 工作约定

## 修改原则

1. 修改前先读相关代码，理解现有架构。
2. 优先最小修改，不动无关代码。
3. 遵守现有模块边界，不跨模块乱调用。
4. 新功能优先放入对应 feature/domain，不堆进大文件。
5. 不提前抽象，不为了“以后可能用”制造复杂设计。
6. 完成后运行必要的 lint/test/build，并检查是否扩大耦合。

## 详细架构分析的触发条件

只有以下情况才进入详细架构分析：

- 新增一个完整业务模块
- 修改核心数据模型
- 修改模块边界
- 涉及 3 个以上领域模块
- 涉及数据库迁移
- 涉及认证、支付、权限、安全
- 大规模重构

## 发布与里程碑

发布策略：自更新通道就是正式版通道。每次推送到 main，CI 自动读取当前最高正式版标签（`vN.N.N`，历史 `v0.0.0-build.*` 预发布标签被排除），将其 patch 位 +1 作为新版本号，内嵌进 Linux/Windows 双平台二进制，并发布为不加 `--prerelease` 标记的正式 Release（v1.0.0 → v1.0.1 → v1.0.2 …），全程无需人工干预。

里程碑发布（需要跳级到 minor/major 时）：

- 触发条件：用户明确要求发布里程碑；一个完整的用户可见功能闭环上线（例如 售卖+支付+履约、自更新、求剧全链路）；包含破坏性变更（ESP_* 配置语义、数据库结构/行为不兼容、API 契约、升级路径）；跨多个功能模块的修复和改进积压；长期未发版。
- 流程（沿用 v1.0.0 实践）：
  1. 全量检查：`go vet ./...`、`go test ./...`（含更新包测试），工作树只含本次发布相关改动。
  2. 本地构建并嵌入版本：`-ldflags "-s -w -X github.com/Rst307/emby-service-portal/internal/buildinfo.Version=vX.Y.Z -X github.com/Rst307/emby-service-portal/internal/buildinfo.Commit=<short-sha>"`，`CGO_ENABLED=0` 产出 `dist/emby-service-portal-linux-amd64` 与 `dist/emby-service-portal-windows-amd64.exe`。
     - 注意：**不要用 CI 工作流产物发里程碑**——CI 产物内嵌的是自动递增的版本号，不是要发布的里程碑版本。
  3. 生成 `<二进制>.sha256`（格式 `64hex + 两个空格 + 文件名`），与二进制一同上传。
  4. `git tag -a vX.Y.Z -m "Emby Service Portal vX.Y.Z"` → `git push origin vX.Y.Z`。
  5. `gh release create vX.Y.Z --title "Emby Service Portal vX.Y.Z" --notes-file <notes> <4 个资产>`（不加 `--prerelease`）。
  6. 发布说明包含：功能亮点、部署方式、校验和。
  7. 验证：Releases API 最新为 vX.Y.Z、4 个资产齐全、二进制内嵌版本正确。
- 里程碑发布后，下一次推 main 的 CI 自动以 vX.Y.Z 为基线继续 patch+1。

版本号规则：自动发布 = 最高正式版 patch+1（v1.0.0 → v1.0.1）；手动里程碑按语义：新功能 = minor（v1.1.0），破坏性变更/架构级变化 = major（v2.0.0）。

注意：并发推送 main 可能算出相同版本号导致 `gh release create` 冲突（该 job 标红），等待上一个 run 完成后重跑即可；更新器按版本号排序选择最新，不存在预发布被误选的问题，历史 `v0.0.0-build.*` 的兼容代码可无害保留。

## GitHub 同步

- 每次完成用户要求的代码、配置或文档更新后，先运行与改动相关的测试，再将本次任务相关改动提交并推送到当前 GitHub 远程分支。
- 推送前必须检查 `git status` 和 `git diff`，只提交本次任务相关文件，不带入工作区中已有的无关改动。
- 如果因为权限、网络、远程冲突或其他原因无法推送，必须明确告知用户实际状态和失败原因，不得声称已经上传。
- 提交信息使用简洁、能说明变更内容的描述。
