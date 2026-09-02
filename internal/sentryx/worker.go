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
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()
	alertTicker := time.NewTicker(30 * time.Second)
	defer alertTicker.Stop()
	for {
		select {
		case <-cleanupTicker.C:
			if deleted, err := p.CleanupExpired(ctx, 30*24*time.Hour); err != nil {
				fmt.Printf("sentryx retention cleanup failed: %v\n", err)
			} else if deleted > 0 {
				DefaultMetrics.Inc("sentryx_retention_deleted_total", map[string]string{"kind": "events"})
			}
		case <-alertTicker.C:
			if err := p.EvaluateAlerts(ctx); err != nil {
				DefaultMetrics.Inc("sentryx_alert_evaluation_errors_total", nil)
			}
		default:
		}
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

// CleanupExpired removes event data outside the configured retention window.
// A later partitioned deployment can replace these deletes without changing
// the worker contract.
func (p *PostgresStore) CleanupExpired(ctx context.Context, fallback time.Duration) (int64, error) {
	if fallback <= 0 {
		fallback = 30 * 24 * time.Hour
	}
	days := int(fallback / (24 * time.Hour))
	if days < 1 {
		days = 1
	}
	var deleted int64
	queries := []string{
		`DELETE FROM sentryx_events e WHERE e.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=e.project_id), $1))`,
		`DELETE FROM sentryx_signals s WHERE s.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=s.project_id), $1))`,
		`DELETE FROM sentryx_attachments a WHERE a.created_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=a.project_id), $1))`,
		`DELETE FROM sentryx_client_reports c WHERE c.received_at < now()-make_interval(days => COALESCE((SELECT retention_days FROM sentryx_control_projects WHERE id=c.project_id), $1))`,
		`DELETE FROM sentryx_ingest_jobs j WHERE j.state IN ('completed','dead') AND COALESCE(j.completed_at,j.received_at) < now()-make_interval(days => $1)`,
	}
	for _, query := range queries {
		result, err := p.db.ExecContext(ctx, query, days)
		if err != nil {
			return deleted, err
		}
		count, _ := result.RowsAffected()
		deleted += count
	}
	return deleted, nil
}
