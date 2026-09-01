# SentryX UI API 契约

状态：Draft for UI implementation  
目标：为基于 Ant Design 的 SentryX 控制台提供稳定、可分页、可演进的 API 契约。

## 1. 适用范围

本文分为两层：

1. **现有 API**：当前 Go Server 已提供，前端可以直接接入。
2. **UI API v1 扩展**：为了实现接近 Sentry 开源 UI 的 Issue 列表、Issue 详情、Release 管理和过滤能力，需要补充的接口。文中明确标注 `planned`，不能在前端提前假定已存在。

设计参考：Sentry 开源前端的入口负责 bootstrap、初始化配置和 React Router；路由位于 `static/app/routes.tsx`，页面按路由懒加载。见 [Sentry frontend entrypoint](https://github.com/getsentry/sentry/blob/master/static/app/index.tsx) 和 [Sentry frontend conventions](https://github.com/getsentry/sentry/blob/master/static/AGENTS.md)。

## 2. 通用约定

### 2.1 Base URL

```text
UI API:      /api/0
SDK ingest:  /api/{project_id}/envelope
             /api/{project_id}/store
```

UI 不直接调用 Relay 的 SDK ingest 路径。开发环境可将 UI 与 Server 同源部署；生产环境建议由 UI BFF 代理 `/api/0`，避免把管理 token 暴露到浏览器。

### 2.2 Headers

```http
Accept: application/json
Content-Type: application/json
X-Request-ID: <uuid>
```

当前 Release/Artifact 写接口使用：

```http
X-SentryX-Management-Token: <token>
```

该 token 是部署级静态凭据，只适合 NUC、内网或 BFF 使用。正式 UI 需要替换为用户会话或带 scope 的 Bearer token：

```http
Authorization: Bearer <session-or-api-token>
```

### 2.3 响应和错误

| 状态码 | 含义 | UI 行为 |
|---:|---|---|
| `200` | 查询成功或 ingest 接收成功 | 渲染数据；ingest 只由 SDK 使用 |
| `201` | Release 创建成功 | 关闭表单并刷新 Release 列表 |
| `204` | Artifact 删除成功或 CORS 预检成功 | 刷新列表 |
| `400` | 参数、Envelope 或 artifact 无效 | `Alert` 展示可读错误 |
| `401` | DSN key、Relay token 或管理 token 无效 | 跳转登录/显示无权限 |
| `404` | 资源不存在 | `Result status="404"` |
| `413` | 请求或 artifact 超过大小限制 | 提示压缩或缩小文件 |
| `429` | 限流 | 读取 `Retry-After`，显示倒计时并暂停自动刷新 |
| `503` | 服务暂时不可用 | 显示重试按钮，不做激进轮询 |

错误响应当前是纯文本；UI BFF 应统一转换为：

```json
{
  "error": {
    "code": "rate_limited",
    "message": "rate limit exceeded",
    "request_id": "...",
    "retry_after_seconds": 30
  }
}
```

### 2.4 时间、ID 和分页

- 时间统一 RFC3339 UTC，例如 `2026-09-01T09:15:10.320Z`。
- `project_id`、`issue_id`、`event_id`、`artifact_id` 按字符串处理，不能转换为 JavaScript number。
- 当前已有列表接口返回全量数组，没有分页；UI v1 必须使用 cursor 分页，禁止在浏览器端对大数组做长期缓存。

## 3. 当前已实现 API

### 3.1 健康检查

```http
GET /health/live
```

响应：

```json
{"status":"ok"}
```

该接口只表示进程存活，不代表 PostgreSQL、Worker 或 BlobStore 可用。UI 不应将它当作业务数据 API。

### 3.2 Issue 列表

```http
GET /api/0/issues?project={project_id}
```

当前响应为数组：

```json
[
  {
    "id": "89419e8cbbf4ce4e",
    "project_id": "1",
    "title": "source map lookup failed",
    "level": "error",
    "count": 10,
    "first_seen": "2026-09-01T08:08:52.780Z",
    "last_seen": "2026-09-01T09:12:29.252Z",
    "latest_event_id": "ccbeede39e8e48cc952fdd220b60ed31",
    "group_hash": "1e39104c248d6b4a"
  }
]
```

字段说明：

| 字段 | UI 用途 |
|---|---|
| `id` | 路由参数和详情查询键 |
| `title` | Issue 主标题 |
| `level` | `Tag`/颜色：fatal、error、warning、info |
| `count` | 事件次数 |
| `first_seen` / `last_seen` | 相对时间和详情时间范围 |
| `latest_event_id` | 打开最新事件 |
| `group_hash` | 高级调试信息，不作为用户主标题 |

当前不支持服务端过滤、排序、分页、状态、环境和 Release 参数；前端只可做小规模本地排序，不能声称与 Sentry Issue 列表完全一致。

### 3.3 Issue 下的事件

```http
GET /api/0/events?project={project_id}&issue={issue_id}
```

省略 `issue` 时返回项目事件；当前同样是全量数组。

事件核心结构：

```json
{
  "project_id": "1",
  "event_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "occurred_at": "2026-09-01T09:15:10.320Z",
  "received_at": "2026-09-01T09:15:10.320Z",
  "platform": "javascript",
  "level": "error",
  "release": "web@1.0.0",
  "dist": "web",
  "environment": "production",
  "title": "checkout failed",
  "message": "checkout failed",
  "culprit": "checkout@app.min.js",
  "fingerprint": ["{{ default }}"],
  "exception": {},
  "stacktrace": {},
  "frames": [],
  "symbolicated_frames": [],
  "symbolication_status": "symbolicated",
  "tags": {"browser": "Chrome"},
  "extra": {},
  "contexts": {},
  "user": {},
  "request": {},
  "breadcrumbs": [],
  "sdk": {},
  "debug_meta": {},
  "raw": {},
  "issue_id": "89419e8cbbf4ce4e"
}
```

详情页展示顺序建议：

1. `title`、`level`、`release`、`environment`、`last_seen`。
2. `symbolicated_frames`；为空时回退 `frames`，并显示 `symbolication_status`。
3. exception chain、mechanism、message。
4. tags、contexts、user、request、breadcrumbs、extra。
5. raw event 作为折叠的调试面板，不默认展开。

### 3.4 Client Report

```http
GET /api/0/client-reports?project={project_id}
```

响应：

```json
[
  {
    "id": "2",
    "project_id": "1",
    "received_at": "2026-09-01T09:14:18.664Z",
    "timestamp": "2026-09-01T09:14:18.501Z",
    "discarded_events": [
      {"reason":"sample_rate","category":"error","quantity":3}
    ]
  }
]
```

UI 页面使用 `Table` 展示 reason/category/quantity，并在顶部用 `Statistic` 汇总数量。该接口是诊断 SDK 丢弃事件的原始记录，不等于完整 Sentry Stats。

### 3.5 Attachment 列表和下载

```http
GET /api/0/attachments?project={project_id}&event={event_id}
GET /api/0/attachments/{attachment_id}?project={project_id}
```

列表响应：

```json
[
  {
    "id": "c80f515002f1d96f969b65c7bb1ca754",
    "project_id": "1",
    "event_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    "filename": "console.log",
    "content_type": "text/plain",
    "size": 20,
    "sha256": "...",
    "blob_key": "attachments/1/.../console.log",
    "created_at": "2026-09-01T09:14:18.664Z"
  }
]
```

下载响应是二进制内容，响应头包含 `Content-Type` 和 `Content-Disposition`。UI 必须：

- 文本/JSON 使用 `Drawer` 或 `Modal` 预览，并限制预览大小；
- 其他类型只提供下载按钮；
- 不根据 `filename` 直接执行或渲染 HTML/脚本；
- 通过 `URL.createObjectURL` 下载后及时 `revokeObjectURL`。

### 3.6 扩展 Signal

```http
GET /api/0/signals?project={project_id}&kind={kind}
```

`kind` 可为：

```text
transaction, span, replay_event, replay_recording,
profile, profile_chunk, session, sessions,
minidump, unreal_report, applecrashreport
```

响应：

```json
[
  {
    "id": "521a024fb7ef9f876a845b20fb3f0b10",
    "project_id": "1",
    "event_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    "kind": "replay_recording",
    "received_at": "2026-09-01T09:14:18.664Z",
    "payload": {"binary":true},
    "schema_version": 1,
    "content_type": "application/octet-stream",
    "size": 21,
    "blob_key": "signals/1/..."
  }
]
```

UI v1 只展示接收数量、时间、类型和 schema；不要把 `payload` 当作稳定的产品字段。Replay/Profile/Native 的可视化必须等对应 Signal schema 冻结后再开发。

### 3.7 Release

查询：

```http
GET /api/0/projects/{project_id}/releases
```

创建：

```http
POST /api/0/projects/{project_id}/releases
X-SentryX-Management-Token: <token>
Content-Type: application/json

{"version":"web@1.0.0"}
```

响应：

```json
{
  "project_id": "1",
  "version": "web@1.0.0",
  "created_at": "2026-09-01T09:17:30.385Z"
}
```

### 3.8 Artifact

列举：

```http
GET /api/0/projects/{project_id}/releases/{release}/files
X-SentryX-Management-Token: <token>
```

删除：

```http
DELETE /api/0/projects/{project_id}/releases/{release}/files/{name}
X-SentryX-Management-Token: <token>
```

上传仍兼容 legacy SourceMap multipart：

```http
POST /api/0/projects/{project_id}/releases/{release}/files/?name=app.min.js
X-SentryX-Management-Token: <token>
Content-Type: multipart/form-data
```

Artifact 信息：

```json
{
  "project_id":"1",
  "release":"web@1.0.0",
  "dist":"web",
  "name":"app.min.js",
  "sha256":"...",
  "blob_key":"sourcemaps/1/web@1.0.0/_/app.min.js",
  "size":12345,
  "debug_id":"...",
  "created_at":"2026-09-01T09:17:30.385Z"
}
```

## 4. UI v1 必须补充的 API

现有 API 能够支撑最小只读控制台，但无法支撑大数据量、Issue 状态操作和完整详情。建议新增 `/api/1`，不破坏当前 `/api/0`。

### 4.1 分页 Issue 列表

```http
GET /api/1/projects/{project_id}/issues
  ?cursor={cursor}
  &limit=50
  &query={text}
  &status=unresolved
  &environment=production
  &release=web@1.0.0
  &level=error
  &sort=last_seen
```

响应：

```json
{
  "data": [/* Issue */],
  "next_cursor": "eyJ...",
  "has_more": true
}
```

### 4.2 Issue 详情和状态

```http
GET   /api/1/projects/{project_id}/issues/{issue_id}
PATCH /api/1/projects/{project_id}/issues/{issue_id}
```

PATCH body：

```json
{"status":"resolved"}
```

允许值：`unresolved`、`resolved`、`ignored`。状态变更必须返回新的 Issue，并记录 `updated_at` 和操作者。

### 4.3 Issue 事件分页

```http
GET /api/1/projects/{project_id}/issues/{issue_id}/events
  ?cursor={cursor}&limit=50&environment=production
```

返回 `{data,next_cursor,has_more}`。事件详情页只请求当前页，点击某条事件再请求完整 Event。

### 4.4 单事件详情

```http
GET /api/1/projects/{project_id}/events/{event_id}
```

响应应支持 `include=raw,attachments`，默认不返回大 raw payload 和二进制内容。

### 4.5 Stats 和 Outcomes

```http
GET /api/1/projects/{project_id}/stats
  ?start=2026-09-01T00:00:00Z
  &end=2026-09-02T00:00:00Z
  &interval=1h
  &group_by=outcome,category,environment
```

建议返回：`accepted`、`filtered`、`rate_limited`、`invalid`、`client_discard`、`events`、`users`。UI 用 `Card`、`Statistic` 和 AntV/纯 SVG 图表展示，不从 Issue 数量反推接入量。

### 4.6 Release 分页和构建状态

```http
GET /api/1/projects/{project_id}/releases?cursor=&limit=50&query=
GET /api/1/projects/{project_id}/releases/{version}
GET /api/1/projects/{project_id}/releases/{version}/files?cursor=&limit=50
```

Release 详情应返回 artifact 数量、最后事件时间、symbolication 命中率和 debug ID 覆盖率。

## 5. API 客户端约束

前端建立单一 `apiClient`，页面禁止直接调用 `fetch`：

```ts
export type Page<T> = {
  data: T[];
  next_cursor?: string;
  has_more: boolean;
};

export async function listIssues(
  projectId: string,
  params: IssueQuery,
): Promise<Page<Issue>> {
  return request<Page<Issue>>(`/api/1/projects/${projectId}/issues`, {
    query: params,
  });
}
```

请求层必须统一处理：

- AbortController 取消旧查询；
- 401/403 登录或权限事件；
- 429 的 `Retry-After`；
- 413/422 的字段级错误；
- `X-Request-ID` 透传到错误页面和日志；
- JSON schema 校验，避免后端新增字段导致页面崩溃。

## 6. 版本和兼容策略

- `/api/0` 保持当前 SDK/UI 最小只读兼容，不删除字段。
- `/api/1` 采用分页 envelope 和明确的错误对象。
- 新增字段只做 additive change；删除或改变含义必须升 major API 版本。
- Event/Signal 的 `schema_version` 独立于 API 版本。
- UI 必须对未知 Signal kind 显示 `Unsupported`，不能因为新增 item 让整个页面失败。
