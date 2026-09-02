package sentryx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
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

func (p *PostgresStore) Ready(ctx context.Context) error { return p.db.PingContext(ctx) }

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
		event = scrubEventWithConfig(event, p.PII)
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
		groupVersion := groupingVersionNumber()
		issueID := shortHash(projectID + ":" + groupHash)
		mappedIssueID := p.lookupGroupingIssue(projectID, groupVersion, groupHash, event)
		if mappedIssueID != "" {
			issueID = mappedIssueID
		}
		event.IssueID = issueID
		tx, err := p.db.BeginTx(context.Background(), nil)
		if err != nil {
			return accepted, err
		}
		var returnedIssueID string
		if mappedIssueID != "" {
			err = tx.QueryRow(`
				UPDATE sentryx_issues
				SET count=count+1, last_seen=$2, latest_event_id=$3,
				  status=CASE WHEN status='resolved' THEN 'unresolved' ELSE status END,
				  regression=CASE WHEN status='resolved' THEN true ELSE regression END,
				  status_changed_at=CASE WHEN status='resolved' THEN $2 ELSE status_changed_at END
				WHERE id=$1 RETURNING id`, issueID, now, event.EventID).Scan(&returnedIssueID)
		} else {
			err = tx.QueryRow(`
			INSERT INTO sentryx_issues
			  (id, project_id, title, level, count, first_seen, last_seen, latest_event_id, grouping_version, group_hash, status, status_changed_at)
			VALUES ($1, $2, $3, $4, 1, $5, $5, $6, $8, $7, 'unresolved', $5)
			ON CONFLICT (project_id, grouping_version, group_hash)
			DO UPDATE SET count = sentryx_issues.count + 1,
			  last_seen = EXCLUDED.last_seen, latest_event_id = EXCLUDED.latest_event_id,
			  status = CASE WHEN sentryx_issues.status = 'resolved' THEN 'unresolved' ELSE sentryx_issues.status END,
			  regression = CASE WHEN sentryx_issues.status = 'resolved' THEN true ELSE sentryx_issues.regression END,
			  status_changed_at = CASE WHEN sentryx_issues.status = 'resolved' THEN EXCLUDED.status_changed_at ELSE sentryx_issues.status_changed_at END
			RETURNING id`, issueID, projectID, event.Title, event.Level, now, event.EventID, groupHash, groupVersion).Scan(&returnedIssueID)
		}
		if err != nil {
			tx.Rollback()
			return accepted, err
		}
		result, err := tx.Exec(`
			INSERT INTO sentryx_events
			  (project_id, event_id, issue_id, occurred_at, received_at, canonical_json)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (project_id, event_id) DO NOTHING`,
			projectID, event.EventID, issueID, event.OccurredAt, event.ReceivedAt, mustJSON(event))
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
		_, _ = tx.Exec(`INSERT INTO sentryx_grouping_hashes (project_id, grouping_version, group_hash, issue_id, components) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, projectID, groupVersion, groupHash, returnedIssueID, mustJSON(GroupingComponents(event, groupingVersion())))
		if err := tx.Commit(); err != nil {
			return accepted, err
		}
		bucket := event.ReceivedAt.UTC().Truncate(time.Hour)
		_, _ = p.db.Exec(`INSERT INTO sentryx_issue_stats_hourly (project_id, issue_id, bucket, event_count, user_count) VALUES ($1,$2,$3,1,$4) ON CONFLICT (project_id,issue_id,bucket) DO UPDATE SET event_count=sentryx_issue_stats_hourly.event_count+1, user_count=sentryx_issue_stats_hourly.user_count+EXCLUDED.user_count`, projectID, returnedIssueID, bucket, userCount(event))
		accepted++
	}
	return accepted, nil
}

func (p *PostgresStore) lookupGroupingIssue(projectID string, version int, groupHash string, event Event) string {
	var issueID string
	if err := p.db.QueryRow(`SELECT issue_id FROM sentryx_grouping_hashes WHERE project_id=$1 AND grouping_version=$2 AND group_hash=$3`, projectID, version, groupHash).Scan(&issueID); err == nil {
		return issueID
	}
	if version > 1 {
		legacyHash := GroupingHashForVersion(event, "v1")
		if err := p.db.QueryRow(`SELECT issue_id FROM sentryx_grouping_hashes WHERE project_id=$1 AND grouping_version=1 AND group_hash=$2`, projectID, legacyHash).Scan(&issueID); err == nil {
			return issueID
		}
	}
	return ""
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
	rows, err := p.db.Query(`SELECT id, project_id, title, level, count, first_seen, last_seen, latest_event_id, group_hash, status, status_changed_at, COALESCE(resolved_in_release,''), ignore_until, regression, ignore_count, ignore_window FROM sentryx_issues WHERE ($1 = '' OR project_id=$1) ORDER BY last_seen DESC`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]Issue, 0)
	for rows.Next() {
		var issue Issue
		if err := rows.Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.Count, &issue.FirstSeen, &issue.LastSeen, &issue.LatestEvent, &issue.GroupHash, &issue.Status, &issue.StatusChangedAt, &issue.ResolvedInRelease, &issue.IgnoreUntil, &issue.Regression, &issue.IgnoreCount, &issue.IgnoreWindow); err == nil {
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

func (p *PostgresStore) ListIssuesPage(options QueryOptions) IssuePage {
	options = defaultQueryOptions(options)
	args := []any{options.ProjectID}
	where := []string{"($1 = '' OR project_id=$1)"}
	add := func(value any, clause string) {
		args = append(args, value)
		where = append(where, strings.Replace(clause, "?", fmt.Sprintf("$%d", len(args)), 1))
	}
	if !options.Start.IsZero() {
		add(options.Start, "last_seen >= ?")
	}
	if !options.End.IsZero() {
		add(options.End, "first_seen < ?")
	}
	if options.Level != "" {
		add(options.Level, "level = ?")
	}
	if options.Status != "" {
		add(options.Status, "status = ?")
	}
	if options.Query != "" {
		for _, token := range strings.Fields(options.Query) {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) == 2 {
				switch strings.ToLower(parts[0]) {
				case "is":
					add(parts[1], "status = ?")
				case "level":
					add(parts[1], "level = ?")
				case "title":
					add("%"+parts[1]+"%", "title ILIKE ?")
				}
			} else {
				add("%"+token+"%", "title ILIKE ?")
			}
		}
	}
	if key, id, ok := decodeCursor(options.Cursor); ok {
		switch options.Sort {
		case "first_seen":
			if parsed, err := time.Parse(time.RFC3339Nano, key); err == nil {
				args = append(args, parsed, id)
				where = append(where, fmt.Sprintf("(first_seen < $%d OR (first_seen = $%d AND id < $%d))", len(args)-1, len(args)-1, len(args)))
			}
		case "count", "users":
			countKey := strings.TrimLeft(key, "0")
			if countKey == "" {
				countKey = "0"
			}
			if parsed, err := strconv.ParseInt(countKey, 10, 64); err == nil {
				args = append(args, parsed, id)
				where = append(where, fmt.Sprintf("(count < $%d OR (count = $%d AND id < $%d))", len(args)-1, len(args)-1, len(args)))
			}
		default:
			if parsed, err := time.Parse(time.RFC3339Nano, key); err == nil {
				args = append(args, parsed, id)
				where = append(where, fmt.Sprintf("(last_seen < $%d OR (last_seen = $%d AND id < $%d))", len(args)-1, len(args)-1, len(args)))
			}
		}
	}
	order := "last_seen DESC, id DESC"
	if options.Sort == "first_seen" {
		order = "first_seen DESC, id DESC"
	}
	if options.Sort == "count" || options.Sort == "users" {
		order = "count DESC, id DESC"
	}
	query := `SELECT id, project_id, title, level, count, first_seen, last_seen, latest_event_id, group_hash, status, status_changed_at, COALESCE(resolved_in_release,''), ignore_until, regression, ignore_count, ignore_window FROM sentryx_issues WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order + ` LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, options.Limit+1)
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return IssuePage{}
	}
	defer rows.Close()
	result := make([]Issue, 0, options.Limit)
	for rows.Next() {
		var issue Issue
		if rows.Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.Count, &issue.FirstSeen, &issue.LastSeen, &issue.LatestEvent, &issue.GroupHash, &issue.Status, &issue.StatusChangedAt, &issue.ResolvedInRelease, &issue.IgnoreUntil, &issue.Regression, &issue.IgnoreCount, &issue.IgnoreWindow) == nil {
			result = append(result, issue)
		}
	}
	page := IssuePage{Items: result}
	if len(result) > options.Limit {
		last := result[options.Limit-1]
		page.Items = result[:options.Limit]
		page.NextCursor = encodeCursor(issueSortKey(last, options.Sort), last.ID)
	}
	return page
}

func (p *PostgresStore) ListEventsPage(options QueryOptions) EventPage {
	options = defaultQueryOptions(options)
	args := []any{options.ProjectID, options.IssueID}
	where := []string{"($1 = '' OR project_id=$1)", "($2 = '' OR issue_id=$2)"}
	add := func(value any, clause string) {
		args = append(args, value)
		where = append(where, strings.Replace(clause, "?", fmt.Sprintf("$%d", len(args)), 1))
	}
	if !options.Start.IsZero() {
		add(options.Start, "received_at >= ?")
	}
	if !options.End.IsZero() {
		add(options.End, "received_at < ?")
	}
	if options.Environment != "" {
		add(options.Environment, "canonical_json->>'environment' = ?")
	}
	if options.Release != "" {
		add(options.Release, "canonical_json->>'release' = ?")
	}
	if options.Level != "" {
		add(options.Level, "canonical_json->>'level' = ?")
	}
	query := `SELECT canonical_json, received_at, event_id FROM sentryx_events WHERE ` + strings.Join(where, " AND ") + ` ORDER BY received_at DESC, event_id DESC LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, options.Limit+1)
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return EventPage{}
	}
	defer rows.Close()
	result := make([]Event, 0, options.Limit)
	for rows.Next() {
		var raw []byte
		var received time.Time
		var id string
		var event Event
		if rows.Scan(&raw, &received, &id) == nil && json.Unmarshal(raw, &event) == nil {
			if event.ReceivedAt.IsZero() {
				event.ReceivedAt = received
			}
			result = append(result, event)
		}
	}
	page := EventPage{Items: result}
	if len(result) > options.Limit {
		last := result[options.Limit-1]
		page.Items = result[:options.Limit]
		page.NextCursor = encodeCursor(last.ReceivedAt.UTC().Format(time.RFC3339Nano), last.EventID)
	}
	return page
}

