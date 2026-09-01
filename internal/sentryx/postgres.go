package sentryx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore persists the same canonical Event/Issue model used by the
// memory vertical slice. It performs an at-least-once safe insert: the event
// uniqueness constraint is checked before incrementing the Issue counter.
type PostgresStore struct {
	db        *sql.DB
	artifacts *ArtifactStore
	PII       PIIConfig
	Async     bool
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
	return &PostgresStore{db: db, artifacts: newArtifactStoreWithDB(db), PII: DefaultPIIConfig()}, nil
}

func (p *PostgresStore) Close() error { return p.db.Close() }

func (p *PostgresStore) SetArtifactStore(artifacts *ArtifactStore) { p.artifacts = artifacts }

func (p *PostgresStore) SetPIIConfig(config PIIConfig) { p.PII = config }

func (p *PostgresStore) ArtifactStore() *ArtifactStore { return p.artifacts }

func (p *PostgresStore) SetBlobStore(blob BlobStore) {
	if p.artifacts != nil {
		p.artifacts.SetBlobStore(blob)
	}
}

func (p *PostgresStore) Ingest(projectID, _ string, body []byte) (int, error) {
	var err error
	body, err = ScrubEnvelope(body, p.PII)
	if err != nil {
		return 0, err
	}
	if _, err := parseEnvelope(body); err != nil {
		return 0, err
	}
	jobID, err := p.EnqueueEnvelope(projectID, body, p.Async)
	if err != nil {
		return 0, err
	}
	if p.Async {
		return 1, nil
	}
	accepted, err := p.processPayload(projectID, body)
	state := "completed"
	if err != nil {
		state = "ready"
	}
	_, _ = p.db.Exec(`UPDATE sentryx_ingest_jobs SET state=$1, completed_at=CASE WHEN $1='completed' THEN now() ELSE completed_at END WHERE id=$2`, state, jobID)
	return accepted, err
}

