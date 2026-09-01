# SentryX 完整技术方案

状态：Draft for implementation  
范围：Relay 与 Server 的 Go 重写；首期 JavaScript/TypeScript Web Error 接入与 Issue 聚合

## 1. 目标与原则

### 1.1 产品目标

SentryX 的首个可用版本需要让现有 Sentry JavaScript 生态 SDK 只修改 DSN 即可上报错误，并在服务端形成可检索、可稳定聚合的 Issue。目标 SDK 包括浏览器 JavaScript、TypeScript、Node.js、React、Vue 与 Angular；框架 SDK 最终都落入 Sentry JavaScript Event Schema，因此不按框架建立独立后端协议。

### 1.2 工程原则

1. **兼容入口，内部解耦**：对外遵循 Sentry Envelope、Store、DSN 与 Event Schema；内部使用版本化 Canonical Event，避免让第三方协议渗透到所有领域层。
2. **先模块化单体，后按压力拆分**：Server 保持一个代码库和二进制，通过运行角色独立扩缩 API 与 Worker；只有指标证明需要时才拆服务。
3. **接受即持久化**：只有 Envelope 已进入持久任务队列才返回成功，进程内队列不作为可靠性边界。
4. **原始数据可回放**：短期保留受限的原始 Envelope，以便修复解析器后重放；敏感字段必须在保留策略和访问控制内。
5. **算法必须版本化**：规范化、符号化和分组均记录版本；升级算法不能静默改变历史 Issue。
6. **默认安全和节制依赖**：首期不引入 Kafka、Redis、ClickHouse、Elasticsearch；优先 PostgreSQL 和可替换 BlobStore。

### 1.3 非目标

首期不追求 Sentry 产品层面的完全兼容，不实现完整管理 API、Discover、告警通知、Performance、Replay、Session、Profile、Cron、Logs、移动端和原生崩溃符号化。未知或暂不支持的 Envelope item 应可观测地丢弃，不能影响同一 Envelope 内受支持的错误事件。

## 2. 规划基线与验收指标

以下容量是设计基线，不是硬编码限制，进入实施前应以真实流量校准：

- 稳态 1,000 Envelope/s，短时突发 5,000 Envelope/s。
- 单 Envelope 默认压缩后 1 MiB、解压后 5 MiB；项目可下调，平台有不可突破的上限。
- 同一项目以 `event_id` 幂等；重复上报不重复增加 Issue 次数。
- Relay 成功响应 p95 小于 100 ms；从接收到 Issue 可见的处理延迟 p95 小于 10 s。
- 已返回成功的事件在单节点进程重启时不丢失；第一阶段服务目标为月度 99.9% 接入可用性。
- 契约测试覆盖 Node.js、Browser、React、Vue、Angular SDK 产生的匿名化真实 Envelope 样本。

## 3. 总体架构

### 3.1 组件

| 组件 | 职责 | 是否持有业务状态 |
|---|---|---|
| `sentryx-relay` | Sentry 公网入口、请求解压、Envelope 流式解析、DSN 鉴权、粗粒度限流、大小限制、转发、丢弃统计 | 无；仅短 TTL 项目配置缓存 |
| `sentryx-server api` | 管理/查询 API、内部接入 API、Envelope 持久化、项目配置、Release 与 Source Map 接口 | PostgreSQL/BlobStore |
| `sentryx-server worker` | 任务领取、事件解码、规范化、PII 清洗、Source Map、分组与 Issue 聚合 | PostgreSQL/BlobStore |
| PostgreSQL | 租户配置、持久任务、事件、Issue、分组映射、Release/Artifact 元数据 | 是 |
| BlobStore | Source Map 与未来大对象；开发使用文件系统，生产使用 S3 兼容实现 | 是 |

`sentryx-server` 是同一 Go 二进制，支持 `--role=api|worker|all`。小规模部署运行 `all`；需要扩容时将 API 与 Worker 作为不同 Deployment，仍共享领域代码和数据库。

