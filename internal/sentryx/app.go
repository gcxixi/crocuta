package sentryx

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultMaxEnvelopeBytes int64 = 5 << 20

// Event is the first Canonical Error shape. It deliberately keeps the raw
// fields needed for later language-specific processors without exposing the
// Sentry wire format to the issue store.
type Event struct {
	ProjectID   string            `json:"project_id"`
	EventID     string            `json:"event_id"`
	OccurredAt  time.Time         `json:"occurred_at"`
	ReceivedAt  time.Time         `json:"received_at"`
	Platform    string            `json:"platform,omitempty"`
	Level       string            `json:"level,omitempty"`
	Release     string            `json:"release,omitempty"`
	Dist        string            `json:"dist,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Title       string            `json:"title"`
	Message     string            `json:"message,omitempty"`
	Culprit     string            `json:"culprit,omitempty"`
	Fingerprint []string          `json:"fingerprint,omitempty"`
	Exception   json.RawMessage   `json:"exception,omitempty"`
	Stacktrace  json.RawMessage   `json:"stacktrace,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Raw         map[string]any    `json:"raw,omitempty"`
	IssueID     string            `json:"issue_id"`
}

type Issue struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Title       string    `json:"title"`
	Level       string    `json:"level,omitempty"`
	Count       int       `json:"count"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	LatestEvent string    `json:"latest_event_id"`
	GroupHash   string    `json:"group_hash"`
}

type Store struct {
	mu        sync.RWMutex
	events    map[string]Event
	issues    map[string]*Issue
	groupToID map[string]string
}

func NewStore() *Store {
	return &Store{events: make(map[string]Event), issues: make(map[string]*Issue), groupToID: make(map[string]string)}
}

type App struct {
	Store       *Store
	MaxEnvelope int64
	RelayToken  string
	Logger      *slog.Logger
}

func NewApp(store *Store) *App {
	if store == nil {
		store = NewStore()
	}
	return &App{Store: store, MaxEnvelope: DefaultMaxEnvelopeBytes, Logger: slog.Default()}
}

func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/live" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			a.handleAPI(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func (a *App) handleAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "issues" && r.Method == http.MethodGet {
		projectID := r.URL.Query().Get("project")
		writeJSON(w, http.StatusOK, a.Store.ListIssues(projectID))
		return
	}
	if len(parts) < 3 || parts[0] != "api" {
		http.NotFound(w, r)
		return
	}
	projectID := parts[1]
	if r.Method != http.MethodPost || (parts[2] != "envelope" && parts[2] != "store") {
		http.NotFound(w, r)
		return
	}
	if a.RelayToken != "" && r.Header.Get("X-SentryX-Relay-Token") != a.RelayToken {
		http.Error(w, "relay authentication required", http.StatusUnauthorized)
		return
	}
	key := r.URL.Query().Get("sentry_key")
	if key == "" {
		key = parseSentryAuth(r.Header.Get("X-Sentry-Auth"), "sentry_key")
	}
	if key == "" {
		http.Error(w, "missing sentry key", http.StatusUnauthorized)
		return
	}
	if r.Body == nil {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.MaxEnvelope))
	if err != nil {
		http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
		return
	}
	if parts[2] == "store" {
		body = wrapStoreEvent(body)
	}
	accepted, err := a.Store.Ingest(projectID, key, body)
	if err != nil {
		a.Logger.Warn("envelope rejected", "error", err, "project_id", projectID)
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	w.Header().Set("X-SentryX-Accepted", fmt.Sprintf("%d", accepted))
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted})
}

func (s *Store) Ingest(projectID, _ string, body []byte) (int, error) {
	items, err := parseEnvelope(body)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	accepted := 0
	for _, item := range items {
		if item.Type != "event" && item.Type != "error" {
			continue
		}
		event, err := decodeEvent(projectID, item.Payload, now)
		if err != nil {
			continue
		}
		groupHash := groupingHash(event)
		s.mu.Lock()
		key := projectID + ":" + event.EventID
		if _, exists := s.events[key]; exists {
			s.mu.Unlock()
			continue
		}
		issueID := s.groupToID[projectID+":"+groupHash]
		if issueID == "" {
			issueID = shortHash(projectID + ":" + groupHash)
			s.groupToID[projectID+":"+groupHash] = issueID
			s.issues[issueID] = &Issue{ID: issueID, ProjectID: projectID, Title: event.Title, Level: event.Level, FirstSeen: now, GroupHash: groupHash}
		}
		issue := s.issues[issueID]
		issue.Count++
		issue.LastSeen = now
		issue.LatestEvent = event.EventID
		event.IssueID = issueID
		s.events[key] = event
		s.mu.Unlock()
		accepted++
	}
	return accepted, nil
}

func (s *Store) ListIssues(projectID string) []Issue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Issue, 0)
	for _, issue := range s.issues {
		if projectID == "" || issue.ProjectID == projectID {
			result = append(result, *issue)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastSeen.After(result[j].LastSeen) })
	return result
}

func (s *Store) ListEvents(projectID, issueID string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Event, 0)
	for _, event := range s.events {
		if (projectID == "" || event.ProjectID == projectID) && (issueID == "" || event.IssueID == issueID) {
			result = append(result, event)
		}
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseSentryAuth(value, wanted string) string {
	for _, part := range strings.Split(value, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) == 2 && strings.TrimSpace(pair[0]) == wanted {
			return strings.Trim(strings.TrimSpace(pair[1]), "\"")
		}
	}
	return ""
}

func wrapStoreEvent(body []byte) []byte {
	header, _ := json.Marshal(map[string]any{"event_id": ""})
	item, _ := json.Marshal(map[string]any{"type": "event", "length": len(body)})
	return append(append(append(append(header, '\n'), item...), '\n'), body...)
}

func shortHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])[:16]
}

var (
	dynamicNumber = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b|\b\d{2,}\b`)
	dynamicUUID   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f-]{27,}\b`)
	dynamicURL    = regexp.MustCompile(`https?://[^\s]+`)
)

