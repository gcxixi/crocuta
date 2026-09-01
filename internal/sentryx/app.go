package sentryx

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
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
	ProjectID           string            `json:"project_id"`
	EventID             string            `json:"event_id"`
	OccurredAt          time.Time         `json:"occurred_at"`
	ReceivedAt          time.Time         `json:"received_at"`
	Platform            string            `json:"platform,omitempty"`
	Level               string            `json:"level,omitempty"`
	Release             string            `json:"release,omitempty"`
	Dist                string            `json:"dist,omitempty"`
	Environment         string            `json:"environment,omitempty"`
	Title               string            `json:"title"`
	Message             string            `json:"message,omitempty"`
	Culprit             string            `json:"culprit,omitempty"`
	Logger              string            `json:"logger,omitempty"`
	Transaction         string            `json:"transaction,omitempty"`
	ServerName          string            `json:"server_name,omitempty"`
	Fingerprint         []string          `json:"fingerprint,omitempty"`
	Exception           json.RawMessage   `json:"exception,omitempty"`
	Stacktrace          json.RawMessage   `json:"stacktrace,omitempty"`
	Mechanism           json.RawMessage   `json:"mechanism,omitempty"`
	Frames              []StackFrame      `json:"frames,omitempty"`
	SymbolicatedFrames  []StackFrame      `json:"symbolicated_frames,omitempty"`
	SymbolicationStatus string            `json:"symbolication_status,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	Extra               map[string]any    `json:"extra,omitempty"`
	Contexts            map[string]any    `json:"contexts,omitempty"`
	Modules             map[string]string `json:"modules,omitempty"`
	User                *User             `json:"user,omitempty"`
	Request             *Request          `json:"request,omitempty"`
	Breadcrumbs         []Breadcrumb      `json:"breadcrumbs,omitempty"`
	SDK                 map[string]any    `json:"sdk,omitempty"`
	DebugMeta           map[string]any    `json:"debug_meta,omitempty"`
	Raw                 map[string]any    `json:"raw,omitempty"`
	IssueID             string            `json:"issue_id"`
}

// User, Request and Breadcrumb are intentionally JSON-compatible with the
// corresponding Sentry event fields. They are first-class canonical fields so
// consumers do not need to understand the original wire payload.
type User struct {
	ID        string `json:"id,omitempty"`
	Username  string `json:"username,omitempty"`
	Email     string `json:"email,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	Name      string `json:"name,omitempty"`
}

type Request struct {
	URL         string            `json:"url,omitempty"`
	Method      string            `json:"method,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	QueryString string            `json:"query_string,omitempty"`
	Data        any               `json:"data,omitempty"`
	Fragments   string            `json:"fragments,omitempty"`
}

type Breadcrumb struct {
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Type      string         `json:"type,omitempty"`
	Category  string         `json:"category,omitempty"`
	Message   string         `json:"message,omitempty"`
	Level     string         `json:"level,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type ClientReport struct {
	ID              string           `json:"id"`
	ProjectID       string           `json:"project_id"`
	ReceivedAt      time.Time        `json:"received_at"`
	Timestamp       time.Time        `json:"timestamp"`
	DiscardedEvents []DiscardedEvent `json:"discarded_events,omitempty"`
}

type DiscardedEvent struct {
	Reason   string `json:"reason,omitempty"`
	Category string `json:"category,omitempty"`
	Quantity int    `json:"quantity,omitempty"`
}

