# Feature request implementation

This document maps GitHub Issue #1 to the implementation in SentryX. The wire
contract remains the Sentry Envelope and `/api/0` resource model; the additions
are backwards-compatible query and lifecycle capabilities.

## Ingestion and Relay

`SENTRYX_ITEM_POLICY` is a comma-separated map such as
`transaction:drop,session:drop,replay_event:store,profile:sample:25`.
Supported actions are `store`, `drop`, and `sample:N` (percentage). Unknown
types default to `store`, so future Sentry SDK items are not silently lost.
Relay exposes `/health/ready` and `/metrics`. When
`SENTRYX_MIRROR_SPOOL_DIR` is set, failed mirror deliveries are written as
atomic JSON files and retried every 15 seconds.

## Query and Issue lifecycle

Existing array responses are preserved. Set `limit` (1-100) and follow the
`X-Next-Cursor` response header:

```
GET /api/0/issues/?project=1&start=2026-09-01T00:00:00Z&end=2026-09-02T00:00:00Z&status=unresolved&sort=last_seen&limit=50
GET /api/0/events/?project=1&issue=<issue-id>&environment=production&release=web@1
```

`query` supports the compatible subset `is:unresolved`, `level:error`, and
`title:text`. Update lifecycle state with
`PUT /api/0/issues/<id>/` and body `{"status":"resolved"}` (or
`unresolved`/`ignored`, with optional `resolved_in_release`). A resolved issue
automatically becomes a regression when a later event arrives.

## Analytics and alerts

* `GET /api/0/issues/<id>/series?project=1&resolution=1h`
* `GET /api/0/issues/<id>/tags/<key>/?project=1`
* `GET /api/0/projects/<org>/<project>/stats`
* `GET|POST /api/0/projects/<org>/<project>/alert-rules`
* `PUT|DELETE /api/0/projects/<org>/<project>/alert-rules/<id>`

Alert rules support `new_issue`, `regression`, and event-count conditions,
with a webhook action, threshold, window, cooldown, and enabled flag. The
worker evaluates rules every 30 seconds outside the ingest loop. Evaluation is
serialized with a PostgreSQL advisory lock, filters `level`, `environment`, and
`release` are enforced, and cooldown state is isolated per `(rule, issue)`.
Webhook calls have a 10-second timeout and include issue identity, title,
level, counts, latest event, stack-top frame, and optional direct URL from
`SENTRYX_UI_BASE_URL`.

`users` now always means distinct user identity inside the requested window;
it is calculated exactly from event data rather than summing the historical
`user_count` rollup. The `errors`, `issues`, and `users` project cards share the
same `start`/`end` window, and PostgreSQL honors arbitrary positive series
resolutions in the same way as the memory store. Standard SDK context fields
(`browser`, `os`, `runtime`, release/environment/dist, URL, transaction, and
user identity) are promoted to canonical tags during decoding.

## Retention, security, and reconciliation

Migrations `006_feature_request.sql`, `007_grouping_migration.sql`, and
`008_issue_2_correctness.sql` add issue lifecycle columns, hourly
rollups, alert rules, nullable completed-job payloads, and project retention
configuration, persisted grouping-hash mappings/component trees, per-issue
alert cooldown state, and operational indexes. The worker performs bounded
5,000-row cleanup batches in a dedicated goroutine, clears queue
payloads on acknowledgement. `SENTRYX_API_TOKEN_HASHES` accepts
`sha256hex:user-id` entries; plaintext `SENTRYX_API_TOKENS` remains supported
for local compatibility. Blob-backed expired attachments and signals are
reclaimed after the database transaction. `cmd/sentryx-reconcile` paginates
both stores, supports `--start`, `--end`, and `--page-size`, calls the official
Sentry `/api/0/projects/{org}/{project}/events/` endpoint, and reports missing
events plus grouping mismatches. `sentryx-groupctl`
replays JSONL or PostgreSQL events and reports grouping changes between two
algorithm versions.

Grouping v2 is now the default. During rolling upgrades it computes both v2
and legacy v1 hashes; when v1 already resolves to an issue, the v2 mapping is
attached to that same issue, preserving counters, state, first-seen time, and
regression history. Set `SENTRYX_GROUPING_VERSION=v1` only for rollback.

## Operations

The repository now includes `.github/workflows/ci.yml` for Go formatting,
vet/tests (including installing root Node SDK dependencies), and Ant Design UI
builds. `/health/live`, `/health/ready`, and the
Prometheus text endpoint `/metrics` are available on both server and Relay.