func normalizeMessage(message string) string {
	message = dynamicUUID.ReplaceAllString(message, "<uuid>")
	message = dynamicURL.ReplaceAllString(message, "<url>")
	message = dynamicNumber.ReplaceAllString(message, "<n>")
	return strings.Join(strings.Fields(message), " ")
}

type exceptionValue struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Stacktrace struct {
		Frames []struct {
			Filename string `json:"filename"`
			Function string `json:"function"`
			InApp    bool   `json:"in_app"`
		} `json:"frames"`
	} `json:"stacktrace"`
}

func decodeEvent(projectID string, payload []byte, now time.Time) (Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, err
	}
	event := Event{ProjectID: projectID, ReceivedAt: now, OccurredAt: now, Raw: raw, Platform: stringValue(raw["platform"]), Level: stringValue(raw["level"]), Release: stringValue(raw["release"]), Dist: stringValue(raw["dist"]), Environment: stringValue(raw["environment"]), Message: stringValue(raw["message"]), Culprit: stringValue(raw["culprit"])}
	event.EventID = stringValue(raw["event_id"])
	if event.EventID == "" {
		event.EventID = randomID()
	}
	if ts := stringValue(raw["timestamp"]); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			event.OccurredAt = parsed
		}
	}
	if logentry, ok := raw["logentry"].(map[string]any); ok && event.Message == "" {
		event.Message = stringValue(logentry["formatted"])
	}
	if exception, ok := raw["exception"].(map[string]any); ok {
		values, ok := exception["values"].([]any)
		if !ok || len(values) == 0 {
			values = nil
		}
		if len(values) > 0 {
			var last exceptionValue
			encoded, _ := json.Marshal(values[len(values)-1])
			_ = json.Unmarshal(encoded, &last)
			if last.Value != "" {
				event.Message = last.Value
			}
			event.Exception = encoded
			if len(last.Stacktrace.Frames) > 0 {
				frame := last.Stacktrace.Frames[len(last.Stacktrace.Frames)-1]
				event.Culprit = frame.Function + "@" + frame.Filename
			}
		}
	}
	if event.Message == "" {
		event.Message = event.Culprit
	}
	event.Title = event.Message
	if event.Title == "" {
		event.Title = "JavaScript error"
	}
	if fp, ok := raw["fingerprint"].([]any); ok {
		for _, value := range fp {
			event.Fingerprint = append(event.Fingerprint, stringValue(value))
		}
	}
	return event, nil
}