func (p *PostgresStore) SetIssueStatus(issueID, status, resolvedInRelease string) (Issue, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "resolved" && status != "unresolved" && status != "ignored" {
		return Issue{}, fmt.Errorf("invalid issue status")
	}
	var issue Issue
	err := p.db.QueryRow(`UPDATE sentryx_issues SET status=$1,status_changed_at=now(),resolved_in_release=NULLIF($2,''),regression=false,ignore_count=ignore_count+CASE WHEN $1='ignored' THEN 1 ELSE 0 END WHERE id=$3 RETURNING id,project_id,title,level,count,first_seen,last_seen,latest_event_id,group_hash,status,status_changed_at,COALESCE(resolved_in_release,''),ignore_until,regression,ignore_count,ignore_window`, status, resolvedInRelease, issueID).Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.Count, &issue.FirstSeen, &issue.LastSeen, &issue.LatestEvent, &issue.GroupHash, &issue.Status, &issue.StatusChangedAt, &issue.ResolvedInRelease, &issue.IgnoreUntil, &issue.Regression, &issue.IgnoreCount, &issue.IgnoreWindow)
	return issue, err
}

func userCount(event Event) int {
	if event.User != nil && (event.User.ID != "" || event.User.Email != "") {
		return 1
	}
	return 0
}