### 3.2 请求链路

```text
SDK
  -> POST /api/{project_id}/envelope/ 或 /api/{project_id}/store/
Relay
  -> 请求级限额、公共 DSN key 校验、解析 Envelope header
  -> POST /internal/v1/envelopes（服务间认证、流式 body、边缘 PII 清洗）
Server API
  -> 再次校验 project/key、解压和 PII 清洗
  -> 单事务写 ingest_jobs(payload BYTEA, metadata, state=ready)，只保存清洗后的 Envelope
  -> 202/200
Worker
  -> SELECT ... FOR UPDATE SKIP LOCKED 租约领取
  -> item 路由 -> Canonical Error -> 防御性 scrub -> source map -> grouping
  -> 单事务写 event、group hash/issue 聚合，完成 job
```

初期将受限大小的原始 Envelope 直接放在 PostgreSQL `BYTEA` 持久任务中，获得单事务“接受即持久化”，并避免引入消息中间件。任务完成后按短保留期清除 payload。若流量或数据库膨胀达到阈值，再把 `Queue` 接口迁移到 NATS JetStream/Kafka，把大 payload 迁移到 BlobStore；外部协议与处理器不变。

### 3.3 Relay 与 Server 的信任边界

- 浏览器 DSN public key 是项目标识/写入凭据，不视为机密；secret key 不要求出现在浏览器请求。
- Relay 可以缓存有效项目、key、限额和 scrub 版本，但 Server 是最终权威，必须复验。
- Relay 到 Server 使用 mTLS 或短期服务令牌，禁止直接暴露 `/internal/*`。
- Relay 不访问业务数据库，不执行分组或 Source Map，不保存长期状态。
- 配置服务不可用时只允许在有限的 stale-while-revalidate 时间内使用旧配置，并记录指标。

## 4. Sentry 协议兼容设计

### 4.1 首期入口

| API | 首期行为 |
|---|---|
| `POST /api/{project_id}/envelope/` | 主入口；支持 Envelope header、item header、逐 item payload |
| `POST /api/{project_id}/store/` | 旧 SDK 兼容入口；将单事件包装成内部 Envelope |
| `OPTIONS` | 浏览器 CORS 预检 |
| Release files / Source Map API 子集 | 供 `sentry-cli` 或 CI 上传 Source Map，详见 7.2 |

鉴权兼容 query string 中的 `sentry_key`、`sentry_version`、`sentry_client`，以及 `X-Sentry-Auth`。项目路径参数、Envelope header DSN 与认证 key 必须一致；冲突返回 `400`，无效 key 返回 `401/403`。

### 4.2 Envelope 解析

采用流式、限长解析，不对整个请求无界 `ReadAll`：

1. 处理 HTTP `gzip`/`br`（是否启用 br 由基准测试决定），在解压层同时限制压缩与解压字节数、压缩比和耗时。
2. 第一行为 Envelope JSON header；之后循环读取 item header 和声明长度的 payload。
3. item 有 `length` 时严格按字节读取；缺失时仅在协议允许的位置使用换行边界。
4. 校验 item 数量、header 长度、JSON 深度、字符串长度、时间戳范围和 attachment 总量。
5. 首期处理 `event` 与 `error`；读取 `client_report` 作为丢弃遥测。`transaction`、`span`、`replay_event`、`replay_recording`、`session`、`profile` 等计数后丢弃。
6. 一个不支持的 item 不使整个 Envelope 失败；受支持 item 的格式错误按 item 隔离，并留下处理失败原因。

对 SDK 返回尽可能兼容的短响应：成功 `200/202`；限流 `429` 并携带 `Retry-After` 和兼容的 rate-limit header；格式错误 `400`；过大 `413`；暂时不可接收 `503`。响应不得泄露内部栈或项目配置。

### 4.3 Canonical Signal 模型

协议层先解码为带版本的内部模型，而不是直接写数据库：

