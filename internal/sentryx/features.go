package sentryx

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// QueryOptions is shared by the HTTP API and both storage implementations.
// Cursor values are opaque to clients and encode the last sort key plus ID.
type QueryOptions struct {
	ProjectID   string
	IssueID     string
	Start       time.Time
	End         time.Time
	Query       string
	Level       string
	Environment string
	Release     string
	Status      string
	Sort        string
	Cursor      string
	Limit       int
}

type IssuePage struct {
	Items      []Issue
	NextCursor string
}

type EventPage struct {
	Items      []Event
	NextCursor string
}

type PagedStore interface {
	ListIssuesPage(QueryOptions) IssuePage
	ListEventsPage(QueryOptions) EventPage
}

type IssueStateStore interface {
	SetIssueStatus(issueID, status, resolvedInRelease string) (Issue, error)
}

type SeriesPoint struct {
	Bucket time.Time `json:"bucket"`
	Count  int64     `json:"count"`
	Users  int64     `json:"users"`
}

type TagValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

type AnalyticsStore interface {
	IssueSeries(projectID, issueID string, start, end time.Time, resolution time.Duration) []SeriesPoint
	IssueTagValues(projectID, issueID, key string, start, end time.Time, limit int) []TagValueCount
	ProjectStats(projectID string, start, end time.Time) map[string]int64
}