func groupingHash(event Event) string {
	if len(event.Fingerprint) > 0 && !containsDefault(event.Fingerprint) {
		return "fp:" + strings.Join(event.Fingerprint, "|")
	}
	parts := []string{"v1", event.Platform, normalizeMessage(event.Message)}
	if event.Exception != nil {
		var ex exceptionValue
		if json.Unmarshal(event.Exception, &ex) == nil {
			parts = append(parts, ex.Type)
			for i := len(ex.Stacktrace.Frames) - 1; i >= 0; i-- {
				frame := ex.Stacktrace.Frames[i]
				if frame.InApp || i == len(ex.Stacktrace.Frames)-1 {
					parts = append(parts, normalizePath(frame.Filename), frame.Function)
					break
				}
			}
		}
	}
	if event.Culprit != "" {
		parts = append(parts, normalizePath(event.Culprit))
	}
	return shortHash(strings.Join(parts, "\x00"))
}

func containsDefault(values []string) bool {
	for _, value := range values {
		if value == "{{ default }}" {
			return true
		}
	}
	return false
}

func normalizePath(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	value = strings.TrimSuffix(value, ".js")
	return strings.ReplaceAll(value, "\\", "/")
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(data[:])
}

type envelopeItem struct {
	Type    string
	Payload []byte
}

func parseEnvelope(body []byte) ([]envelopeItem, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("empty envelope")
	}
	index := 0
	if _, next, err := nextLine(body, index); err != nil {
		return nil, err
	} else {
		index = next
	}
	var items []envelopeItem
	for index < len(body) {
		line, next, err := nextLine(body, index)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			index = next
			continue
		}
		var header struct {
			Type   string `json:"type"`
			Length int    `json:"length"`
		}
		if err := json.Unmarshal(line, &header); err != nil || header.Type == "" {
			return nil, errors.New("invalid item header")
		}
		index = next
		if header.Length > 0 {
			if header.Length > len(body)-index {
				return nil, errors.New("truncated item")
			}
			items = append(items, envelopeItem{Type: header.Type, Payload: body[index : index+header.Length]})
			index += header.Length
			if index < len(body) && body[index] == '\n' {
				index++
			}
			continue
		}
		if header.Type == "event" || header.Type == "error" {
			decoder := json.NewDecoder(bytes.NewReader(body[index:]))
			var payload json.RawMessage
			if err := decoder.Decode(&payload); err != nil {
				return nil, err
			}
			consumed := int(decoder.InputOffset())
			items = append(items, envelopeItem{Type: header.Type, Payload: payload})
			index += consumed
			if index < len(body) && body[index] == '\n' {
				index++
			}
			continue
		}
		items = append(items, envelopeItem{Type: header.Type, Payload: bytes.TrimSpace(body[index:])})
		break
	}
	return items, nil
}

func nextLine(body []byte, index int) ([]byte, int, error) {
	if index >= len(body) {
		return nil, index, io.ErrUnexpectedEOF
	}
	end := bytes.IndexByte(body[index:], '\n')
	if end < 0 {
		return bytes.TrimSuffix(body[index:], []byte{'\r'}), len(body), nil
	}
	end += index
	return bytes.TrimSuffix(body[index:end], []byte{'\r'}), end + 1, nil
}
