package sentryx

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ParseProjectKeys parses the deployment format "project:key,project:key".
// An empty value leaves the compatibility-mode validator open.
func ParseProjectKeys(value string) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		project, key, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok || project == "" || key == "" {
			continue
		}
		if result[project] == nil {
			result[project] = make(map[string]struct{})
		}
		result[project][key] = struct{}{}
	}
	return result
}

// ParseAPITokens parses the local management-token compatibility format
// "token:user-id,token2:user-id". Tokens are only held in memory; the
// PostgreSQL control-plane table is reserved for hashed tokens in production.
func ParseAPITokens(value string) map[string]string {
	result := make(map[string]string)
	for _, entry := range strings.Split(value, ",") {
		token, userID, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if ok && token != "" {
			result[token] = strings.TrimSpace(userID)
		}
	}
	return result
}

func requestClientKey(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

const defaultMaxRateLimiterEntries = 10000

type rateWindow struct {
	start time.Time
	count int
}

// RateLimiter is a bounded in-process fixed window limiter with automatic
// TTL cleanup and maximum capacity protection against memory leaks.
type RateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	maxEntries  int
	lastCleanup time.Time
	entries     map[string]rateWindow
}

func NewRateLimiter(limit int) *RateLimiter {
	if limit <= 0 {
		return nil
	}
	return &RateLimiter{
		limit:       limit,
		window:      time.Minute,
		maxEntries:  defaultMaxRateLimiterEntries,
		lastCleanup: time.Now(),
		entries:     make(map[string]rateWindow),
	}
}

func (r *RateLimiter) cleanup(now time.Time) {
	r.lastCleanup = now
	for k, v := range r.entries {
		if now.Sub(v.start) >= r.window {
			delete(r.entries, k)
		}
	}
	if len(r.entries) >= r.maxEntries {
		// If still over capacity after removing expired windows, clear entries to prevent OOM
		r.entries = make(map[string]rateWindow)
	}
}

func (r *RateLimiter) Allow(bucket string, now time.Time) (bool, int) {
	if r == nil || r.limit <= 0 {
		return true, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.lastCleanup) >= r.window || len(r.entries) >= r.maxEntries {
		r.cleanup(now)
	}
	entry := r.entries[bucket]
	if entry.start.IsZero() || now.Sub(entry.start) >= r.window {
		entry = rateWindow{start: now}
	}
	if entry.count >= r.limit {
		remaining := int((entry.start.Add(r.window).Sub(now) + time.Second - 1) / time.Second)
		if remaining < 1 {
			remaining = 1
		}
		return false, remaining
	}
	entry.count++
	r.entries[bucket] = entry
	return true, 0
}

func parsePositiveInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	return parsed, err == nil && parsed > 0
}
