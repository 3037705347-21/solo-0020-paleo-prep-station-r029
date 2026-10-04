# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界、工作流以及工作区导入导出。
- `internal/domain`：标本、方案、进度和放行记录的状态、不变量与恢复构造。
- `internal/workspace`：可移植工作区包格式、严格解析与全量一致性校验。
- `internal/repository`：互斥保护的内存仓储和原子提交。
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
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`
- `GET /workspace/export`
- `POST /workspace/import`

## 工作区导入导出

进程内仓储会随进程结束而丢失，因此服务支持把当前工作区作为一份完整、可校验的包带走并恢复。

`GET /workspace/export` 返回一个 JSON 包，包含：

- `format_version`：当前固定为 `1`，导入仅接受同版本包。
- `kind`：固定为 `paleo-prep-station-workspace`，用于识别包类型。
- `generated_at`：导出时间（RFC3339）。
- `specimens`、`plans`、`progress`、`releases`：覆盖当前全部业务事实的四个集合，空工作区导出为空数组而不是 `null`。

`POST /workspace/import` 接收同格式包并执行全量替换：

1. 先严格解析结构（拒绝非法 JSON、未知字段、多对象、缺字段、错类型、错误版本、错误类型标识）。
2. 重建每个实体并校验实体引用（方案必须指向存在的标本、进度必须指向存在的方案、放行必须指向存在的标本）。
3. 校验开放方案关系（每份标本最多一个未结束方案，标本状态与其方案集合必须互证）。
4. 校验进度连续性（序号从 1 连续、比例不回退、时间不倒挂、100% 只能是最后一条且方案必为 `completed`）。
5. 校验放行唯一性（每份标本最多一条放行记录，放行记录与 `available` 终态成对出现）。

只有整份包通过全部校验才会在一次仓储事务中替换当前状态；任何失败都返回 `400` 和可解释的问题列表，原工作区保持不变。

导入结果通过响应体说明：

- 发生替换返回 `201`，`replaced` 为 `true`。
- 包与当前工作区业务事实完全相同（例如重复导入同一份包）返回 `200`，`unchanged` 为 `true`，即使导出时间不同也按幂等处理。
- 旧格式、损坏内容返回 `400`，错误码分别为 `unsupported_format_version`、`workspace_corrupt`；业务一致性失败返回 `workspace_validation_failed` 并附带逐项 `issues`。
- 空工作区可以正常导出；把空包导入有数据的工作区是一次合法的清空恢复点，导入空工作区则是幂等空操作。

恢复后既有查询和四条工作流继续使用同一套领域规则：已完成方案仍不能追加进度、目录号仍唯一、标本仍只能放行一次。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow backup-restore
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由；`backup-restore` 会导出工作区、恢复到一个全新的服务实例，并验证恢复后工作流仍可继续。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
