package sentryx

import (
	"context"
	"fmt"
	"time"
)

type IngestJob struct {
	ID        int64
	ProjectID string
	Payload   []byte
	Attempts  int
}

func (p *PostgresStore) QueueDepth(ctx context.Context) map[string]int64 {
	result := map[string]int64{"ready": 0, "processing": 0, "dead": 0}
	rows, err := p.db.QueryContext(ctx, `SELECT state, count(*) FROM sentryx_ingest_jobs GROUP BY state`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if rows.Scan(&state, &count) == nil {
			result[state] = count
		}
	}
	return result
}

// LeaseJobs claims ready jobs and jobs whose previous lease expired. The
// UPDATE...RETURNING statement and SKIP LOCKED allow multiple workers to run
// without a central coordinator.
func (p *PostgresStore) LeaseJobs(ctx context.Context, limit int, lease time.Duration) ([]IngestJob, error) {
	if limit <= 0 {
		limit = 20
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	seconds := int(lease / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		UPDATE sentryx_ingest_jobs AS job
		SET state='processing', attempts=job.attempts+1,
		    lease_until=now()+make_interval(secs => $2)
		WHERE job.id IN (
			SELECT id FROM sentryx_ingest_jobs
			WHERE (state='ready' AND available_at <= now())
			   OR (state='processing' AND lease_until < now())
			ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		RETURNING job.id, job.project_id, job.payload, job.attempts`, limit, seconds)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	defer rows.Close()
	jobs := make([]IngestJob, 0, limit)
	for rows.Next() {
		var job IngestJob
		if err := rows.Scan(&job.ID, &job.ProjectID, &job.Payload, &job.Attempts); err != nil {
			tx.Rollback()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (p *PostgresStore) AckJob(ctx context.Context, id int64) error {
	_, err := p.db.ExecContext(ctx, `UPDATE sentryx_ingest_jobs SET state='completed', payload=NULL, lease_until=NULL, completed_at=now() WHERE id=$1`, id)
	return err
}

func (p *PostgresStore) NackJob(ctx context.Context, id int64, cause error, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	detail := "unknown worker error"
	if cause != nil {
		detail = cause.Error()
	}
	_, err := p.db.ExecContext(ctx, `UPDATE sentryx_ingest_jobs SET state='ready', lease_until=NULL, available_at=now()+make_interval(secs => $2), error_detail=$3 WHERE id=$1`, id, int(retryAfter/time.Second), detail)
	return err
}

// RunWorker continuously drains PostgreSQL jobs. Processing remains bounded
// by the lease and is safe to repeat because Event and Issue writes are
// idempotent at the project/event_id and grouping-hash constraints.
func (p *PostgresStore) RunWorker(ctx context.Context, batch int, poll time.Duration) error {
	if batch <= 0 {
		batch = 20
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	go p.runRetentionLoop(ctx)
	go p.runAlertLoop(ctx)
	for {
		jobs, err := p.LeaseJobs(ctx, batch, 30*time.Second)
		for state, count := range p.QueueDepth(ctx) {
			DefaultMetrics.Set("sentryx_queue_depth", map[string]string{"state": state}, uint64(count))
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll):
				continue
			}
		}
		for _, job := range jobs {
			_, processErr := p.processPayload(job.ProjectID, job.Payload)
			if processErr == nil {
				if err := p.AckJob(ctx, job.ID); err != nil {
					return err
				}
				continue
			}
			if job.Attempts >= 5 {
				_, _ = p.db.ExecContext(ctx, `UPDATE sentryx_ingest_jobs SET state='dead', lease_until=NULL, error_code='worker_max_attempts', error_detail=$2 WHERE id=$1`, job.ID, processErr.Error())
				continue
			}
			backoff := time.Duration(1<<(job.Attempts-1)) * time.Second
			if err := p.NackJob(ctx, job.ID, processErr, backoff); err != nil {
				return fmt.Errorf("nack job %d: %w", job.ID, err)
			}
		}
		if len(jobs) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (p *PostgresStore) runRetentionLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.CleanupExpired(ctx, 30*24*time.Hour); err != nil {
				fmt.Printf("sentryx retention cleanup failed: %v\n", err)
			}
		}
	}
}

func (p *PostgresStore) runAlertLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.EvaluateAlerts(ctx); err != nil {
				DefaultMetrics.Inc("sentryx_alert_evaluation_errors_total", nil)
				fmt.Printf("sentryx alert evaluation failed: %v\n", err)
			}
		}
	}
}

// CleanupExpired removes expired rows in bounded transactions. It runs outside
// the ingest loop and reclaims associated blob objects after relational rows
// have been removed, so object-store failures cannot hold database locks.
func (p *PostgresStore) CleanupExpired(ctx context.Context, fallback time.Duration) (int64, error) {
	if fallback <= 0 {
		fallback = 30 * 24 * time.Hour
	}
	days := int(fallback / (24 * time.Hour))
	if days < 1 {
		days = 1
	}
	connection, err := p.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	var locked bool
	if err := connection.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('sentryx-retention-cleanup'))`).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer connection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('sentryx-retention-cleanup'))`)
	const batchSize = 5000
	type cleanupTarget struct {
		kind     string
		query    string
		withBlob bool
	}
	targets := []cleanupTarget{
		{"events", `DELETE FROM sentryx_events WHERE ctid IN (SELECT e.ctid FROM sentryx_events e WHERE e.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=e.project_id), $1)) LIMIT $2)`, false},
		{"issue_stats", `DELETE FROM sentryx_issue_stats_hourly WHERE ctid IN (SELECT s.ctid FROM sentryx_issue_stats_hourly s WHERE s.bucket < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=s.project_id), $1)) LIMIT $2)`, false},
		{"tag_stats", `DELETE FROM sentryx_issue_tag_values_hourly WHERE ctid IN (SELECT s.ctid FROM sentryx_issue_tag_values_hourly s WHERE s.bucket < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=s.project_id), $1)) LIMIT $2)`, false},
		{"signals", `DELETE FROM sentryx_signals WHERE ctid IN (SELECT s.ctid FROM sentryx_signals s WHERE s.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=s.project_id), $1)) LIMIT $2) RETURNING COALESCE(blob_key,'')`, true},
		{"attachments", `DELETE FROM sentryx_attachments WHERE ctid IN (SELECT a.ctid FROM sentryx_attachments a WHERE a.created_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=a.project_id), $1)) LIMIT $2) RETURNING COALESCE(blob_key,'')`, true},
		{"client_reports", `DELETE FROM sentryx_client_reports WHERE ctid IN (SELECT c.ctid FROM sentryx_client_reports c WHERE c.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=c.project_id), $1)) LIMIT $2)`, false},
		{"jobs", `DELETE FROM sentryx_ingest_jobs WHERE ctid IN (SELECT j.ctid FROM sentryx_ingest_jobs j WHERE j.state IN ('completed','dead') AND COALESCE(j.completed_at,j.received_at) < now()-make_interval(days => $1) LIMIT $2)`, false},
	}
	var deleted int64
	for _, target := range targets {
		for {
			var count int64
			var blobKeys []string
			if target.withBlob {
				rows, err := connection.QueryContext(ctx, target.query, days, batchSize)
				if err != nil {
					return deleted, err
				}
				for rows.Next() {
					var key string
					if err := rows.Scan(&key); err != nil {
						rows.Close()
						return deleted, err
					}
					count++
					if key != "" {
						blobKeys = append(blobKeys, key)
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return deleted, err
				}
			} else {
				result, err := connection.ExecContext(ctx, target.query, days, batchSize)
				if err != nil {
					return deleted, err
				}
				count, _ = result.RowsAffected()
			}
			deleted += count
			if count > 0 {
				DefaultMetrics.Add("sentryx_retention_deleted_total", map[string]string{"kind": target.kind}, uint64(count))
			}
			if deleter, ok := p.blobDeleter(); ok {
				for _, key := range blobKeys {
					if err := deleter.Delete(ctx, key); err != nil {
						DefaultMetrics.Inc("sentryx_blob_gc_errors_total", map[string]string{"kind": target.kind})
					}
				}
			}
			if count < batchSize {
				break
			}
			select {
			case <-ctx.Done():
				return deleted, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return deleted, nil
}

func (p *PostgresStore) blobDeleter() (BlobDeleter, bool) {
	if p.artifacts == nil || p.artifacts.blob == nil {
		return nil, false
	}
	deleter, ok := p.artifacts.blob.(BlobDeleter)
	return deleter, ok
}