```go
type SignalKind string

const (
    SignalError       SignalKind = "error"
    SignalTransaction SignalKind = "transaction"
    SignalSpan        SignalKind = "span"
    SignalReplay      SignalKind = "replay"
)

type Signal interface {
    Kind() SignalKind
    SchemaVersion() uint16
}

type ItemProcessor interface {
    ItemTypes() []string
    Decode(ctx context.Context, env EnvelopeMeta, item Item) (Signal, error)
}
```

`ProcessorRegistry` 按 item type 注册处理器。首期仅注册 `JavaScriptErrorProcessor`；以后新增 Python/Java 错误仍产出 Canonical Error，Performance/Replay 则产出新的 Signal，不修改 Relay 和队列协议。数据库保留 `schema_version` 与必要的原始字段，支持离线迁移。

### 4.4 JavaScript/TypeScript 事件映射

首期解析并保留：

- `event_id`、`timestamp`、`platform`、`level`、`logger`、`transaction`、`culprit`、`server_name`。
- `message`、`logentry`、`exception.values[]`、exception mechanism、stacktrace frames。
- `release`、`dist`、`environment`、`tags`、`extra`、`modules`。
- `user`、`request`、`contexts`、`breadcrumbs`、`sdk`、`debug_meta`。
- frame 的 `filename`、`abs_path`、`module`、`function`、`lineno`、`colno`、`in_app`、context lines、vars。

React、Vue、Angular 的组件名、framework context、mechanism 与 integration 信息作为上下文和标签进入统一 JavaScript 处理器。Node.js 额外规范化 `runtime.name=node`、模块路径、`node:` 内置模块、CommonJS/ESM 路径与服务端 request 信息。

## 5. 数据处理流水线

Worker 对每个受支持 item 执行固定阶段，并记录每阶段耗时、版本和结果：

1. **Decode**：Sentry JSON -> Canonical Error；宽容读取未知字段，严格限制资源使用。
2. **Validate**：校验 event ID、时间、字段类型；生成缺失的接收时间，但不伪造 SDK event ID。
3. **Normalize**：URL、文件路径、平台、level、tags、exception 链、frame 顺序、时间戳统一。
4. **Scrub**：在落最终事件表前清洗 header、cookie、query、user IP、密码/token 字段；规则带版本。
5. **Enrich**：项目配置、release/dist、in-app include/exclude、运行时与框架识别。
6. **Symbolicate**：按 debug ID 或 release/dist/URL 找到 Source Map，生成原始 frame，同时保留 minified frame。
7. **Group**：生成一个或多个版本化 group hash，选择/创建 Issue。
8. **Persist**：幂等插入 Event，原子更新 Issue 计数、first/last seen、level、latest event。
9. **Finalize**：标记任务完成；失败按分类重试或进入 dead-letter 状态。

错误分类：格式/业务错误不重试；数据库、BlobStore、短暂配置错误指数退避重试；超过次数进入 `dead`，保留原因和可人工重放标记。租约超时的 `processing` 任务可被其他 Worker 重新领取。

## 6. Issue 分组设计

### 6.1 基本规则

分组是项目内的纯函数，输入是清洗、规范化且尽可能符号化后的 Canonical Error 与 `grouping_config_version`，输出有序 hash 列表和可解释的 component tree。

优先级如下：

1. SDK 显式 `fingerprint`。纯自定义 fingerprint 直接决定分组；包含 `{{ default }}` 时将自定义片段与默认 hash 组合。
2. exception 链：优先最内层/主要 exception 的 type、规范化 value 与 stacktrace。
3. stacktrace：优先 `in_app=true` frame；无 in-app 时使用过滤后的应用候选 frame。
4. 无 exception 时使用 stacktrace、message/logentry 与 transaction/culprit 的稳定组合。

### 6.2 稳定化

