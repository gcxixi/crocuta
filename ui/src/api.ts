export type Organization = {
  id: string
  slug: string
  name: string
  dateCreated: string
  status?: Record<string, string>
}

export type ProjectKey = { id: string; name?: string; public: string; secret?: string; dsn: string }
export type Team = { id: string; slug: string; name: string; dateCreated: string; memberCount?: number; hasAccess?: boolean }
export type Project = { id: string; slug: string; name: string; platform?: string; dateCreated: string; teams?: Team[]; keys?: ProjectKey[] }
export type Issue = { id: string; project_id: string; title: string; level?: string; count: number; users: number; first_seen: string; last_seen: string; latest_event_id: string; group_hash: string; status?: "resolved" | "unresolved" | "ignored"; regression?: boolean; resolved_in_release?: string }
export type SeriesPoint = { bucket: string; count: number; users: number }
export type TagValueCount = { value: string; count: number }
export type AlertAction = { type: string; url?: string }
export type AlertRule = { id: string; project_id: string; name: string; condition: "new_issue" | "regression" | "count"; threshold: number; window_minutes: number; cooldown_minutes: number; enabled: boolean; filters?: Record<string, string>; actions?: AlertAction[] }
export type Page<T> = { data: T[]; nextCursor?: string }
export type IssueQueryOptions = { status?: string; level?: string; query?: string; environment?: string; release?: string; sort?: string; start?: string; end?: string; limit?: number; cursor?: string }

export type Breadcrumb = {
  timestamp?: string
  type?: string
  category?: string
  message?: string
  level?: string
  data?: Record<string, unknown>
}

export type RequestInfo = {
  url?: string
  method?: string
  headers?: Record<string, string>
  query_string?: string
  data?: unknown
  fragments?: string
}

export type UserInfo = {
  id?: string
  username?: string
  email?: string
  ip_address?: string
  name?: string
}

export type Frame = {
  filename?: string
  abs_path?: string
  function?: string
  lineno?: number
  colno?: number
  in_app?: boolean
  context_line?: string
  pre_context?: string[]
  post_context?: string[]
  source?: string
}

export type Event = {
  project_id: string
  event_id: string
  occurred_at: string
  received_at: string
  platform?: string
  level?: string
  release?: string
  dist?: string
  environment?: string
  title: string
  message?: string
  culprit?: string
  logger?: string
  transaction?: string
  server_name?: string
  fingerprint?: string[]
  exception?: unknown
  mechanism?: unknown
  stacktrace?: unknown
  frames?: Frame[]
  symbolicated_frames?: Frame[]
  symbolication_status?: string
  tags?: Record<string, string>
  extra?: Record<string, unknown>
  contexts?: Record<string, unknown>
  user?: UserInfo
  request?: RequestInfo
  breadcrumbs?: Breadcrumb[]
  sdk?: Record<string, unknown>
  debug_meta?: Record<string, unknown>
  raw?: Record<string, unknown>
  issue_id: string
}

export type Release = { project_id: string; version: string; created_at: string }
export type ArtifactInfo = { project_id: string; release: string; dist?: string; name: string; sha256: string; blob_key?: string; size?: number; debug_id?: string; created_at?: string }
export type Signal = { id: string; project_id: string; event_id?: string; kind: string; received_at: string; payload: unknown; schema_version: number; content_type?: string; size?: number }
export type ClientReport = { id: string; project_id: string; received_at: string; timestamp: string; discarded_events?: Array<{ reason?: string; category?: string; quantity?: number }> }

const apiBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? ""
const apiToken = (import.meta.env.VITE_API_TOKEN as string | undefined) ?? ""

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set("Accept", "application/json")
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json")
  if (apiToken) headers.set("Authorization", `Bearer ${apiToken}`)
  const response = await fetch(`${apiBase}${path}`, { ...init, headers })
  if (!response.ok) {
    const message = await response.text()
    throw new Error(message || `${response.status} ${response.statusText}`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export async function apiFetchPage<T>(path: string, init: RequestInit = {}): Promise<Page<T>> {
  const headers = new Headers(init.headers)
  headers.set("Accept", "application/json")
  if (apiToken) headers.set("Authorization", `Bearer ${apiToken}`)
  const response = await fetch(`${apiBase}${path}`, { ...init, headers })
  if (!response.ok) throw new Error((await response.text()) || `${response.status} ${response.statusText}`)
  return { data: await response.json() as T[], nextCursor: response.headers.get("X-Next-Cursor") || undefined }
}

function withQuery(path: string, values: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "" && value !== "all") query.set(key, String(value))
  })
  const encoded = query.toString()
  return encoded ? `${path}?${encoded}` : path
}

