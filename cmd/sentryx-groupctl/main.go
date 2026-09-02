package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/gcxixi/crocuta/internal/sentryx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type transitionReport struct {
	OldHash   string   `json:"old_hash,omitempty"`
	NewHash   string   `json:"new_hash,omitempty"`
	OldHashes []string `json:"old_hashes,omitempty"`
	NewHashes []string `json:"new_hashes,omitempty"`
	Events    int      `json:"events"`
	Samples   []string `json:"sample_event_ids,omitempty"`
	Titles    []string `json:"sample_titles,omitempty"`
}

type replayResult struct {
	ProjectID string             `json:"project_id"`
	Version   string             `json:"version"`
	Compare   string             `json:"compare,omitempty"`
	Events    int                `json:"events"`
	Groups    int                `json:"groups"`
	Changed   int                `json:"changed_events"`
	Unchanged int                `json:"unchanged_events"`
	Hashes    map[string]int     `json:"hashes"`
	Merges    []transitionReport `json:"merges,omitempty"`
	Splits    []transitionReport `json:"splits,omitempty"`
}

type transitionBucket struct {
	hashes  map[string]struct{}
	events  int
	samples []string
	titles  []string
}

type replayAccumulator struct {
	result   replayResult
	oldToNew map[string]*transitionBucket
	newToOld map[string]*transitionBucket
	samples  int
}

func newReplayAccumulator(project, version, compare string, samples int) *replayAccumulator {
	if samples < 0 {
		samples = 0
	}
	return &replayAccumulator{
		result:   replayResult{ProjectID: project, Version: version, Compare: compare, Hashes: map[string]int{}},
		oldToNew: map[string]*transitionBucket{},
		newToOld: map[string]*transitionBucket{},
		samples:  samples,
	}
}

func (a *replayAccumulator) add(event sentryx.Event) {
	newHash := sentryx.GroupingHashForVersion(event, a.result.Version)
	a.result.Events++
	a.result.Hashes[newHash]++
	if a.result.Compare == "" {
		return
	}
	oldHash := sentryx.GroupingHashForVersion(event, a.result.Compare)
	if newHash == oldHash {
		a.result.Unchanged++
	} else {
		a.result.Changed++
	}
	addTransition(a.oldToNew, oldHash, newHash, event, a.samples)
	addTransition(a.newToOld, newHash, oldHash, event, a.samples)
}

func addTransition(buckets map[string]*transitionBucket, key, related string, event sentryx.Event, maxSamples int) {
	bucket := buckets[key]
	if bucket == nil {
		bucket = &transitionBucket{hashes: map[string]struct{}{}}
		buckets[key] = bucket
	}
	bucket.hashes[related] = struct{}{}
	bucket.events++
	if event.EventID != "" && len(bucket.samples) < maxSamples && !contains(bucket.samples, event.EventID) {
		bucket.samples = append(bucket.samples, event.EventID)
	}
	if event.Title != "" && len(bucket.titles) < maxSamples && !contains(bucket.titles, event.Title) {
		bucket.titles = append(bucket.titles, event.Title)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sortedHashes(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (a *replayAccumulator) finish(top int) replayResult {
	a.result.Groups = len(a.result.Hashes)
	for oldHash, bucket := range a.oldToNew {
		if len(bucket.hashes) > 1 {
			a.result.Splits = append(a.result.Splits, transitionReport{OldHash: oldHash, NewHashes: sortedHashes(bucket.hashes), Events: bucket.events, Samples: bucket.samples, Titles: bucket.titles})
		}
	}
	for newHash, bucket := range a.newToOld {
		if len(bucket.hashes) > 1 {
			a.result.Merges = append(a.result.Merges, transitionReport{NewHash: newHash, OldHashes: sortedHashes(bucket.hashes), Events: bucket.events, Samples: bucket.samples, Titles: bucket.titles})
		}
	}
	sortReports(a.result.Merges)
	sortReports(a.result.Splits)
	if top > 0 && len(a.result.Merges) > top {
		a.result.Merges = a.result.Merges[:top]
	}
	if top > 0 && len(a.result.Splits) > top {
		a.result.Splits = a.result.Splits[:top]
	}
	return a.result
}

func sortReports(reports []transitionReport) {
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Events != reports[j].Events {
			return reports[i].Events > reports[j].Events
		}
		return reports[i].OldHash+reports[i].NewHash < reports[j].OldHash+reports[j].NewHash
	})
}

func main() {
	source := flag.String("source", "jsonl", "event source: jsonl or pg")
	version := flag.String("version", "v2", "grouping version to calculate")
	compare := flag.String("compare", "", "optional previous version to compare")
	project := flag.String("project", "", "project ID filter")
	input := flag.String("input", "", "JSONL file for --source=jsonl")
	dsn := flag.String("dsn", os.Getenv("SENTRYX_DATABASE_URL"), "PostgreSQL DSN for --source=pg")
	sinceValue := flag.String("since", "", "only replay events received at or after RFC3339 timestamp")
	limit := flag.Int("limit", 0, "maximum events to replay; zero means unlimited")
	top := flag.Int("top", 20, "maximum merge and split reports; zero means unlimited")
	samples := flag.Int("samples", 3, "sample event IDs and titles per merge or split")
	flag.Parse()
	var since time.Time
	var err error
	if *sinceValue != "" {
		since, err = time.Parse(time.RFC3339, *sinceValue)
		if err != nil {
			fmt.Fprintln(os.Stderr, "--since must be RFC3339:", err)
			os.Exit(2)
		}
	}
	if *limit < 0 || *top < 0 || *samples < 0 {
		fmt.Fprintln(os.Stderr, "--limit, --top, and --samples cannot be negative")
		os.Exit(2)
	}
	accumulator := newReplayAccumulator(*project, *version, *compare, *samples)
	err = streamEvents(context.Background(), *source, *input, *dsn, *project, since, *limit, accumulator.add)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, _ := json.MarshalIndent(accumulator.finish(*top), "", "  ")
	fmt.Println(string(encoded))
}

func streamEvents(ctx context.Context, source, input, dsn, project string, since time.Time, limit int, visit func(sentryx.Event)) error {
	if source == "pg" {
		if dsn == "" {
			return fmt.Errorf("--dsn or SENTRYX_DATABASE_URL is required")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		var sinceArg any
		if !since.IsZero() {
			sinceArg = since
		}
		rows, err := db.QueryContext(ctx, `SELECT canonical_json FROM sentryx_events WHERE ($1='' OR project_id=$1) AND ($2::timestamptz IS NULL OR received_at >= $2) ORDER BY received_at LIMIT NULLIF($3,0)`, project, sinceArg, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var event sentryx.Event
			if rows.Scan(&raw) == nil && json.Unmarshal(raw, &event) == nil {
				visit(event)
			}
		}
		return rows.Err()
	}
	if source != "jsonl" {
		return fmt.Errorf("unsupported --source %q", source)
	}
	if input == "" {
		return fmt.Errorf("--input is required for --source=jsonl")
	}
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	visited := 0
	for scanner.Scan() {
		var event sentryx.Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil || (project != "" && event.ProjectID != project) || (!since.IsZero() && event.ReceivedAt.Before(since)) {
			continue
		}
		visit(event)
		visited++
		if limit > 0 && visited >= limit {
			break
		}
	}
	return scanner.Err()
}