- URL 删除 query/fragment，统一 scheme/host 大小写，按项目规则移除 CDN 前缀。
- bundle 文件名中的 content hash、UUID、十六进制地址、纯数字 ID、时间戳和随机 token 替换为占位符。
- Source Map 成功时优先原始 `module/function/filename`；失败时使用规范化 minified frame，并标记质量。
- 行列号默认不作为主 hash 的强输入，避免每次构建拆分 Issue；项目可在分组配置中开启。
- 过滤浏览器扩展、框架内部、Node 内置模块与项目配置的 system frames，但在事件详情中保留。
- 消息模板化必须保守，规则版本化，并保存 component tree 便于解释“为何被分到一起”。

### 6.3 映射与并发

`grouping_hashes` 使用 `(project_id, algorithm_version, hash)` 唯一约束并映射到 `issue_id`。事务中先尝试查找任一 hash；不存在时创建 Issue 和映射；并发创建通过唯一约束冲突后重查收敛。事件表以 `(project_id, event_id)` 唯一，重复事件不增加 Issue `event_count`。

算法升级新增版本而不重写旧映射。迁移可选择：只对新事件使用新算法；离线 shadow 计算差异；经人工确认后 merge/split。绝不因发布新二进制自动重组历史 Issue。

## 7. Source Map 与 Release

### 7.1 为什么属于首期

React/Vue/Angular/TypeScript 生产错误通常来自压缩 bundle。没有 Source Map，frame、标题和分组会随构建漂移，因此 Source Map 是 Web Error 与稳定 Issue 聚合的必要能力，而不是后续优化。

### 7.2 兼容子集

首个实现提供：

- Release 创建/查询的最小 API。
- 兼容 `sentry-cli releases files upload-sourcemaps` 所需的 legacy release file 上传、列举、删除/覆盖子集。
- 解析 `release`、`dist`、`url-prefix`、`~` 路径和 `sourceMappingURL`。
- Artifact 元数据存 PostgreSQL，压缩内容存 BlobStore，按 SHA-256 去重。实现提供数据库 BYTEA 兼容模式、原子写入的文件 BlobStore 和基于 SigV4 的 S3 兼容 BlobStore；`migrations/002_blobstore.sql` 通过 `blob_key` 与可空 `source_map` 支持滚动迁移。
- 下一小版本增加 debug ID/artifact bundle 接口；事件带 `debug_meta` 时优先 debug ID，之后才回退到 release/dist/URL。

匹配顺序固定并可解释：`debug_id` -> `(project, release, dist, normalized abs_path)` -> release 无 dist -> sourceMappingURL。每次符号化记录 artifact ID、匹配策略和 symbolicator 版本。

### 7.3 安全限制

上传 API 使用管理 token，不接受 DSN public key。限制单文件/单 release 总量，解压归档时阻止 zip slip、压缩炸弹和符号链接逃逸。Source Map 中的源码默认按项目访问权限保护，并支持选择不保存 `sourcesContent`。

## 8. 数据模型

核心表建议如下；所有租户数据访问必须先带 `project_id`，仓储接口不提供无范围的事件查询：

### 8.1 配置与 Release

- `organizations(id, slug, created_at)`
- `projects(id, organization_id, slug, platform, status, config_version, retention_days, created_at)`
- `project_keys(id, project_id, public_key, status, rate_limit, created_at, revoked_at)`
- `releases(id, project_id, version, created_at)`，唯一 `(project_id, version)`
- `artifacts(id, project_id, release_id, dist, debug_id, name, blob_key, sha256, size, metadata, created_at)`

### 8.2 接入任务

- `ingest_jobs(id bigserial, project_id, received_at, payload bytea, checksum, state, attempts, lease_until, available_at, error_code, error_detail, completed_at)`
- `ingest_job_items(job_id, item_index, item_type, state, event_id, error_code)` 可在需要 item 级重放后启用。

关键索引：`(state, available_at, id)`；Worker 以小批次 `FOR UPDATE SKIP LOCKED` 领取。完成 payload 由清理任务按较短 TTL 置空或删除；dead job 使用单独保留策略。

