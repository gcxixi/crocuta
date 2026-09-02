# ADR 0003: Defer event partitioning without weakening deduplication

## Status

Accepted

## Context

PostgreSQL range partitioning requires every unique constraint on a partitioned
table to contain the partition key. Adding `received_at` to the current
`(project_id, event_id)` key would permit the same SDK event to be stored again
when a retry crosses a partition boundary. SentryX does not yet need event-table
partitioning, and retention already deletes data in bounded batches.

## Decision

Keep the global `(project_id, event_id)` uniqueness guarantee and do not
partition `sentryx_events` yet. Revisit partitioning when measured retention,
vacuum, or query costs justify it. The migration must first add a durable,
unpartitioned deduplication ledger keyed by `(project_id, event_id)`, backfill
and verify that ledger, then partition the event payload table by `received_at`.
Only after dual-write and retry tests pass may the original constraint change.

## Consequences

Current ingestion remains globally idempotent, including delayed SDK and mirror
retries. A future partition migration is more involved, but it will be staged,
observable, and reversible instead of silently changing event identity.