func (p *PostgresStore) IssueSeries(projectID, issueID string, start, end time.Time, resolution time.Duration) []SeriesPoint {
	if resolution <= 0 {
		resolution = time.Hour
	}
	rows, err := p.db.Query(`SELECT date_trunc('hour',bucket), SUM(event_count), SUM(user_count) FROM sentryx_issue_stats_hourly WHERE project_id=$1 AND issue_id=$2 AND ($3::timestamptz IS NULL OR bucket >= $3) AND ($4::timestamptz IS NULL OR bucket < $4) GROUP BY 1 ORDER BY 1`, projectID, issueID, nullableTime(start), nullableTime(end))
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []SeriesPoint{}
	for rows.Next() {
		var point SeriesPoint
		if rows.Scan(&point.Bucket, &point.Count, &point.Users) == nil {
			result = append(result, point)
		}
	}
	return result
}

func (p *PostgresStore) IssueTagValues(projectID, issueID, key string, start, end time.Time, limit int) []TagValueCount {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := p.db.Query(`SELECT COALESCE(canonical_json->'tags'->>$3,''), count(*) FROM sentryx_events WHERE project_id=$1 AND issue_id=$2 AND ($4::timestamptz IS NULL OR received_at >= $4) AND ($5::timestamptz IS NULL OR received_at < $5) GROUP BY 1 ORDER BY 2 DESC LIMIT $6`, projectID, issueID, key, nullableTime(start), nullableTime(end), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []TagValueCount{}
	for rows.Next() {
		var item TagValueCount
		if rows.Scan(&item.Value, &item.Count) == nil {
			result = append(result, item)
		}
	}
	return result
}

func (p *PostgresStore) ProjectStats(projectID string, start, end time.Time) map[string]int64 {
	result := map[string]int64{"errors": 0, "issues": 0, "users": 0}
	var errorsCount, issueCount, userCountValue int64
	_ = p.db.QueryRow(`SELECT count(*) FROM sentryx_events WHERE project_id=$1 AND ($2::timestamptz IS NULL OR received_at >= $2) AND ($3::timestamptz IS NULL OR received_at < $3)`, projectID, nullableTime(start), nullableTime(end)).Scan(&errorsCount)
	_ = p.db.QueryRow(`SELECT count(*) FROM sentryx_issues WHERE project_id=$1`, projectID).Scan(&issueCount)
	_ = p.db.QueryRow(`SELECT count(DISTINCT NULLIF(COALESCE(canonical_json->'user'->>'id',canonical_json->'user'->>'email'),'')) FROM sentryx_events WHERE project_id=$1`, projectID).Scan(&userCountValue)
	result["errors"], result["issues"], result["users"] = errorsCount, issueCount, userCountValue
	return result
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (p *PostgresStore) ListAlertRules(projectID string) []AlertRule {
	rows, err := p.db.Query(`SELECT id,project_id,name,condition,threshold,window_minutes,filters,actions,cooldown_minutes,enabled,last_triggered_at,created_at,updated_at FROM sentryx_alert_rules WHERE ($1='' OR project_id=$1) ORDER BY created_at`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := []AlertRule{}
	for rows.Next() {
		var rule AlertRule
		var filters, actions []byte
		if rows.Scan(&rule.ID, &rule.ProjectID, &rule.Name, &rule.Condition, &rule.Threshold, &rule.WindowMinutes, &filters, &actions, &rule.CooldownMinutes, &rule.Enabled, &rule.LastTriggeredAt, &rule.CreatedAt, &rule.UpdatedAt) == nil {
			rule.Filters = decodeFilters(filters)
			_ = json.Unmarshal(actions, &rule.Actions)
			result = append(result, rule)
		}
	}
	return result
}

func (p *PostgresStore) CreateAlertRule(rule AlertRule) AlertRule {
	rule = normalizeAlertRule(rule)
	_, err := p.db.Exec(`INSERT INTO sentryx_alert_rules (id,project_id,name,condition,threshold,window_minutes,filters,actions,cooldown_minutes,enabled,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`, rule.ID, rule.ProjectID, rule.Name, rule.Condition, rule.Threshold, rule.WindowMinutes, mustJSON(rule.Filters), mustJSON(rule.Actions), rule.CooldownMinutes, rule.Enabled, rule.CreatedAt)
	if err != nil {
		return AlertRule{}
	}
	return rule
}

func (p *PostgresStore) UpdateAlertRule(rule AlertRule) (AlertRule, bool) {
	rule = normalizeAlertRule(rule)
	result, err := p.db.Exec(`UPDATE sentryx_alert_rules SET name=$1,condition=$2,threshold=$3,window_minutes=$4,filters=$5,actions=$6,cooldown_minutes=$7,enabled=$8,updated_at=$9 WHERE id=$10 AND project_id=$11`, rule.Name, rule.Condition, rule.Threshold, rule.WindowMinutes, mustJSON(rule.Filters), mustJSON(rule.Actions), rule.CooldownMinutes, rule.Enabled, rule.UpdatedAt, rule.ID, rule.ProjectID)
	if err != nil {
		return AlertRule{}, false
	}
	count, _ := result.RowsAffected()
	return rule, count > 0
}

func (p *PostgresStore) DeleteAlertRule(projectID, id string) bool {
	result, err := p.db.Exec(`DELETE FROM sentryx_alert_rules WHERE id=$1 AND project_id=$2`, id, projectID)
	if err != nil {
		return false
	}
	count, _ := result.RowsAffected()
	return count > 0
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