### 8.3 Event 与 Issue

- `events(id, project_id, event_id, issue_id, received_at, occurred_at, platform, level, release, dist, environment, title, culprit, message, exception jsonb, stacktrace jsonb, tags jsonb, contexts jsonb, user_data jsonb, request_data jsonb, breadcrumbs jsonb, sdk jsonb, raw_event jsonb, schema_version, normalization_version, scrub_version, symbolication_status)`
- `issues(id, project_id, short_id, title, culprit, level, status, first_seen, last_seen, event_count, user_count, latest_event_id, grouping_version, created_at, updated_at)`
- `grouping_hashes(project_id, algorithm_version, hash, issue_id, component_tree jsonb, created_at)`
- `event_group_hashes(event_pk, algorithm_version, hash, is_primary)`

唯一约束：`events(project_id, event_id)`、`grouping_hashes(project_id, algorithm_version, hash)`、`issues(project_id, short_id)`。常用索引覆盖项目+时间、项目+issue+时间、release/environment/level；tags 初期使用受控 GIN，避免对任意 JSONB 盲目建索引。

PII 字段在 scrub 后入事件表；若为故障重放短期保留原始 Envelope，使用独立加密密钥、严格权限和短 TTL。项目删除采用后台可恢复窗口后再物理清理 Blob。

## 9. Go 工程设计

### 9.1 包边界

```text
cmd/sentryx-relay          组装 relay 依赖，不放业务逻辑
cmd/sentryx-server         组装 api/worker/all 角色
internal/contracts/sentry  Envelope、Event DTO、兼容错误码
internal/domain            Canonical Signal、Issue、Release
internal/processing        流水线和 ProcessorRegistry
internal/grouping          纯函数、版本注册表、component tree
internal/sourcemap         Artifact 匹配与符号化
internal/queue             Queue/Lease 接口
internal/blob              BlobStore 接口
internal/storage/postgres  pgx/sqlc 仓储实现
internal/transport/http    public/internal/admin handlers
```

协议 DTO、领域模型、数据库模型分离。领域包不依赖 HTTP、PostgreSQL 或 S3。接口定义在使用方，避免建立巨型“公共接口包”。所有外部调用接受 `context.Context`，错误使用可判定类型而不是字符串匹配。

### 9.2 推荐基础库

- HTTP：Go `net/http`，路由仅使用轻量 router；中间件必须可观测且有明确顺序。
- PostgreSQL：`pgx`；查询可用 `sqlc` 生成，不使用重型 ORM。
- 日志：标准库 `log/slog` 的结构化 JSON handler，统一 request/job/event correlation ID。
- 可观测性：OpenTelemetry traces/metrics，暴露 Prometheus 格式指标。
- 配置：环境变量/文件映射到强类型结构，启动时完整校验；secret 不写日志。
- 迁移：版本化 SQL migration，由独立启动命令执行，不由所有实例并发自动执行。

Go 版本通过 `go.mod` 和 `toolchain` 固定；支持当前团队选定版本及一个升级窗口，不在文档中漂移依赖版本。依赖需要锁定、定期漏洞扫描和许可检查。

## 10. API 与管理面

首期除接入 API 外提供最小 JSON 管理/查询 API：

- 项目与 DSN key 的创建、轮换、吊销。
- Release/Artifact 上传与查询。
- Issue 列表/详情、状态更新（unresolved/resolved/ignored）。
- Event 详情与 Issue 下事件分页。
- 健康检查：`/health/live` 只看进程；`/health/ready` 检查必要依赖和 schema 版本。

列表使用 keyset pagination，避免大 offset。所有写接口支持 request ID，管理 token 只保存哈希并具备 scope。首期 UI 可由 API 之外的独立工作实现，不应阻塞核心兼容链路。

## 11. 可靠性、背压与一致性