type Attachment struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	EventID     string    `json:"event_id,omitempty"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type,omitempty"`
	Size        int       `json:"size"`
	SHA256      string    `json:"sha256"`
	BlobKey     string    `json:"blob_key,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// StoredSignal is the extensibility boundary for non-error Sentry items. The
// first implementation stores the normalized JSON payload without trying to
// build a query model for every future product capability.
type StoredSignal struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	EventID     string          `json:"event_id,omitempty"`
	Kind        string          `json:"kind"`
	ReceivedAt  time.Time       `json:"received_at"`
	Payload     json.RawMessage `json:"payload"`
	Schema      int             `json:"schema_version"`
	ContentType string          `json:"content_type,omitempty"`
	Size        int             `json:"size,omitempty"`
	BlobKey     string          `json:"blob_key,omitempty"`
}

type Release struct {
	ProjectID string    `json:"project_id"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type ArtifactInfo struct {
	ProjectID string    `json:"project_id"`
	Release   string    `json:"release"`
	Dist      string    `json:"dist,omitempty"`
	Name      string    `json:"name"`
	SHA256    string    `json:"sha256"`
	BlobKey   string    `json:"blob_key,omitempty"`
	Size      int       `json:"size"`
	DebugID   string    `json:"debug_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	mu               sync.RWMutex
	PII              PIIConfig
	events           map[string]Event
	issues           map[string]*Issue
	groupToID        map[string]string
	artifacts        *ArtifactStore
	reports          []ClientReport
	attachments      map[string]Attachment
	attachmentBodies map[string][]byte
	signals          map[string]StoredSignal
	releases         map[string]Release
}

type EventStore interface {
	Ingest(projectID, key string, body []byte) (int, error)
	ListIssues(projectID string) []Issue
	ListEvents(projectID, issueID string) []Event
}

type ExtendedStore interface {
	ListClientReports(projectID string) []ClientReport
	ListAttachments(projectID, eventID string) []Attachment
	ListSignals(projectID, kind string) []StoredSignal
	ListReleases(projectID string) []Release
	UpsertRelease(projectID, version string) Release
	ListArtifacts(projectID, release string) []ArtifactInfo
	DeleteArtifact(projectID, release, dist, name string) bool
}

type AttachmentReader interface {
	GetAttachment(projectID, id string) (Attachment, []byte, bool)
}

func NewStore() *Store {
	return &Store{PII: DefaultPIIConfig(), events: make(map[string]Event), issues: make(map[string]*Issue), groupToID: make(map[string]string), artifacts: NewArtifactStore(), attachments: make(map[string]Attachment), attachmentBodies: make(map[string][]byte), signals: make(map[string]StoredSignal), releases: make(map[string]Release)}
}

func (s *Store) SetPIIConfig(config PIIConfig) { s.PII = config }

func (s *Store) SetArtifactStore(artifacts *ArtifactStore) {
	s.mu.Lock()
	s.artifacts = artifacts
	s.mu.Unlock()
}

type App struct {
	Store         EventStore
	Artifacts     *ArtifactStore
	Control       ControlPlane
	PII           PIIConfig
	MaxEnvelope   int64
	RelayToken    string
	ArtifactToken string
	ProjectKeys   map[string]map[string]struct{}
	APITokens     map[string]string
	CurrentUserID string
	RateLimiter   *RateLimiter
	Logger        *slog.Logger
}

func NewApp(store EventStore) *App {
	if store == nil {
		store = NewStore()
	}
	artifacts := NewArtifactStore()
	if provider, ok := store.(interface{ ArtifactStore() *ArtifactStore }); ok {
		artifacts = provider.ArtifactStore()
	}
	if memoryStore, ok := store.(*Store); ok {
		if memoryStore.artifacts == nil {
			memoryStore.artifacts = artifacts
		}
		artifacts = memoryStore.artifacts
	}
	if _, provider := store.(interface{ ArtifactStore() *ArtifactStore }); provider {
		// The persistent store already owns an ArtifactStore backed by its DB.
	} else if artifactAware, ok := store.(interface{ SetArtifactStore(*ArtifactStore) }); ok {
		artifactAware.SetArtifactStore(artifacts)
	}
	control := NewMemoryControlPlane()
	if provider, ok := store.(ControlPlane); ok {
		control = provider
	}
	app := &App{Store: store, Artifacts: artifacts, Control: control, PII: DefaultPIIConfig(), MaxEnvelope: DefaultMaxEnvelopeBytes, CurrentUserID: "1", Logger: slog.Default()}
	app.SetPIIConfig(app.PII)
	return app
}

// SetPIIConfig updates both the HTTP ingress policy and any store that can
// process envelopes directly (memory or PostgreSQL).
func (a *App) SetPIIConfig(config PIIConfig) {
	a.PII = config
	if configurable, ok := a.Store.(interface{ SetPIIConfig(PIIConfig) }); ok {
		configurable.SetPIIConfig(config)
	}
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
	if r.Method == http.MethodOptions {
		setCORSHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	setCORSHeaders(w)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if a.isControlAPI(parts) {
		a.handleControlAPI(w, r, parts)
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "issues" && r.Method == http.MethodGet {
		projectID := r.URL.Query().Get("project")
		writeJSON(w, http.StatusOK, a.Store.ListIssues(projectID))
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "events" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.Store.ListEvents(r.URL.Query().Get("project"), r.URL.Query().Get("issue")))
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "client-reports" && r.Method == http.MethodGet {
		if extended, ok := a.Store.(ExtendedStore); ok {
			writeJSON(w, http.StatusOK, extended.ListClientReports(r.URL.Query().Get("project")))
		} else {
			writeJSON(w, http.StatusOK, []ClientReport{})
		}
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "attachments" && r.Method == http.MethodGet {
		if len(parts) == 4 {
			if reader, ok := a.Store.(AttachmentReader); ok {
				attachment, body, found := reader.GetAttachment(r.URL.Query().Get("project"), parts[3])
				if found {
					w.Header().Set("Content-Type", attachment.ContentType)
					w.Header().Set("Content-Disposition", `attachment; filename="`+attachment.Filename+`"`)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(body)
					return
				}
				http.NotFound(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if extended, ok := a.Store.(ExtendedStore); ok {
			writeJSON(w, http.StatusOK, extended.ListAttachments(r.URL.Query().Get("project"), r.URL.Query().Get("event")))
		} else {
			writeJSON(w, http.StatusOK, []Attachment{})
		}
		return
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "0" && parts[2] == "signals" && r.Method == http.MethodGet {
		if extended, ok := a.Store.(ExtendedStore); ok {
			writeJSON(w, http.StatusOK, extended.ListSignals(r.URL.Query().Get("project"), r.URL.Query().Get("kind")))
		} else {
			writeJSON(w, http.StatusOK, []StoredSignal{})
		}
		return
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "0" && parts[2] == "projects" && parts[4] == "releases" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		a.handleReleaseCollection(w, r, parts)
		return
	}
	if isArtifactUploadPath(parts) && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		if !a.validArtifactToken(r) {
			http.Error(w, "management authentication required", http.StatusUnauthorized)
			return
		}
		a.handleArtifactUpload(w, r, parts)
		return
	}
	if isArtifactManagementPath(parts) && (r.Method == http.MethodGet || r.Method == http.MethodDelete) {
		if !a.validArtifactToken(r) {
			http.Error(w, "management authentication required", http.StatusUnauthorized)
			return
		}
		a.handleArtifactManagement(w, r, parts)
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
	if !a.validProjectKey(projectID, key) {
		http.Error(w, "invalid sentry key", http.StatusUnauthorized)
		return
	}
	if a.Control != nil {
		if err := a.Control.EnsureProject(projectID, key); err != nil {
			a.Logger.Warn("control plane project provisioning failed", "error", err, "project_id", projectID)
		}
	}
	if a.RateLimiter != nil {
		bucket := projectID + ":" + key + ":" + requestClientKey(r.RemoteAddr)
		if allowed, retryAfter := a.RateLimiter.Allow(bucket, time.Now().UTC()); !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			w.Header().Set("X-Sentry-Rate-Limits", fmt.Sprintf("%d:error:project", retryAfter))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}
	if r.Body == nil {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	body, err := readEnvelopeBody(w, r, a.MaxEnvelope)
	if err != nil {
		http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
		return
	}
	if parts[2] == "store" {
		body = wrapStoreEvent(body)
	}
	body, err = ScrubEnvelope(body, a.PII)
	if err != nil {
		a.Logger.Warn("envelope PII scrub failed", "error", err, "project_id", projectID)
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
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

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Sentry-Auth, X-Sentry-Envelope, X-SentryX-Relay-Token, X-SentryX-Management-Token")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func readEnvelopeBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxEnvelopeBytes
	}
	reader := io.Reader(http.MaxBytesReader(w, r.Body, maxBytes))
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		compressed, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		body, err := io.ReadAll(io.LimitReader(gzipReader, maxBytes+1))
		if err != nil || int64(len(body)) > maxBytes {
			return nil, errors.New("decompressed envelope too large")
		}
		return body, nil
	}
	body, err := io.ReadAll(reader)
	return body, err
}

func (a *App) validProjectKey(projectID, key string) bool {
	if len(a.ProjectKeys) == 0 {
		return true
	}
	keys, ok := a.ProjectKeys[projectID]
	if !ok {
		return false
	}
	_, ok = keys[key]
	return ok
}

func (a *App) validArtifactToken(r *http.Request) bool {
	if a.ArtifactToken == "" {
		return true
	}
	token := r.Header.Get("X-SentryX-Management-Token")
	if token == "" {
		const prefix = "Bearer "
		if value := r.Header.Get("Authorization"); strings.HasPrefix(value, prefix) {
			token = strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return len(token) == len(a.ArtifactToken) && subtle.ConstantTimeCompare([]byte(token), []byte(a.ArtifactToken)) == 1
}

func (s *Store) Ingest(projectID, _ string, body []byte) (int, error) {
	body, err := ScrubEnvelope(body, s.PII)
	if err != nil {
		return 0, err
	}
	items, err := parseEnvelope(body)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	accepted := 0
	for _, item := range items {
		switch item.Type {
		case "event", "error":
			event, err := decodeEvent(projectID, item.Payload, now)
			if err != nil {
				continue
			}
			if event.EventID == "" && item.EventID != "" {
				event.EventID = item.EventID
			}
			event = scrubEvent(event)
			s.symbolicate(&event)
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
		case "client_report":
			if report, err := decodeClientReport(projectID, item.Payload, now); err == nil {
				s.mu.Lock()
				s.reports = append(s.reports, report)
				s.mu.Unlock()
				accepted++
			}
		case "attachment":
			if attachment, err := s.storeAttachment(projectID, item, now); err == nil {
				s.mu.Lock()
				s.attachments[attachment.ID] = attachment
				s.mu.Unlock()
				accepted++
			}
		case "log", "transaction", "span", "replay_event", "replay_recording", "profile", "profile_chunk", "session", "sessions", "minidump", "unreal_report", "applecrashreport":
			if stored, err := s.storeSignal(projectID, item, now); err == nil {
				s.mu.Lock()
				s.signals[stored.ID] = stored
				s.mu.Unlock()
				accepted++
			}
		}
	}
	return accepted, nil
}

func (s *Store) storeSignal(projectID string, item envelopeItem, now time.Time) (StoredSignal, error) {
	stored := StoredSignal{ID: randomID(), ProjectID: projectID, EventID: item.EventID, Kind: item.Type, ReceivedAt: now, Payload: append(json.RawMessage(nil), item.Payload...), Schema: 1, ContentType: item.ContentType, Size: len(item.Payload)}
	if !json.Valid(stored.Payload) {
		stored.Payload = json.RawMessage(`{"binary":true}`)
		if s.artifacts == nil || s.artifacts.blob == nil {
			return StoredSignal{}, errors.New("binary signal requires blob store")
		}
		stored.BlobKey = path.Join("signals", blobPathSegment(projectID), stored.ID)
		if err := s.artifacts.blob.Put(context.Background(), stored.BlobKey, item.Payload); err != nil {
			return StoredSignal{}, err
		}
	}
	return stored, nil
}

func decodeClientReport(projectID string, payload []byte, now time.Time) (ClientReport, error) {
	var wire struct {
		Timestamp       string           `json:"timestamp"`
		DiscardedEvents []DiscardedEvent `json:"discarded_events"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return ClientReport{}, err
	}
	timestamp := now
	if wire.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, wire.Timestamp); err == nil {
			timestamp = parsed
		}
	}
	return ClientReport{ID: randomID(), ProjectID: projectID, ReceivedAt: now, Timestamp: timestamp, DiscardedEvents: wire.DiscardedEvents}, nil
}