func (p *PostgresStore) EnqueueEnvelope(projectID string, body []byte, ready bool) (int64, error) {
	now := timeNowUTC()
	state := "processing"
	if ready {
		state = "ready"
	}
	checksum := artifactDigest(body)
	var jobID int64
	err := p.db.QueryRow(`
		INSERT INTO sentryx_ingest_jobs
		  (project_id, received_at, payload, checksum, state)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, projectID, now, body, checksum, state).Scan(&jobID)
	return jobID, err
}

func (p *PostgresStore) processPayload(projectID string, body []byte) (accepted int, err error) {
	items, err := parseEnvelope(body)
	if err != nil {
		return 0, err
	}
	now := timeNowUTC()
	for _, item := range items {
		if item.Type == "client_report" {
			if report, decodeErr := decodeClientReport(projectID, item.Payload, now); decodeErr == nil {
				if _, insertErr := p.db.Exec(`INSERT INTO sentryx_client_reports (project_id, received_at, report_timestamp, discarded_events) VALUES ($1, $2, $3, $4)`, projectID, report.ReceivedAt, report.Timestamp, mustJSON(report.DiscardedEvents)); insertErr != nil {
					return accepted, insertErr
				}
				accepted++
			}
			continue
		}
		if item.Type == "attachment" {
			if err := p.persistAttachment(projectID, item, now); err != nil {
				return accepted, err
			}
			accepted++
			continue
		}
		if isExtendedSignalType(item.Type) {
			signal := StoredSignal{ID: randomID(), ProjectID: projectID, EventID: item.EventID, Kind: item.Type, ReceivedAt: now, Payload: append(json.RawMessage(nil), item.Payload...), Schema: 1}
			payload := signal.Payload
			if !json.Valid(payload) {
				signal.Payload = json.RawMessage(`{"binary":true}`)
				signal.ContentType = item.ContentType
				signal.Size = len(payload)
				signal.BlobKey = path.Join("signals", blobPathSegment(projectID), signal.ID)
				if p.artifacts == nil || p.artifacts.blob == nil {
					return accepted, fmt.Errorf("binary signal requires blob store")
				}
				if err := p.artifacts.blob.Put(context.Background(), signal.BlobKey, payload); err != nil {
					return accepted, err
				}
			}
			if _, err := p.db.Exec(`INSERT INTO sentryx_signals (id, project_id, event_id, kind, received_at, schema_version, content_type, size, blob_key, payload) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, NULLIF($7, ''), $8, NULLIF($9, ''), $10)`, signal.ID, signal.ProjectID, signal.EventID, signal.Kind, signal.ReceivedAt, signal.Schema, signal.ContentType, signal.Size, signal.BlobKey, signal.Payload); err != nil {
				return accepted, err
			}
			accepted++
			continue
		}
		if item.Type != "event" && item.Type != "error" {
			continue
		}
		event, err := decodeEvent(projectID, item.Payload, now)
		if err != nil {
			continue
		}
		event = scrubEvent(event)
		if p.artifacts != nil {
			// Reuse the same symbolication semantics as the memory backend.
			debugID := debugIDFromMeta(event.DebugMeta)
			for _, frame := range event.Frames {
				var mapped StackFrame
				var ok bool
				if debugID != "" {
					mapped, ok = p.artifacts.LookupDebugID(event.ProjectID, debugID, frame.Filename, frame.Lineno, frame.Colno)
				}
				if !ok {
					mapped, ok = p.artifacts.Lookup(event.ProjectID, event.Release, event.Dist, frame.Filename, frame.Lineno, frame.Colno)
				}
				if ok {
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

func isExtendedSignalType(value string) bool {
	switch value {
	case "log", "transaction", "span", "replay_event", "replay_recording", "profile", "profile_chunk", "session", "sessions", "minidump", "unreal_report", "applecrashreport":
		return true
	default:
		return false
	}
}

func (p *PostgresStore) persistAttachment(projectID string, item envelopeItem, now time.Time) error {
	if len(item.Payload) == 0 || len(item.Payload) > int(DefaultMaxBlobBytes) {
		return fmt.Errorf("attachment too large or empty")
	}
	id := randomID()
	filename := item.Filename
	if filename == "" {
		filename = "attachment-" + id
	}
	filename = path.Base(filename)
	blobKey := ""
	if p.artifacts != nil && p.artifacts.blob != nil {
		blobKey = path.Join("attachments", blobPathSegment(projectID), blobPathSegment(item.EventID), id, blobPathSegment(filename))
		if err := p.artifacts.blob.Put(context.Background(), blobKey, item.Payload); err != nil {
			return err
		}
	}
	var body any
	if blobKey == "" {
		body = item.Payload
	}
	_, err := p.db.Exec(`INSERT INTO sentryx_attachments (id, project_id, event_id, filename, content_type, size, sha256, blob_key, body, created_at) VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7, NULLIF($8, ''), $9, $10)`, id, projectID, item.EventID, filename, item.ContentType, len(item.Payload), artifactDigest(item.Payload), blobKey, body, now)
	return err
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

func (p *PostgresStore) ListClientReports(projectID string) []ClientReport {
	rows, err := p.db.Query(`SELECT id, project_id, received_at, report_timestamp, discarded_events FROM sentryx_client_reports WHERE ($1 = '' OR project_id=$1) ORDER BY received_at DESC`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]ClientReport, 0)
	for rows.Next() {
		var report ClientReport
		var id int64
		var discarded []byte
		if rows.Scan(&id, &report.ProjectID, &report.ReceivedAt, &report.Timestamp, &discarded) == nil {
			report.ID = fmt.Sprint(id)
			_ = json.Unmarshal(discarded, &report.DiscardedEvents)
			result = append(result, report)
		}
	}
	return result
}

func (p *PostgresStore) ListAttachments(projectID, eventID string) []Attachment {
	rows, err := p.db.Query(`SELECT id, project_id, COALESCE(event_id, ''), filename, COALESCE(content_type, ''), size, sha256, COALESCE(blob_key, ''), created_at FROM sentryx_attachments WHERE ($1 = '' OR project_id=$1) AND ($2 = '' OR event_id=$2) ORDER BY created_at DESC`, projectID, eventID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Attachment, 0)
	for rows.Next() {
		var attachment Attachment
		if rows.Scan(&attachment.ID, &attachment.ProjectID, &attachment.EventID, &attachment.Filename, &attachment.ContentType, &attachment.Size, &attachment.SHA256, &attachment.BlobKey, &attachment.CreatedAt) == nil {
			result = append(result, attachment)
		}
	}
	return result
}

func (p *PostgresStore) GetAttachment(projectID, id string) (Attachment, []byte, bool) {
	var attachment Attachment
	var body []byte
	var blobKey sql.NullString
	if err := p.db.QueryRow(`SELECT id, project_id, COALESCE(event_id, ''), filename, COALESCE(content_type, ''), size, sha256, blob_key, body, created_at FROM sentryx_attachments WHERE id=$1 AND ($2 = '' OR project_id=$2)`, id, projectID).Scan(&attachment.ID, &attachment.ProjectID, &attachment.EventID, &attachment.Filename, &attachment.ContentType, &attachment.Size, &attachment.SHA256, &blobKey, &body, &attachment.CreatedAt); err != nil {
		return Attachment{}, nil, false
	}
	attachment.BlobKey = blobKey.String
	if len(body) == 0 && blobKey.Valid && p.artifacts != nil && p.artifacts.blob != nil {
		body, _ = p.artifacts.blob.Get(context.Background(), blobKey.String)
	}
	return attachment, body, true
}

func (p *PostgresStore) ListSignals(projectID, kind string) []StoredSignal {
	rows, err := p.db.Query(`SELECT id, project_id, COALESCE(event_id, ''), kind, received_at, schema_version, COALESCE(content_type, ''), size, COALESCE(blob_key, ''), payload FROM sentryx_signals WHERE ($1 = '' OR project_id=$1) AND ($2 = '' OR kind=$2) ORDER BY received_at DESC`, projectID, kind)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]StoredSignal, 0)
	for rows.Next() {
		var signal StoredSignal
		if rows.Scan(&signal.ID, &signal.ProjectID, &signal.EventID, &signal.Kind, &signal.ReceivedAt, &signal.Schema, &signal.ContentType, &signal.Size, &signal.BlobKey, &signal.Payload) == nil {
			result = append(result, signal)
		}
	}
	return result
}

func (p *PostgresStore) ListReleases(projectID string) []Release {
	rows, err := p.db.Query(`SELECT project_id, version, created_at FROM sentryx_releases WHERE ($1 = '' OR project_id=$1) ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Release, 0)
	for rows.Next() {
		var release Release
		if rows.Scan(&release.ProjectID, &release.Version, &release.CreatedAt) == nil {
			result = append(result, release)
		}
	}
	return result
}

func (p *PostgresStore) UpsertRelease(projectID, version string) Release {
	var release Release
	if err := p.db.QueryRow(`INSERT INTO sentryx_releases (project_id, version) VALUES ($1, $2) ON CONFLICT (project_id, version) DO UPDATE SET version=EXCLUDED.version RETURNING project_id, version, created_at`, projectID, version).Scan(&release.ProjectID, &release.Version, &release.CreatedAt); err != nil {
		return Release{ProjectID: projectID, Version: version, CreatedAt: timeNowUTC()}
	}
	return release
}

func (p *PostgresStore) ListArtifacts(projectID, release string) []ArtifactInfo {
	if p.artifacts == nil {
		return nil
	}
	return p.artifacts.List(projectID, release)
}

func (p *PostgresStore) DeleteArtifact(projectID, release, dist, name string) bool {
	if p.artifacts == nil {
		return false
	}
	return p.artifacts.Delete(projectID, release, dist, name)
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