type AlertRule struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	Name            string            `json:"name"`
	Condition       string            `json:"condition"`
	Threshold       int64             `json:"threshold"`
	WindowMinutes   int               `json:"window_minutes"`
	Filters         map[string]string `json:"filters,omitempty"`
	Actions         []AlertAction     `json:"actions,omitempty"`
	CooldownMinutes int               `json:"cooldown_minutes"`
	Enabled         bool              `json:"enabled"`
	LastTriggeredAt *time.Time        `json:"last_triggered_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type AlertAction struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
}

type AlertStore interface {
	ListAlertRules(projectID string) []AlertRule
	CreateAlertRule(rule AlertRule) AlertRule
	UpdateAlertRule(rule AlertRule) (AlertRule, bool)
	DeleteAlertRule(projectID, id string) bool
}

func defaultQueryOptions(options QueryOptions) QueryOptions {
	if options.Limit <= 0 || options.Limit > 100 {
		options.Limit = 50
	}
	if options.Sort == "" {
		options.Sort = "last_seen"
	}
	switch options.Sort {
	case "last_seen", "first_seen", "count", "users":
	default:
		options.Sort = "last_seen"
	}
	return options
}

func encodeCursor(sortKey string, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sortKey + "\x00" + id))
}

func decodeCursor(value string) (string, string, bool) {
	if value == "" {
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(decoded), "\x00", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func queryMatchesIssue(issue Issue, options QueryOptions) bool {
	if options.ProjectID != "" && issue.ProjectID != options.ProjectID {
		return false
	}
	if options.Level != "" && issue.Level != options.Level {
		return false
	}
	if options.Status != "" && issue.Status != options.Status {
		return false
	}
	if !options.Start.IsZero() && issue.LastSeen.Before(options.Start) {
		return false
	}
	if !options.End.IsZero() && !issue.FirstSeen.Before(options.End) {
		return false
	}
	for _, token := range strings.Fields(options.Query) {
		parts := strings.SplitN(token, ":", 2)
		if len(parts) != 2 {
			if !strings.Contains(strings.ToLower(issue.Title), strings.ToLower(token)) {
				return false
			}
			continue
		}
		value := strings.ToLower(parts[1])
		switch strings.ToLower(parts[0]) {
		case "is":
			if issue.Status != value {
				return false
			}
		case "level":
			if strings.ToLower(issue.Level) != value {
				return false
			}
		case "title":
			if !strings.Contains(strings.ToLower(issue.Title), value) {
				return false
			}
		}
	}
	return true
}

func issueSortKey(issue Issue, sortName string) string {
	switch sortName {
	case "first_seen":
		return issue.FirstSeen.UTC().Format(time.RFC3339Nano)
	case "count", "users":
		return fmt.Sprintf("%020d", issue.Count)
	default:
		return issue.LastSeen.UTC().Format(time.RFC3339Nano)
	}
}

func eventMatches(event Event, options QueryOptions) bool {
	if options.ProjectID != "" && event.ProjectID != options.ProjectID {
		return false
	}
	if options.IssueID != "" && event.IssueID != options.IssueID {
		return false
	}
	when := event.ReceivedAt
	if !options.Start.IsZero() && when.Before(options.Start) {
		return false
	}
	if !options.End.IsZero() && !when.Before(options.End) {
		return false
	}
	if options.Level != "" && event.Level != options.Level {
		return false
	}
	if options.Environment != "" && event.Environment != options.Environment {
		return false
	}
	if options.Release != "" && event.Release != options.Release {
		return false
	}
	return true
}

func (s *Store) ListIssuesPage(options QueryOptions) IssuePage {
	options = defaultQueryOptions(options)
	items := s.ListIssues(options.ProjectID)
	filtered := items[:0]
	for _, issue := range items {
		if queryMatchesIssue(issue, options) {
			filtered = append(filtered, issue)
		}
	}
	items = filtered
	sort.SliceStable(items, func(i, j int) bool {
		left, right := issueSortKey(items[i], options.Sort), issueSortKey(items[j], options.Sort)
		if left == right {
			return items[i].ID < items[j].ID
		}
		return left > right
	})
	start := 0
	if key, id, ok := decodeCursor(options.Cursor); ok {
		for index, issue := range items {
			if issueSortKey(issue, options.Sort) == key && issue.ID == id {
				start = index + 1
				break
			}
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + options.Limit
	if end > len(items) {
		end = len(items)
	}
	page := IssuePage{Items: append([]Issue(nil), items[start:end]...)}
	if end < len(items) {
		last := items[end-1]
		page.NextCursor = encodeCursor(issueSortKey(last, options.Sort), last.ID)
	}
	return page
}

func (s *Store) ListEventsPage(options QueryOptions) EventPage {
	options = defaultQueryOptions(options)
	items := s.ListEvents(options.ProjectID, options.IssueID)
	filtered := items[:0]
	for _, event := range items {
		if eventMatches(event, options) {
			filtered = append(filtered, event)
		}
	}
	items = filtered
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ReceivedAt.Equal(items[j].ReceivedAt) {
			return items[i].EventID < items[j].EventID
		}
		return items[i].ReceivedAt.After(items[j].ReceivedAt)
	})
	start := 0
	if key, id, ok := decodeCursor(options.Cursor); ok {
		for index, event := range items {
			if event.ReceivedAt.UTC().Format(time.RFC3339Nano) == key && event.EventID == id {
				start = index + 1
				break
			}
		}
	}
	end := start + options.Limit
	if end > len(items) {
		end = len(items)
	}
	page := EventPage{Items: append([]Event(nil), items[start:end]...)}
	if end < len(items) {
		last := items[end-1]
		page.NextCursor = encodeCursor(last.ReceivedAt.UTC().Format(time.RFC3339Nano), last.EventID)
	}
	return page
}

func (s *Store) SetIssueStatus(issueID, status, resolvedInRelease string) (Issue, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "resolved" && status != "unresolved" && status != "ignored" {
		return Issue{}, fmt.Errorf("invalid issue status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.issues[issueID]
	if !ok {
		return Issue{}, fmt.Errorf("issue not found")
	}
	issue.Status = status
	issue.StatusChangedAt = time.Now().UTC()
	issue.ResolvedInRelease = resolvedInRelease
	issue.Regression = false
	return *issue, nil
}

func (s *Store) IssueSeries(projectID, issueID string, start, end time.Time, resolution time.Duration) []SeriesPoint {
	if resolution <= 0 {
		resolution = time.Hour
	}
	counts := make(map[time.Time]*SeriesPoint)
	for _, event := range s.ListEvents(projectID, issueID) {
		if !eventMatches(event, QueryOptions{ProjectID: projectID, IssueID: issueID, Start: start, End: end}) {
			continue
		}
		bucket := event.ReceivedAt.UTC().Truncate(resolution)
		point := counts[bucket]
		if point == nil {
			point = &SeriesPoint{Bucket: bucket}
			counts[bucket] = point
		}
		point.Count++
		if event.User != nil && (event.User.ID != "" || event.User.Email != "") {
			point.Users++
		}
	}
	result := make([]SeriesPoint, 0, len(counts))
	for _, point := range counts {
		result = append(result, *point)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Bucket.Before(result[j].Bucket) })
	return result
}

func (s *Store) IssueTagValues(projectID, issueID, key string, start, end time.Time, limit int) []TagValueCount {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	counts := make(map[string]int64)
	for _, event := range s.ListEvents(projectID, issueID) {
		if eventMatches(event, QueryOptions{ProjectID: projectID, IssueID: issueID, Start: start, End: end}) {
			if value, ok := event.Tags[key]; ok {
				counts[value]++
			}
		}
	}
	result := make([]TagValueCount, 0, len(counts))
	for value, count := range counts {
		result = append(result, TagValueCount{Value: value, Count: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Count > result[j].Count })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (s *Store) ProjectStats(projectID string, start, end time.Time) map[string]int64 {
	result := map[string]int64{"errors": 0, "issues": 0, "users": 0}
	users := make(map[string]struct{})
	for _, event := range s.ListEvents(projectID, "") {
		if !eventMatches(event, QueryOptions{ProjectID: projectID, Start: start, End: end}) {
			continue
		}
		result["errors"]++
		if event.User != nil {
			key := event.User.ID
			if key == "" {
				key = event.User.Email
			}
			if key != "" {
				users[key] = struct{}{}
			}
		}
	}
	result["users"] = int64(len(users))
	result["issues"] = int64(len(s.ListIssues(projectID)))
	return result
}

func (s *Store) ListAlertRules(projectID string) []AlertRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]AlertRule, 0)
	for _, rule := range s.alertRules {
		if projectID == "" || rule.ProjectID == projectID {
			result = append(result, rule)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (s *Store) CreateAlertRule(rule AlertRule) AlertRule {
	rule = normalizeAlertRule(rule)
	s.mu.Lock()
	s.alertRules[rule.ID] = rule
	s.mu.Unlock()
	return rule
}

func (s *Store) UpdateAlertRule(rule AlertRule) (AlertRule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alertRules[rule.ID]; !ok {
		return AlertRule{}, false
	}
	rule = normalizeAlertRule(rule)
	s.alertRules[rule.ID] = rule
	return rule, true
}

func (s *Store) DeleteAlertRule(projectID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.alertRules[id]
	if !ok || (projectID != "" && rule.ProjectID != projectID) {
		return false
	}
	delete(s.alertRules, id)
	return true
}

type Metrics struct {
	mu       sync.RWMutex
	counters map[string]*atomic.Uint64
}

func NewMetrics() *Metrics { return &Metrics{counters: make(map[string]*atomic.Uint64)} }

func (m *Metrics) Inc(name string, labels map[string]string) {
	if m == nil {
		return
	}
	key := metricKey(name, labels)
	m.mu.RLock()
	counter := m.counters[key]
	m.mu.RUnlock()
	if counter == nil {
		m.mu.Lock()
		counter = m.counters[key]
		if counter == nil {
			counter = &atomic.Uint64{}
			m.counters[key] = counter
		}
		m.mu.Unlock()
	}
	counter.Add(1)
}

func (m *Metrics) Snapshot() map[string]uint64 {
	result := make(map[string]uint64)
	if m == nil {
		return result
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, value := range m.counters {
		result[key] = value.Load()
	}
	return result
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(name)
	builder.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(key)
		builder.WriteString("=\"")
		builder.WriteString(strings.ReplaceAll(labels[key], "\"", "\\\""))
		builder.WriteString("\"")
	}
	builder.WriteByte('}')
	return builder.String()
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for key, value := range m.Snapshot() {
		name := key
		labels := ""
		if index := strings.IndexByte(key, '{'); index >= 0 {
			name, labels = key[:index], key[index:]
		}
		fmt.Fprintf(w, "%s%s %d\n", name, labels, value)
	}
}

var DefaultMetrics = NewMetrics()

func hashSample(value string) uint64 {
	digest := sha256.Sum256([]byte(value))
	return uint64(digest[0])<<8 | uint64(digest[1])
}

func parseTimeQuery(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC()
	}
	return time.Time{}
}

func queryOptionsFromRequest(r *http.Request) QueryOptions {
	query := r.URL.Query()
	return QueryOptions{
		ProjectID: query.Get("project"), IssueID: query.Get("issue"),
		Start: parseTimeQuery(query.Get("start")), End: parseTimeQuery(query.Get("end")),
		Query: query.Get("query"), Level: query.Get("level"), Environment: query.Get("environment"),
		Release: query.Get("release"), Status: query.Get("status"), Sort: query.Get("sort"),
		Cursor: query.Get("cursor"), Limit: intQuery(query.Get("limit"), 50),
	}
}

func intQuery(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseResolution(value string) time.Duration {
	switch strings.ToLower(value) {
	case "1m":
		return time.Minute
	case "5m":
		return 5 * time.Minute
	case "1d", "day":
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

func groupingVersionNumber() int {
	value := strings.TrimPrefix(groupingVersion(), "v")
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 2
	}
	return parsed
}

func writePageHeaders(w http.ResponseWriter, next string) {
	if next != "" {
		w.Header().Set("X-Next-Cursor", next)
		w.Header().Set("Link", `; rel="next"; cursor="`+next+`"`)
	}
}

func alertRuleID(projectID string) string {
	digest := sha256.Sum256([]byte(projectID + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)))
	return hex.EncodeToString(digest[:])[:16]
}

func normalizeAlertRule(rule AlertRule) AlertRule {
	if rule.ID == "" {
		rule.ID = alertRuleID(rule.ProjectID)
	}
	if rule.Threshold <= 0 {
		rule.Threshold = 1
	}
	if rule.WindowMinutes <= 0 {
		rule.WindowMinutes = 60
	}
	if rule.CooldownMinutes < 0 {
		rule.CooldownMinutes = 0
	}
	if rule.Filters == nil {
		rule.Filters = map[string]string{}
	}
	if rule.Actions == nil {
		rule.Actions = []AlertAction{}
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now().UTC()
	}
	rule.UpdatedAt = time.Now().UTC()
	return rule
}

func decodeFilters(value []byte) map[string]string {
	var filters map[string]string
	_ = json.Unmarshal(value, &filters)
	return filters
}