func (s *Store) storeAttachment(projectID string, item envelopeItem, now time.Time) (Attachment, error) {
	if len(item.Payload) == 0 || len(item.Payload) > int(DefaultMaxBlobBytes) {
		return Attachment{}, errors.New("attachment too large or empty")
	}
	id := randomID()
	filename := item.Filename
	if filename == "" {
		filename = "attachment-" + id
	}
	attachment := Attachment{ID: id, ProjectID: projectID, EventID: item.EventID, Filename: path.Base(filename), ContentType: item.ContentType, Size: len(item.Payload), SHA256: artifactDigest(item.Payload), CreatedAt: now}
	if s.artifacts != nil && s.artifacts.blob != nil {
		attachment.BlobKey = path.Join("attachments", blobPathSegment(projectID), blobPathSegment(item.EventID), id, blobPathSegment(attachment.Filename))
		if err := s.artifacts.blob.Put(context.Background(), attachment.BlobKey, item.Payload); err != nil {
			return Attachment{}, err
		}
	} else {
		s.mu.Lock()
		s.attachmentBodies[id] = append([]byte(nil), item.Payload...)
		s.mu.Unlock()
	}
	return attachment, nil
}

func (s *Store) GetAttachment(projectID, id string) (Attachment, []byte, bool) {
	s.mu.RLock()
	attachment, ok := s.attachments[id]
	body := append([]byte(nil), s.attachmentBodies[id]...)
	s.mu.RUnlock()
	if !ok || (projectID != "" && attachment.ProjectID != projectID) {
		return Attachment{}, nil, false
	}
	if len(body) == 0 && attachment.BlobKey != "" && s.artifacts != nil && s.artifacts.blob != nil {
		body, _ = s.artifacts.blob.Get(context.Background(), attachment.BlobKey)
	}
	return attachment, body, true
}

