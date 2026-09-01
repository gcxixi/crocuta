# SDK 与协议兼容范围

本文件是 SentryX 对外兼容承诺的唯一清单。兼容以自动化契约测试为准，不使用笼统的“完全兼容 Sentry”表述。

## SDK 家族

| SDK 家族 | 首期目标 | 说明 |
|---|---:|---|
| `@sentry/browser` | 必须 | 普通异常、未捕获异常、unhandled rejection、breadcrumbs、request/user/context |
| `@sentry/node` | 必须 | Node runtime、HTTP request、CommonJS/ESM stack、exception chain |
| `@sentry/react` | 必须 | ErrorBoundary、React context；复用 JS Event Schema |
| `@sentry/vue` | 必须 | Vue error handler、component 信息；复用 JS Event Schema |
| `@sentry/angular` | 必须 | Angular ErrorHandler、zone 相关堆栈；复用 JS Event Schema |
| TypeScript 应用 | 必须 | 与 Browser/Node SDK 相同，重点验证 Source Map |

正式支持的具体 SDK 版本范围由 CI 测试矩阵固定：每个家族覆盖团队当前主版本和一个仍在使用的前一主版本。升级矩阵需要先增加 fixture 与端到端测试。

## Envelope item

| Item type | 首期 | 行为 |
|---|---:|---|
| `event` / `error` | 支持 | 解码、规范化、Source Map、分组、持久化 |
| `client_report` | 支持 | 解析丢弃原因并入库/查询，不创建错误事件 |
| `transaction` / `span` | 接收 | 以版本化 StoredSignal 持久化，暂不提供性能分析 UI |
| `replay_event` / `replay_recording` | 接收 | JSON 保留 payload；二进制写入 BlobStore |
| `session` / `sessions` | 接收 | 以版本化 StoredSignal 持久化 |
| `profile` / `profile_chunk` | 接收 | 以版本化 StoredSignal 持久化 |
| attachment/minidump/native crash | 支持子集 | Attachment 元数据和内容可查询；native 二进制走 BlobStore，暂不做完整符号化 |

## 协议特性

| 特性 | 首期 |
|---|---:|
| Envelope endpoint | 支持 |
| Legacy Store endpoint | 支持 |
| query `sentry_key` 与 `X-Sentry-Auth` | 支持 |
| HTTP gzip | 支持 |
| DSN public key、项目禁用、origin 校验 | 支持 |
| 兼容 rate-limit 响应 | 支持 |
| `event_id` 幂等 | 支持 |
| custom fingerprint / `{{ default }}` | 支持 |
| release/dist/environment | 支持 |
| legacy release file Source Map 上传 | 支持 |
| debug ID / artifact bundle | debug ID 支持；artifact bundle 接收边界已预留 |
| Sentry 管理 API 全兼容 | 不支持 |

## 必测场景

每个 SDK 家族至少包含以下可适用场景：

1. 捕获普通 Error。
2. 未捕获异常与 unhandled promise rejection。
3. exception chain/cause。
4. 无 stacktrace 的 message event。
5. 自定义 tags、user、request、breadcrumbs、contexts。
6. release、dist、environment。
7. 自定义 fingerprint 以及包含 `{{ default }}` 的 fingerprint。
8. 相同事件重复投递。
9. Source Map 命中、缺失、错误 map 和多个候选。
10. 同一逻辑错误跨两个带 content hash 的 bundle 构建仍稳定分组。
11. Envelope 同时含受支持 error 和不支持 transaction/replay item。
12. 超大、截断、错误 length、深层 JSON 和压缩比异常的恶意输入。

## 兼容性判定

- **支持**：SDK 端不需要自定义 transport，只修改 DSN；接入响应、主要字段、Source Map 和 Issue 聚合均由 CI 验证。
- **部分**：请求可接受但只有文档列出的字段或行为生效。
- **不处理**：Relay 可识别并安全丢弃，且有指标；不会创建对应产品数据。
- **不支持**：接口可能返回明确错误，客户端不得依赖。

每次协议变更必须新增 fixture；解析器对未知字段遵循 forward-compatible 原则，但不承诺未知字段产生产品行为。
