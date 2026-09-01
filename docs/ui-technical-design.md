# SentryX UI 技术方案（Ant Design）

状态：Draft for implementation  
前端栈：React + TypeScript + Vite + Ant Design 5.x + React Router 6 + TanStack Query

## 1. 目标

构建一个接近 Sentry 开源前端信息架构的错误监控控制台，但不复制 Sentry 品牌、源码或私有实现。第一阶段聚焦：

- 项目级 Issue 列表和筛选；
- Issue 详情、异常链、Stack Trace、SourceMap 前后对比；
- Event 时间线、Tags、Contexts、User、Request、Breadcrumbs；
- Release/Artifact 管理；
- Attachment、Client Report 和扩展 Signal 的运维查看；
- 为 Performance、Replay、Profile、Native Crash 预留路由和数据边界。

Sentry 开源前端的入口包含 bootstrap、运行时配置、React 应用初始化和 Router；其当前前端规范建议 React Router 路由、懒加载和查询缓存。Sentry UI 的导航/详情组织方式作为交互参考，不直接复用其组件实现。[Sentry frontend entrypoint](https://github.com/getsentry/sentry/blob/master/static/app/index.tsx)、[Sentry frontend conventions](https://github.com/getsentry/sentry/blob/master/static/AGENTS.md)

## 2. 非目标

第一阶段不实现：

- Sentry 组织、团队、邀请和完整 RBAC；
- Discover、Alert、Dashboard、通知集成；
- Performance/Replay/Profile 的完整分析器；
- Native Crash 完整符号化；
- 直接把管理 token 注入浏览器；
- 复制 Sentry 的内部设计系统或源码。

## 3. 信息架构

```text
AppShell
├── ProjectSwitcher
├── MainNavigation
│   ├── Issues
│   ├── Releases
│   ├── Signals
│   ├── Client Reports
│   └── Settings
├── GlobalToolbar
│   ├── Project selector
│   ├── Environment selector
│   ├── Time range
│   └── Refresh / user menu
└── RouteOutlet
    ├── IssuesPage
    ├── IssueDetailPage
    ├── ReleasesPage
    ├── ReleaseDetailPage
    ├── SignalsPage
    ├── ClientReportsPage
    └── SettingsPage
```

Ant Design 的 `Layout`、`Sider`、`Header`、`Content` 适合固定顶部工具栏和可折叠侧边导航；`Table` 适合排序、筛选和分页集合；`Descriptions` 适合 Issue/Event 详情字段。[Ant Design Layout](https://ant.design/components/layout/)、[Ant Design Table](https://ant.design/components/table/)、[Ant Design Descriptions](https://ant.design/components/descriptions/)

## 4. 路由设计

使用 React Router 6，页面组件全部懒加载：

```text
/projects/:projectId/issues
/projects/:projectId/issues/:issueId
/projects/:projectId/issues/:issueId/events/:eventId
/projects/:projectId/releases
/projects/:projectId/releases/:release
/projects/:projectId/releases/:release/files
/projects/:projectId/signals
/projects/:projectId/client-reports
/projects/:projectId/settings
```

路由守卫顺序：

1. 加载运行时配置；
2. 恢复用户会话；
3. 校验项目是否存在且用户有访问权限；
4. 再加载页面查询。

不存在的项目、Issue 或 Event 使用 `Result`，请求失败使用 `Alert`，空结果使用 `Empty`，加载状态使用 `Skeleton` 或 `Spin`。

## 5. AppShell 与设计系统

### 5.1 Ant Design 主题

```tsx
<ConfigProvider
  theme={{
    algorithm: theme.defaultAlgorithm,
    token: {
      colorPrimary: '#5b5bd6',
      colorInfo: '#5b5bd6',
      colorSuccess: '#2f9e66',
      colorWarning: '#d89614',
      colorError: '#d4380d',
      borderRadius: 6,
      fontSize: 14,
    },
    components: {
      Layout: { siderBg: '#171923', headerBg: '#ffffff' },
      Table: { headerBg: '#fafafa', cellPaddingBlock: 10 },
    },
  }}
>
  <App />
</ConfigProvider>
```

颜色只表达语义，不让颜色成为唯一信息：level 同时使用文本、图标和 `Tag`；错误和成功状态必须满足 WCAG 对比度要求。

### 5.2 布局规则

- Sider 默认宽度 240px，折叠宽度 64px；小于 992px 使用 overlay 模式。
- Header 高度 56px，包含项目选择、环境、时间范围、刷新和用户菜单。
- Content 最大宽度 1440px，页面内部使用 8px spacing scale。
- 列表页采用 `Card` + `Space`/`Flex` + `Table`；详情页采用双栏或三栏响应式布局。
- 不在页面中散落颜色和间距常量，统一放入 theme token 或 `ui/tokens.ts`。

## 6. 页面设计

### 6.1 IssuesPage

用途：快速识别当前项目最重要、最近出现或持续增长的错误。

组件结构：

```text
PageHeader
├── Typography.Title
├── Badge（未解决数量）
└── Space
    ├── Button（刷新）
    └── Dropdown（视图设置）
FilterBar
├── Input.Search（message/title）
├── Select（status/level/environment/release）
├── DatePicker.RangePicker
└── Segmented（所有/未解决/已解决）
IssueTable
└── Table<Issue>
```

表格列：

| 列 | AntD 实现 | 交互 |
|---|---|---|
| Title | `Typography.Text` + `Tooltip` | 点击进入详情 |
| Level | `Tag` | fatal/error/warning/info |
| Count | `Statistic` 或数字 | 按 count 排序 |
| First seen | `Time` 包装组件 | 显示绝对/相对时间 |
| Last seen | `Time` | 当前时间范围过滤 |
| Release | `Tag` | 进入 Release 详情 |
| Environment | `Tag` | 过滤 |

当前 `/api/0/issues` 没有服务端过滤和分页；UI 实现可以先使用小数据集，但生产版本必须切换到 [UI API v1 分页接口](ui-api.md#41-分页-issue-列表)。不要把全量列表无限缓存到 React state。

### 6.2 IssueDetailPage

页面布局：

```text
IssueHeader
├── Breadcrumb
├── Title + level Tag
├── first_seen / last_seen / count
└── Dropdown actions（未来：resolve/ignore/bookmark）
MainGrid
├── StackTraceCard
│   ├── Tabs（symbolicated / minified）
│   ├── FrameList
│   └── SourceMap status Alert
├── EventListCard
│   └── Table<EventSummary>
└── ContextCard
    ├── Tabs（Tags / User / Request / Breadcrumbs / Contexts / Extra）
    └── Descriptions / Collapse / Timeline
Footer
└── AttachmentsCard
```

Stack Trace 展示规则：

1. 优先 `symbolicated_frames`；
2. 空数组回退 `frames`；
3. `symbolication_status=miss` 使用 `Alert type="warning"`；
4. 每个 frame 显示 filename、function、line、column、in_app；
5. 原始 frame 和 symbolicated frame 使用 `Tabs` 对比；
6. 永远对 filename 和 message 做文本渲染，不能使用 `dangerouslySetInnerHTML`。

Event List 使用服务端 cursor 分页。点击事件时路由到 `/events/:eventId`，不要把完整 event JSON 放入 URL 或全局 store。

### 6.3 EventDetailPage

使用 `Descriptions` 展示稳定字段：

```text
Platform | Level | Release | Dist | Environment | SDK | Event ID
```

使用 `Tabs`：

- Exception：异常链、type、value、mechanism；
- Stack Trace：frames 和 symbolicated frames；
- Tags：两列 key/value；
- User：id、username、email、IP（已脱敏时显示 Filtered）；
- Request：URL、method、headers（敏感 header 永不默认展开）；
- Breadcrumbs：`Timeline`；
- Contexts/Extra：`Collapse` + JSON viewer；
- Raw：只读、折叠、限制最大字符数。

### 6.4 ReleasesPage 与 ReleaseDetailPage

ReleasesPage：

- `Table<Release>` 展示 version、created_at、artifact 数、最近事件时间；
- `Button` 打开 `Modal` + `Form` 创建 Release；
- 创建成功后用 `message.success` 和 Query invalidate 刷新。

ReleaseDetailPage：

- `Descriptions` 显示 release、dist、debug ID 覆盖率；
- Artifact 列表使用 `Table`，支持文件名搜索、dist 筛选；
- 上传使用 `Upload`，进度通过 `Progress` 展示；
- 删除必须使用 `Popconfirm`，成功后刷新列表；
- token 只能由 BFF 注入，浏览器不读取 `X-SentryX-Management-Token`。

### 6.5 SignalsPage

按 `kind` 使用 `Segmented` 或 `Select`：transaction、span、replay、profile、session、native。

页面只展示：

- signal 类型和数量；
- `received_at`；
- event_id；
- schema_version；
- payload 是否为 Blob；
- content_type/size。

尚未冻结 schema 的 payload 放在 `Collapse` 中，以 JSON viewer 或下载链接展示。不要在 UI 中根据未知 payload 字段硬编码业务图表。

### 6.6 ClientReportsPage

- 顶部 4 个 `Statistic`：总报告数、丢弃数量、主要 reason、最近时间；
- 主表使用 `Table` 展示 timestamp、reason、category、quantity；
- 用 `Progress` 或 `Bar` 展示原因分布；
- 当没有报告时显示“没有 SDK 丢弃报告”，不要误报为系统零错误。

## 7. 数据层和状态管理

### 7.1 推荐目录

```text
web/
├── src/
│   ├── app/
│   │   ├── App.tsx
│   │   ├── routes.tsx
│   │   └── queryClient.ts
│   ├── api/
│   │   ├── client.ts
│   │   ├── issues.ts
│   │   ├── events.ts
│   │   ├── releases.ts
│   │   ├── attachments.ts
│   │   ├── signals.ts
│   │   └── reports.ts
│   ├── components/
│   │   ├── AppShell/
│   │   ├── Filters/
│   │   ├── IssueTable/
│   │   ├── StackTrace/
│   │   ├── JsonViewer/
│   │   └── Time/
│   ├── features/
│   │   ├── issues/
│   │   ├── releases/
│   │   ├── signals/
│   │   └── clientReports/
│   ├── types/
│   └── ui/
└── package.json
```

### 7.2 TanStack Query

使用 Query Cache 管理服务器状态，React Context 只保存 UI 状态（侧栏折叠、主题、当前项目）。Sentry 开源前端规范也采用查询选项和缓存策略，要求查询具备明确的 stale time。[Sentry frontend data-fetching guidance](https://github.com/getsentry/sentry/blob/master/static/AGENTS.md)

```ts
export const issueKeys = {
  all: ['issues'] as const,
  list: (projectId: string, query: IssueQuery) =>
    [...issueKeys.all, projectId, query] as const,
  detail: (projectId: string, issueId: string) =>
    [...issueKeys.all, projectId, issueId] as const,
};

export function useIssues(projectId: string, query: IssueQuery) {
  return useQuery({
    queryKey: issueKeys.list(projectId, query),
    queryFn: () => listIssues(projectId, query),
    staleTime: 15_000,
    retry: (count, error) => error.code === 'rate_limited' ? false : count < 2,
  });
}
```

建议 stale time：

| 查询 | stale time |
|---|---:|
| Issue 列表 | 15s |
| Issue 详情 | 30s |
| Event 详情 | 60s |
| Release/Artifact | 60s |
| Client Report | 30s |
| Signal 计数 | 15s |

自动刷新必须可暂停，并在 429 或页面隐藏时停止。

## 8. 类型契约

前端类型与 Go JSON 字段保持 snake_case，转换只发生在 API adapter 层：

```ts
export type Level = 'fatal' | 'error' | 'warning' | 'info' | string;

export interface Issue {
  id: string;
  project_id: string;
  title: string;
  level?: Level;
  count: number;
  first_seen: string;
  last_seen: string;
  latest_event_id: string;
  group_hash: string;
}

export interface Event {
  project_id: string;
  event_id: string;
  occurred_at: string;
  received_at: string;
  platform?: string;
  level?: Level;
  release?: string;
  dist?: string;
  environment?: string;
  title: string;
  message?: string;
  culprit?: string;
  exception?: unknown;
  stacktrace?: unknown;
  frames?: StackFrame[];
  symbolicated_frames?: StackFrame[];
  symbolication_status?: 'not_attempted' | 'miss' | 'symbolicated' | string;
  tags?: Record<string, string>;
  extra?: Record<string, unknown>;
  contexts?: Record<string, unknown>;
  user?: User;
  request?: RequestInfo;
  breadcrumbs?: Breadcrumb[];
  sdk?: Record<string, unknown>;
  debug_meta?: Record<string, unknown>;
  raw?: Record<string, unknown>;
  issue_id: string;
}
```

所有未知字段必须保留在 adapter 的 `unknown` 结构中；渲染组件不能假设 P2 Signal payload 的结构永远不变。

## 9. 安全和隐私

- 管理 token 只存在 BFF 或服务器端 session，不放 localStorage、URL、前端 bundle 或错误日志。
- UI 默认隐藏 email、IP、Authorization、Cookie、Token；只有经过权限检查才显示已脱敏值。
- JSON viewer 默认禁止 HTML 渲染；代码区域使用纯文本和虚拟滚动。
- 附件按 `content_type` allowlist 预览，HTML/SVG/脚本只下载不内嵌。
- 所有 mutation 使用 CSRF token 或 SameSite session，并携带 `X-Request-ID`。
- 错误消息不能回显管理 token、数据库 DSN、BlobStore key 的完整 secret。

## 10. 性能和可访问性

- Issue/Event 表使用 cursor 分页和虚拟滚动；单页默认 50 行。
- Stack Trace 超过 200 帧时折叠系统 frame，保留搜索和展开操作。
- Raw/Contexts 使用 lazy render，避免大 JSON 阻塞主线程。
- 关键操作均支持键盘操作；Table 行点击必须有可聚焦的链接语义。
- loading 使用 `Skeleton`，网络错误使用 `Alert`，空状态使用 `Empty`；避免只显示 spinner。
- 生产构建按路由拆包，Issue 详情和 Replay/Performance 页面独立 chunk。

## 11. 测试策略

### 单元测试

- API adapter 的 snake_case 类型解析；
- level、symbolication status、未知 Signal kind 的降级；
- PII 字段隐藏；
- cursor、query string 和错误映射。

### 组件测试

- IssueTable 排序、筛选、空态、429；
- StackTrace symbolicated/minified 切换；
- Breadcrumb Timeline；
- Attachment 下载和不安全 content type；
- Release 创建、上传、删除确认。

### 路由和网络测试

- `/projects/:id/issues` -> `/issues/:issueId` -> `/events/:eventId`；
- 401/403/404/413/429/503 页面状态；
- Query invalidate 和自动刷新暂停。

### E2E

使用真实 SentryX Compose：

1. 通过官方 SDK 产生 Error；
2. API 读取 Issue；
3. UI 打开详情并验证 SourceMap frame；
4. 上传/列举/删除 Artifact；
5. 产生 Client Report、Attachment、Replay/Native Signal；
6. 重启 Server 后重新加载同一页面并验证数据不丢。

## 12. 实施里程碑

### M1：只读错误控制台

- AppShell、项目切换、IssuesPage、IssueDetailPage、EventDetailPage；
- 只调用 `/api/0/issues` 和 `/api/0/events`；
- 完成 Stack Trace、Tags、Contexts、User、Request、Breadcrumbs；
- UI 测试和 NUC E2E。

### M2：Release 和运维页面

- Release/Artifact 页面；
- Client Reports、Attachments、Signals 页面；
- BFF 鉴权和下载安全；
- `/api/1` 分页接口。

### M3：产品能力

- Issue 状态 mutation、用户/团队权限；
- Stats/Outcomes；
- Performance、Replay、Profile、Native Crash 专用页面；
- 审计、告警和 Dashboard。

## 13. 验收标准

- 1440px 桌面、1024px 平板和 375px 移动宽度下布局可用；
- Issue 列表首屏 p95 < 1.5s（已有缓存时 < 300ms）；
- Issue 详情首屏 p95 < 2s，不因 raw payload 阻塞首屏；
- 所有 API 错误都有可操作反馈；
- 所有敏感字段在 UI 默认脱敏；
- 未知 P2 Signal 不导致页面崩溃；
- 无管理 token 出现在浏览器 Network、localStorage、URL 或前端错误事件中；
- 前端测试、Compose E2E 和无障碍键盘检查全部通过。