func (s *Store) ListClientReports(projectID string) []ClientReport {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ClientReport, 0)
	for _, report := range s.reports {
		if projectID == "" || report.ProjectID == projectID {
			result = append(result, report)
		}
	}
	return result
}

func (s *Store) ListAttachments(projectID, eventID string) []Attachment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Attachment, 0)
	for _, attachment := range s.attachments {
		if (projectID == "" || attachment.ProjectID == projectID) && (eventID == "" || attachment.EventID == eventID) {
			result = append(result, attachment)
		}
	}
	return result
}

func (s *Store) ListSignals(projectID, kind string) []StoredSignal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]StoredSignal, 0)
	for _, signal := range s.signals {
		if (projectID == "" || signal.ProjectID == projectID) && (kind == "" || signal.Kind == kind) {
			result = append(result, signal)
		}
	}
	return result
}

func (s *Store) ListReleases(projectID string) []Release {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Release, 0)
	for _, release := range s.releases {
		if projectID == "" || release.ProjectID == projectID {
			result = append(result, release)
		}
	}
	return result
}

func (s *Store) UpsertRelease(projectID, version string) Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := projectID + ":" + version
	if release, ok := s.releases[key]; ok {
		return release
	}
	release := Release{ProjectID: projectID, Version: version, CreatedAt: time.Now().UTC()}
	s.releases[key] = release
	return release
}