export const api = {
  organizations: () => apiFetch<Organization[]>("/api/0/organizations/"),
  projects: (org: string) => apiFetch<Project[]>(`/api/0/organizations/${encodeURIComponent(org)}/projects/`),
  createProject: (org: string, data: { name: string; slug?: string; platform?: string }) =>
    apiFetch<Project>(`/api/0/organizations/${encodeURIComponent(org)}/projects/`, { method: "POST", body: JSON.stringify(data) }),
  project: (org: string, project: string) => apiFetch<Project>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}`),
  issuesPage: (project: string, options: IssueQueryOptions = {}) => apiFetchPage<Issue>(withQuery("/api/0/issues", { project, ...options })),
  issues: async (project: string, options: IssueQueryOptions = {}) => (await apiFetchPage<Issue>(withQuery("/api/0/issues", { project, ...options }))).data,
  issue: (project: string, issue: string) => apiFetch<Issue>(withQuery(`/api/0/issues/${encodeURIComponent(issue)}`, { project })),
  updateIssue: (issue: string, status: "resolved" | "unresolved" | "ignored", resolvedInRelease?: string) => apiFetch<Issue>(`/api/0/issues/${encodeURIComponent(issue)}`, { method: "PUT", body: JSON.stringify({ status, resolved_in_release: resolvedInRelease }) }),
  events: (project: string, issue?: string) => apiFetch<Event[]>(`/api/0/events?project=${encodeURIComponent(project)}${issue ? `&issue=${encodeURIComponent(issue)}` : ""}`),
  issueSeries: (project: string, issue: string, options: { resolution?: string; start?: string; end?: string } = {}) => apiFetch<SeriesPoint[]>(withQuery(`/api/0/issues/${encodeURIComponent(issue)}/series`, { project, resolution: options.resolution ?? "1h", start: options.start, end: options.end })),
  issueTagValues: (project: string, issue: string, key: string, options: { start?: string; end?: string; limit?: number } = {}) => apiFetch<TagValueCount[]>(withQuery(`/api/0/issues/${encodeURIComponent(issue)}/tags/${encodeURIComponent(key)}/`, { project, ...options })),
  projectStats: (org: string, project: string, options: { start?: string; end?: string } = {}) => apiFetch<Record<string, number>>(withQuery(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}/stats`, options)),
  alertRules: (org: string, project: string) => apiFetch<AlertRule[]>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}/alert-rules`),
  createAlertRule: (org: string, project: string, data: Partial<AlertRule>) => apiFetch<AlertRule>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}/alert-rules`, { method: "POST", body: JSON.stringify(data) }),
  updateAlertRule: (org: string, project: string, id: string, data: Partial<AlertRule>) => apiFetch<AlertRule>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}/alert-rules/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(data) }),
  deleteAlertRule: (org: string, project: string, id: string) => apiFetch<void>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}/alert-rules/${encodeURIComponent(id)}`, { method: "DELETE" }),
  releases: (project: string) => apiFetch<Release[]>(`/api/0/projects/${encodeURIComponent(project)}/releases`),
  createRelease: (project: string, version: string) => apiFetch<Release>(`/api/0/projects/${encodeURIComponent(project)}/releases`, { method: "POST", body: JSON.stringify({ version }) }),
  artifacts: (project: string, release: string) => apiFetch<ArtifactInfo[]>(`/api/0/projects/${encodeURIComponent(project)}/releases/${encodeURIComponent(release)}/files`),
  deleteArtifact: (project: string, release: string, name: string, dist?: string) =>
    apiFetch<void>(`/api/0/projects/${encodeURIComponent(project)}/releases/${encodeURIComponent(release)}/files/${encodeURIComponent(name)}${dist ? `?dist=${encodeURIComponent(dist)}` : ""}`, { method: "DELETE" }),
  signals: (project: string) => apiFetch<Signal[]>(`/api/0/signals?project=${encodeURIComponent(project)}`),
  clientReports: (project: string) => apiFetch<ClientReport[]>(`/api/0/client-reports?project=${encodeURIComponent(project)}`),
}