- Relay 有全局、项目、key、IP 四层限流；多实例初期允许近似配额，严格全局配额需要时再接共享 rate limiter。
- 当前实现提供可配置的项目 key 白名单、管理 Artifact token 和单进程 project/key/client 固定窗口限流；配置为空时不改变 SDK 兼容行为，多实例严格配额仍应替换为共享限流器。
- Relay 到 Server 使用短超时、有限重试和抖动；只有 body 可安全重放且未获得响应时重试，并携带 checksum/idempotency 信息。
- Server 在队列积压、数据库延迟或磁盘水位超过阈值时主动 `429/503`，不能继续接收后静默丢弃。
- Worker 使用租约和幂等写实现 at-least-once；不承诺 exactly-once transport，但实现 exactly-once event aggregation effect。
- PostgreSQL 不可用时不返回成功；BlobStore 暂时不可用的 symbolication 可重试，达到上限后允许事件以 `pending/failed` 入库并可后补符号化。
- 定期 reconciliation 检查卡住租约、孤儿 artifact、Issue 计数偏差与 dead jobs。

## 12. 安全与隐私

- 全链路 TLS；内部接口 mTLS/服务身份；数据库与 Blob 最小权限账户。
- 请求体、JSON 深度、item 数、字符串、breadcrumbs、frames、tags、attachments 均有限制。
- 防 SSRF：Server 不根据事件内 URL 任意联网下载 Source Map；仅使用已上传 artifact。若未来支持抓取，必须使用隔离 fetcher 和 allowlist。
- PII scrub 默认删除认证 header、cookie、密码/token/secret 字段、原始 IP；项目规则只能在平台安全上限内调整。
- 管理 token 带 scope、过期和撤销；审计项目/key/release/issue 状态变更。
- 原始 payload、dead job、Source Map 源码加密存储并分权访问，保留期默认短于处理后事件。
- CORS 基于项目 allowed origins；Node SDK 不受浏览器 origin 限制，但仍需 key、大小与速率限制。

## 13. 可观测性

关键指标：

- `relay_requests_total{project,status}`、请求/解压字节、鉴权失败、限流、Envelope/item type。
- `ingest_accept_latency`、`ingest_jobs_ready`、最老任务年龄、任务重试/dead 数。
- 各流水线阶段 latency/error、事件 decode/drop 原因。
- Source Map match hit/miss/ambiguous、symbolication latency。
- grouping hash 命中/新建/竞争、Issue 聚合冲突重试。
- PostgreSQL 连接池、慢查询、表/索引大小、WAL 与清理延迟。

日志不得输出完整事件、DSN key、token、cookie 或 request body。trace 贯穿 Relay request ID、ingest job ID 和 event ID；高流量项目使用采样，但错误计数指标不采样。

建议告警：成功率低于 SLO、ready job 最老年龄超阈值、dead job 增长、数据库容量/连接水位、Source Map 命中率突降、429/503 异常增长。

## 14. 测试策略

### 14.1 契约与兼容测试

维护匿名化 golden fixtures：由每类官方 SDK 的最小应用真实发出 Envelope，覆盖普通 Error、未捕获异常、unhandled rejection、exception chain、custom fingerprint、release/dist、framework component、Node request、Source Map 成功/缺失。

测试不只比 JSON：启动 Relay+Server+PostgreSQL，SDK 指向 SentryX，断言响应、Event 规范化结果、Issue 分组与幂等性。兼容矩阵按 SDK 家族和协议特性标记，而非声称“100% Sentry compatible”。

### 14.2 其他测试

- Envelope parser、JSON decoder、Source Map parser 进行 Go fuzz，重点覆盖截断、错误 length、深层 JSON、压缩炸弹。
- grouping 使用稳定 golden tests；任何 hash 改动必须显式升级算法版本。
- Repository 使用真实 PostgreSQL 集成测试，覆盖并发同组事件、重复 event_id 和租约恢复。
- 故障注入数据库/BlobStore 超时、Worker kill、Relay 重试，验证接受边界和无重复聚合。
- 基准测试 Envelope 解析、规范化、Source Map 与 grouping；发布前做阶梯压测和积压恢复测试。