func (s *Store) ListArtifacts(projectID, release string) []ArtifactInfo {
	return s.artifacts.List(projectID, release)
}

func (s *Store) DeleteArtifact(projectID, release, dist, name string) bool {
	return s.artifacts.Delete(projectID, release, dist, name)
}

func (s *Store) symbolicate(event *Event) {
	if s.artifacts == nil || event.Release == "" || len(event.Frames) == 0 {
		event.SymbolicationStatus = "not_attempted"
		return
	}
	debugID := debugIDFromMeta(event.DebugMeta)
	for _, frame := range event.Frames {
		var mapped StackFrame
		var ok bool
		if debugID != "" {
			mapped, ok = s.artifacts.LookupDebugID(event.ProjectID, debugID, frame.Filename, frame.Lineno, frame.Colno)
		}
		if !ok {
			mapped, ok = s.artifacts.Lookup(event.ProjectID, event.Release, event.Dist, frame.Filename, frame.Lineno, frame.Colno)
		}
		if !ok {
			continue
		}
		event.SymbolicatedFrames = append(event.SymbolicatedFrames, mapped)
		if event.Culprit == "" || event.Culprit == frame.Function+"@"+frame.Filename {
			event.Culprit = mapped.Function + "@" + mapped.Filename
		}
	}
	if len(event.SymbolicatedFrames) > 0 {
		event.SymbolicationStatus = "symbolicated"
	} else {
		event.SymbolicationStatus = "miss"
	}
}

func debugIDFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if value := stringValue(meta["debug_id"]); value != "" {
		return value
	}
	if images, ok := meta["images"].([]any); ok {
		for _, value := range images {
			if image, ok := value.(map[string]any); ok {
				if debugID := stringValue(image["debug_id"]); debugID != "" {
					return debugID
				}
			}
		}
	}
	return ""
}

func isArtifactUploadPath(parts []string) bool {
	if len(parts) >= 7 && parts[0] == "api" && parts[1] == "0" && parts[2] == "projects" {
		if len(parts) == 7 && parts[4] == "releases" && parts[6] == "files" {
			return true
		}
		if len(parts) == 8 && parts[5] == "releases" && parts[7] == "files" {
			return true
		}
	}
	return false
}

func isArtifactManagementPath(parts []string) bool {
	return (len(parts) == 7 || len(parts) == 8) && parts[0] == "api" && parts[1] == "0" && parts[2] == "projects" && parts[4] == "releases" && parts[6] == "files"
}

func (a *App) handleReleaseCollection(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}
	projectID, _ := url.PathUnescape(parts[3])
	if len(parts) == 5 && r.Method == http.MethodGet {
		if extended, ok := a.Store.(ExtendedStore); ok {
			writeJSON(w, http.StatusOK, extended.ListReleases(projectID))
		} else {
			writeJSON(w, http.StatusOK, []Release{})
		}
		return
	}
	if len(parts) != 5 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !a.validArtifactToken(r) {
		http.Error(w, "management authentication required", http.StatusUnauthorized)
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil || strings.TrimSpace(request.Version) == "" {
		http.Error(w, "release version required", http.StatusBadRequest)
		return
	}
	if extended, ok := a.Store.(ExtendedStore); ok {
		writeJSON(w, http.StatusCreated, extended.UpsertRelease(projectID, strings.TrimSpace(request.Version)))
		return
	}
	writeJSON(w, http.StatusCreated, Release{ProjectID: projectID, Version: strings.TrimSpace(request.Version), CreatedAt: time.Now().UTC()})
}

func (a *App) handleArtifactManagement(w http.ResponseWriter, r *http.Request, parts []string) {
	projectID, _ := url.PathUnescape(parts[3])
	release, _ := url.PathUnescape(parts[5])
	if len(parts) == 7 && r.Method == http.MethodGet {
		if extended, ok := a.Store.(ExtendedStore); ok {
			writeJSON(w, http.StatusOK, extended.ListArtifacts(projectID, release))
		} else {
			writeJSON(w, http.StatusOK, []ArtifactInfo{})
		}
		return
	}
	if len(parts) == 8 && r.Method == http.MethodDelete {
		name, _ := url.PathUnescape(parts[7])
		if extended, ok := a.Store.(ExtendedStore); ok && extended.DeleteArtifact(projectID, release, r.URL.Query().Get("dist"), name) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
		return
	}
	http.NotFound(w, r)
}

