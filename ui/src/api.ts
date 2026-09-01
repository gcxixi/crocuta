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
export type Issue = { id: string; project_id: string; title: string; level?: string; count: number; first_seen: string; last_seen: string; latest_event_id: string; group_hash: string }
export type Event = {
  project_id: string; event_id: string; occurred_at: string; received_at: string; platform?: string; level?: string
  release?: string; dist?: string; environment?: string; title: string; message?: string; culprit?: string
  exception?: unknown; stacktrace?: unknown; frames?: Frame[]; symbolicated_frames?: Frame[]; symbolication_status?: string
  tags?: Record<string, string>; extra?: Record<string, unknown>; contexts?: Record<string, unknown>; user?: Record<string, unknown>
  request?: Record<string, unknown>; breadcrumbs?: Array<Record<string, unknown>>; sdk?: Record<string, unknown>; raw?: Record<string, unknown>
}
export type Frame = { filename?: string; function?: string; lineno?: number; colno?: number; in_app?: boolean; context_line?: string; source?: string }
export type Release = { project_id: string; version: string; created_at: string }
export type Signal = { id: string; project_id: string; event_id?: string; kind: string; received_at: string; payload: unknown; schema_version: number; size?: number }
export type ClientReport = { id: string; project_id: string; received_at: string; timestamp: string; discarded_events?: Array<{ reason?: string; category?: string; quantity?: number }> }

const apiBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? ''
const apiToken = (import.meta.env.VITE_API_TOKEN as string | undefined) ?? ''

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (apiToken) headers.set('Authorization', `Bearer ${apiToken}`)
  const response = await fetch(`${apiBase}${path}`, { ...init, headers })
  if (!response.ok) {
    const message = await response.text()
    throw new Error(message || `${response.status} ${response.statusText}`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const api = {
  organizations: () => apiFetch<Organization[]>('/api/0/organizations/'),
  projects: (org: string) => apiFetch<Project[]>(`/api/0/organizations/${encodeURIComponent(org)}/projects/`),
  project: (org: string, project: string) => apiFetch<Project>(`/api/0/projects/${encodeURIComponent(org)}/${encodeURIComponent(project)}`),
  issues: (project: string) => apiFetch<Issue[]>(`/api/0/issues?project=${encodeURIComponent(project)}`),
  events: (project: string, issue?: string) => apiFetch<Event[]>(`/api/0/events?project=${encodeURIComponent(project)}${issue ? `&issue=${encodeURIComponent(issue)}` : ''}`),
  releases: (project: string) => apiFetch<Release[]>(`/api/0/projects/${encodeURIComponent(project)}/releases`),
  createRelease: (project: string, version: string) => apiFetch<Release>(`/api/0/projects/${encodeURIComponent(project)}/releases`, { method: 'POST', body: JSON.stringify({ version }) }),
  signals: (project: string) => apiFetch<Signal[]>(`/api/0/signals?project=${encodeURIComponent(project)}`),
  clientReports: (project: string) => apiFetch<ClientReport[]>(`/api/0/client-reports?project=${encodeURIComponent(project)}`),
}
