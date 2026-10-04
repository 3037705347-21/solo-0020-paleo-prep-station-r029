# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界和业务流程。
- `internal/domain`：标本、方案、进度和放行记录的状态与不变量。
- `internal/repository`：互斥保护的内存仓储和原子提交。
- `internal/workspace`：带格式版本的工作区导入导出包、严格解析与全量校验。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

- `GET /specimens` 与 `POST /specimens`
- `GET /specimens/{id}`
- `POST /specimens/{id}/plans`
- `GET /specimens/{id}/plans`
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`
- `GET /workspace/export`：导出完整工作区包。
- `POST /workspace/import`：校验并整体替换工作区。

## 工作区导入导出

进程内仓储的全部业务事实都可以作为一个工作区包带走，也可以整体恢复到任意已验证时点。

导出包是单个 JSON 对象，固定记录：

- `format`：格式名称，当前为 `paleo-prep-station-workspace`。
- `version`：格式版本，当前仅支持 `1`。
- `exported_at`：包的生成时间（RFC3339）。
- `specimens`、`plans`、`progress`、`releases`：覆盖全部标本、方案、进度和放行记录，字段与各业务接口返回的实体一致。

`POST /workspace/import` 先解析再做全量校验，只有整份内容通过后才在同一个仓储事务里替换当前状态：

1. 结构：单一 JSON 对象、无未知字段、格式名和版本受支持、生成时间合法、四个业务区段都存在且为数组。
2. 实体：每个实体自身的必填项、长度、比例、版本号、时间和审批信息配对规则。
3. 实体引用：方案必须指向存在的标本，进度必须指向存在的方案，放行必须指向存在的标本。
4. 开放方案关系：同一标本同时最多一个未结束方案，且标本状态与其方案状态一致。
5. 进度连续性：每个方案的序号从 1 连续递增、比例单调不下降，完成方案的最后比例必须是 100。
6. 放行唯一性：每份标本至多一条放行记录，已放行标本必须是 `available` 且无开放方案。

任何一步失败都返回 `400`（结构或规则问题）和可解释错误码，当前工作区不会发生任何写入。校验通过后返回导入结果：

- `status=unchanged`：导入包与当前工作区业务事实完全相同（重复导入同一包即为此结果）。
- `status=replaced`：工作区已整体替换，`previous_counts` 与 `counts` 分别给出替换前后的事实数量。

空工作区包（四个区段均为空数组）本身是合法时点，导入后清空当前工作区；请求体为空或不是合法 JSON 则会被拒绝。恢复后，所有既有查询和四条工作流仍使用同一套规则，例如已放行标本依然不能重复放行。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow export-import-workspace
go run ./cmd/workflowcheck --workflow import-guard
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由。`export-import-workspace` 覆盖导出、重复导入幂等、误操作后恢复时点以及恢复后同一规则继续生效；`import-guard` 覆盖旧格式、损坏内容、空请求体、空工作区包和各类引用/连续性/唯一性违规，并确认失败导入不会破坏原工作区。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

工作区导入导出能力带有 `internal/workspace`、`internal/station` 和 `internal/repository` 下的自动化测试；四条原始业务链路仍保留生产级 HTTP 链路检查作为端到端烟雾路径，可通过 `go test ./...` 与六条 `workflowcheck` 共同验证。