func (a *App) handleArtifactUpload(w http.ResponseWriter, r *http.Request, parts []string) {
	projectIndex, releaseIndex := 3, 5
	if len(parts) == 8 {
		projectIndex, releaseIndex = 4, 6
	}
	projectID, _ := url.PathUnescape(parts[projectIndex])
	release, _ := url.PathUnescape(parts[releaseIndex])
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-Sentry-Artifact-Name")
	}
	var body []byte
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, a.MaxEnvelope)
		if err = r.ParseMultipartForm(a.MaxEnvelope); err == nil {
			if name == "" {
				name = r.FormValue("name")
			}
			file, header, fileErr := r.FormFile("file")
			if fileErr == nil {
				defer file.Close()
				if name == "" {
					name = header.Filename
				}
				body, err = io.ReadAll(file)
			} else {
				err = fileErr
			}
		}
	} else {
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, a.MaxEnvelope))
	}
	if err != nil {
		http.Error(w, "artifact too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	if name == "" {
		http.Error(w, "artifact name required", http.StatusBadRequest)
		return
	}
	if err := a.Artifacts.Upload(projectID, release, r.URL.Query().Get("dist"), name, body); err != nil {
		a.Logger.Warn("artifact rejected", "error", err, "project_id", projectID)
		http.Error(w, "invalid source map", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": true, "name": name, "release": release})
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
		Frames []StackFrame `json:"frames"`
	} `json:"stacktrace"`
}

type StackFrame struct {
	Filename string `json:"filename,omitempty"`
	AbsPath  string `json:"abs_path,omitempty"`
	Function string `json:"function,omitempty"`
	Lineno   int    `json:"lineno,omitempty"`
	Colno    int    `json:"colno,omitempty"`
	InApp    bool   `json:"in_app,omitempty"`
}

func decodeEvent(projectID string, payload []byte, now time.Time) (Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, err
	}
	event := Event{ProjectID: projectID, ReceivedAt: now, OccurredAt: now, Raw: raw, Platform: stringValue(raw["platform"]), Level: stringValue(raw["level"]), Release: stringValue(raw["release"]), Dist: stringValue(raw["dist"]), Environment: stringValue(raw["environment"]), Message: stringValue(raw["message"]), Culprit: stringValue(raw["culprit"]), Logger: stringValue(raw["logger"]), Transaction: stringValue(raw["transaction"]), ServerName: stringValue(raw["server_name"])}
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
			if value, ok := values[len(values)-1].(map[string]any); ok {
				if mechanism, hasMechanism := value["mechanism"]; hasMechanism {
					event.Mechanism, _ = json.Marshal(mechanism)
				}
			}
			event.Frames = append(event.Frames, last.Stacktrace.Frames...)
			if len(last.Stacktrace.Frames) > 0 {
				frame := last.Stacktrace.Frames[len(last.Stacktrace.Frames)-1]
				event.Culprit = frame.Function + "@" + frame.Filename
			}
		}
	}
	if stacktrace, ok := raw["stacktrace"].(map[string]any); ok {
		if encoded, err := json.Marshal(stacktrace); err == nil {
			event.Stacktrace = encoded
		}
		var parsed struct {
			Frames []StackFrame `json:"frames"`
		}
		if encoded, err := json.Marshal(stacktrace); err == nil && json.Unmarshal(encoded, &parsed) == nil {
			event.Frames = append(event.Frames, parsed.Frames...)
		}
	}
	if mechanism, ok := raw["mechanism"]; ok {
		event.Mechanism, _ = json.Marshal(mechanism)
	}
	if value, ok := raw["user"].(map[string]any); ok {
		event.User = &User{ID: stringValue(value["id"]), Username: stringValue(value["username"]), Email: stringValue(value["email"]), IPAddress: stringValue(value["ip_address"]), Name: stringValue(value["name"])}
	}
	if value, ok := raw["request"].(map[string]any); ok {
		event.Request = decodeRequest(value)
	}
	if value, ok := raw["breadcrumbs"].(map[string]any); ok {
		if values, ok := value["values"].([]any); ok {
			event.Breadcrumbs = decodeBreadcrumbs(values)
		}
	} else if values, ok := raw["breadcrumbs"].([]any); ok {
		event.Breadcrumbs = decodeBreadcrumbs(values)
	}
	event.Extra = mapValue(raw["extra"])
	event.Contexts = mapValue(raw["contexts"])
	event.Modules = stringMapValue(raw["modules"])
	event.SDK = mapValue(raw["sdk"])
	event.DebugMeta = mapValue(raw["debug_meta"])
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