## 15. 部署形态

开发环境提供 Docker Compose：Relay、Server all、PostgreSQL、可选 MinIO。生产初期：

- Relay 至少 2 实例，无状态水平扩容。
- Server API 至少 2 实例；Worker 独立实例数按 queue age 自动扩容。
- PostgreSQL 使用托管/主备、PITR 与定期恢复演练。
- BlobStore 启用版本/生命周期策略；Source Map 与未来 Replay 使用不同 prefix/保留策略。
- schema migration 使用部署前 job；应用兼容前后两个 schema 版本，执行 expand/contract。

配置和密钥由部署平台注入。镜像使用非 root、只读文件系统、固定 digest 和 SBOM。Relay 与 internal Server 网络策略分离。

## 16. 扩展路线

### 16.1 其他语言错误

新增协议层平台适配与 `ErrorProcessor`，复用 Canonical Error、scrub、Issue 和查询。Java/Python 等堆栈规范化规则以平台插件注册；原生 crash 因符号文件与计算模型不同，单独实现 Symbolicator worker，但仍通过 Signal/Queue 接口接入。

### 16.2 Performance

注册 `transaction`/`span` processor，新增 trace/span 时序模型与采样模块。高基数查询达到阈值时使用 ClickHouse 等分析存储；不将 Performance 强塞进 Error 表。Relay 已能按 item type 路由和实施动态采样。

### 16.3 Replay

注册 replay metadata/recording processor；大 payload 直接进入 BlobStore，并在 PostgreSQL 保存 session/chunk 索引、TTL 与关联 event ID。Replay 的 PII scrub、压缩与访问授权单独实现，复用项目、DSN、配额和 envelope 层。

### 16.4 队列与存储拆分触发器

只有出现以下任一可量化信号才迁移：PostgreSQL ingest payload 持续造成显著 WAL/膨胀；queue age 无法通过增加 Worker 恢复；单库写入/查询互相干扰；需要跨地域缓冲。迁移时保持 `Queue` 的 lease/ack/nack 语义，将 raw payload 放 BlobStore 或消息系统，Worker 处理接口不变。

## 17. 关键风险与缓解

| 风险 | 缓解 |
|---|---|
| “Sentry 兼容”范围无限扩张 | 发布明确兼容矩阵；未知字段宽容、未知 item 可观测丢弃；每阶段有退出标准 |
| Source Map 不完整导致错误分组 | 将 Source Map 纳入首期；记录匹配原因；分组降级路径与后补符号化 |
| grouping 算法升级拆分/合并历史 Issue | 算法版本化、shadow 评估、显式迁移、component tree 可解释 |
| PostgreSQL 同时承担队列和查询 | payload 短 TTL、分区/清理、积压指标、Queue 接口及拆分触发器 |
| 高流量恶意 Envelope 消耗内存/CPU | 流式解析、分层限制、解压比、超时、fuzz、Relay 背压 |
| PII 在解析前已落原始队列 | 原始 payload 加密、严格权限、短 TTL；高隐私项目支持 Relay 预 scrub 模式 |

## 18. 完成定义

第一阶段完成需要同时满足：

1. 兼容矩阵中的 SDK 用例只改 DSN 即可完成上报。
2. 返回成功的事件经历进程重启后仍能出现，重复投递不重复计数。
3. Source Map 成功与失败路径均可解释，同一代码问题跨常规构建可稳定聚合。
4. 自定义 fingerprint、exception chain、无 stacktrace、Node/Browser/framework 用例分组符合 golden tests。
5. Relay/Server 达到容量基线和 SLO，过载时明确背压而非丢数据。
6. PII、鉴权、大小限制、保留清理、审计与备份恢复通过安全检查。
7. 文档、migration、部署配置、兼容测试和运行手册齐备。
