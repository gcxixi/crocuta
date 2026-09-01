package sentryx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore persists the same canonical Event/Issue model used by the
// memory vertical slice. It performs an at-least-once safe insert: the event
// uniqueness constraint is checked before incrementing the Issue counter.
type PostgresStore struct {
	db        *sql.DB
	artifacts *ArtifactStore
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &PostgresStore{db: db, artifacts: newArtifactStoreWithDB(db)}, nil
}

func (p *PostgresStore) Close() error { return p.db.Close() }

func (p *PostgresStore) SetArtifactStore(artifacts *ArtifactStore) { p.artifacts = artifacts }

func (p *PostgresStore) ArtifactStore() *ArtifactStore { return p.artifacts }

func (p *PostgresStore) Ingest(projectID, _ string, body []byte) (accepted int, err error) {
	items, err := parseEnvelope(body)
	if err != nil {
		return 0, err
	}
	now := timeNowUTC()
	checksum := artifactDigest(body)
	var jobID int64
	if err := p.db.QueryRow(`
		INSERT INTO sentryx_ingest_jobs
		  (project_id, received_at, payload, checksum, state)
		VALUES ($1, $2, $3, $4, 'processing')
		RETURNING id`, projectID, now, body, checksum).Scan(&jobID); err != nil {
		return 0, err
	}
	defer func() {
		state := "completed"
		if err != nil {
			state = "ready"
		}
		_, _ = p.db.Exec(`UPDATE sentryx_ingest_jobs SET state=$1, completed_at=CASE WHEN $1='completed' THEN now() ELSE completed_at END WHERE id=$2`, state, jobID)
	}()
	for _, item := range items {
		if item.Type != "event" && item.Type != "error" {
			continue
		}
		event, err := decodeEvent(projectID, item.Payload, now)
		if err != nil {
			continue
		}
		if p.artifacts != nil {
			// Reuse the same symbolication semantics as the memory backend.
			for _, frame := range event.Frames {
				if mapped, ok := p.artifacts.Lookup(event.ProjectID, event.Release, event.Dist, frame.Filename, frame.Lineno, frame.Colno); ok {
					event.SymbolicatedFrames = append(event.SymbolicatedFrames, mapped)
				}
			}
			if len(event.SymbolicatedFrames) > 0 {
				event.SymbolicationStatus = "symbolicated"
			} else {
				event.SymbolicationStatus = "miss"
			}
		}
		groupHash := groupingHash(event)
		tx, err := p.db.BeginTx(context.Background(), nil)
		if err != nil {
			return accepted, err
		}
		result, err := tx.Exec(`
			INSERT INTO sentryx_events
			  (project_id, event_id, occurred_at, received_at, canonical_json)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id, event_id) DO NOTHING`,
			projectID, event.EventID, event.OccurredAt, event.ReceivedAt, mustJSON(event))
		if err != nil {
			tx.Rollback()
			return accepted, err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows == 0 {
			tx.Rollback()
			if err != nil {
				return accepted, err
			}
			continue
		}
		var issueID string
		err = tx.QueryRow(`
			INSERT INTO sentryx_issues
			  (id, project_id, title, level, count, first_seen, last_seen, latest_event_id, grouping_version, group_hash)
			VALUES ($1, $2, $3, $4, 1, $5, $5, $6, 1, $7)
			ON CONFLICT (project_id, grouping_version, group_hash)
			DO UPDATE SET count = sentryx_issues.count + 1,
			  last_seen = EXCLUDED.last_seen, latest_event_id = EXCLUDED.latest_event_id
			RETURNING id`, shortHash(projectID+":"+groupHash), projectID, event.Title, event.Level, now, event.EventID, groupHash).Scan(&issueID)
		if err != nil {
			tx.Rollback()
			return accepted, err
		}
		event.IssueID = issueID
		if _, err = tx.Exec(`UPDATE sentryx_events SET issue_id=$1, canonical_json=$2 WHERE project_id=$3 AND event_id=$4`, issueID, mustJSON(event), projectID, event.EventID); err != nil {
			tx.Rollback()
			return accepted, err
		}
		if err := tx.Commit(); err != nil {
			return accepted, err
		}
		accepted++
	}
	return accepted, nil
}

func (p *PostgresStore) ListIssues(projectID string) []Issue {
	rows, err := p.db.Query(`SELECT id, project_id, title, level, count, first_seen, last_seen, latest_event_id, group_hash FROM sentryx_issues WHERE ($1 = '' OR project_id=$1) ORDER BY last_seen DESC`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Issue, 0)
	for rows.Next() {
		var issue Issue
		if err := rows.Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.Count, &issue.FirstSeen, &issue.LastSeen, &issue.LatestEvent, &issue.GroupHash); err == nil {
			result = append(result, issue)
		}
	}
	return result
}

func (p *PostgresStore) ListEvents(projectID, issueID string) []Event {
	rows, err := p.db.Query(`SELECT canonical_json FROM sentryx_events WHERE ($1 = '' OR project_id=$1) AND ($2 = '' OR issue_id=$2) ORDER BY received_at DESC`, projectID, issueID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Event, 0)
	for rows.Next() {
		var raw []byte
		var event Event
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &event) == nil {
			result = append(result, event)
		}
	}
	return result
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("sentryx: marshal canonical value: %v", err))
	}
	return encoded
}

// Kept as a function to make clock injection straightforward when worker
// retries are added; the memory backend remains on time.Now for now.
func timeNowUTC() time.Time { return time.Now().UTC() }