func decodeRequest(value map[string]any) *Request {
	request := &Request{URL: stringValue(value["url"]), Method: stringValue(value["method"]), QueryString: stringValue(value["query_string"]), Fragments: stringValue(value["fragments"]), Data: value["data"]}
	if headers, ok := value["headers"].(map[string]any); ok {
		request.Headers = make(map[string]string, len(headers))
		for key, value := range headers {
			request.Headers[key] = stringValue(value)
		}
	}
	return request
}

func decodeBreadcrumbs(values []any) []Breadcrumb {
	result := make([]Breadcrumb, 0, len(values))
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		breadcrumb := Breadcrumb{Type: stringValue(entry["type"]), Category: stringValue(entry["category"]), Message: stringValue(entry["message"]), Level: stringValue(entry["level"]), Data: mapValue(entry["data"])}
		if timestamp := stringValue(entry["timestamp"]); timestamp != "" {
			breadcrumb.Timestamp, _ = time.Parse(time.RFC3339, timestamp)
		}
		result = append(result, breadcrumb)
	}
	return result
}

func mapValue(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return nil
}

func stringMapValue(value any) map[string]string {
	entries, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(entries))
	for key, value := range entries {
		result[key] = stringValue(value)
	}
	return result
}

var scrubKeyPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|cookie|api[_-]?key|private[_-]?key)`)

func scrubEvent(event Event) Event {
	event.Raw = scrubMap(event.Raw)
	if event.User != nil {
		event.User.IPAddress = "[Filtered]"
	}
	if event.Request != nil {
		for key := range event.Request.Headers {
			if scrubKeyPattern.MatchString(key) {
				event.Request.Headers[key] = "[Filtered]"
			}
		}
		event.Request.QueryString = scrubString(event.Request.QueryString)
		event.Request.Data = scrubValue(event.Request.Data)
	}
	event.Extra = scrubMap(event.Extra)
	event.Contexts = scrubMap(event.Contexts)
	for index := range event.Breadcrumbs {
		event.Breadcrumbs[index].Data = scrubMap(event.Breadcrumbs[index].Data)
	}
	return event
}

func scrubMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		if key == "_meta" {
			result[key] = item
			continue
		}
		if scrubKeyPattern.MatchString(key) {
			result[key] = "[Filtered]"
			continue
		}
		result[key] = scrubValue(item)
	}
	return result
}

func scrubValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return scrubMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = scrubValue(item)
		}
		return result
	case string:
		return scrubString(typed)
	default:
		return value
	}
}

func scrubString(value string) string {
	return regexp.MustCompile(`(?i)(bearer\s+|token=|password=)[^\s&]+`).ReplaceAllString(value, "$1[Filtered]")
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
			if len(event.SymbolicatedFrames) > 0 {
				frame := event.SymbolicatedFrames[len(event.SymbolicatedFrames)-1]
				parts = append(parts, normalizePath(frame.Filename), frame.Function)
				return shortHash(strings.Join(parts, "\x00"))
			}
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
	Type        string
	Payload     []byte
	EventID     string
	Filename    string
	ContentType string
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
			Type        string `json:"type"`
			Length      int    `json:"length"`
			EventID     string `json:"event_id"`
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal(line, &header); err != nil || header.Type == "" {
			return nil, errors.New("invalid item header")
		}
		index = next
		if header.Length > 0 {
			if header.Length > len(body)-index {
				return nil, errors.New("truncated item")
			}
			items = append(items, envelopeItem{Type: header.Type, Payload: body[index : index+header.Length], EventID: header.EventID, Filename: header.Filename, ContentType: header.ContentType})
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
			items = append(items, envelopeItem{Type: header.Type, Payload: payload, EventID: header.EventID, Filename: header.Filename, ContentType: header.ContentType})
			index += consumed
			if index < len(body) && body[index] == '\n' {
				index++
			}
			continue
		}
		items = append(items, envelopeItem{Type: header.Type, Payload: bytes.TrimSpace(body[index:]), EventID: header.EventID, Filename: header.Filename, ContentType: header.ContentType})
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
