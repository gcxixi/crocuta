package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gcxixi/crocuta/internal/sentryx"
)

func TestStreamEventsFiltersAndLimitsJSONL(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "events.jsonl")
	content := "{\"project_id\":\"1\",\"event_id\":\"old\",\"received_at\":\"2026-09-01T00:00:00Z\",\"title\":\"old\"}\n" +
		"{\"project_id\":\"2\",\"event_id\":\"other\",\"received_at\":\"2026-09-02T00:00:00Z\",\"title\":\"other\"}\n" +
		"{\"project_id\":\"1\",\"event_id\":\"first\",\"received_at\":\"2026-09-02T00:00:00Z\",\"title\":\"first\"}\n" +
		"{\"project_id\":\"1\",\"event_id\":\"second\",\"received_at\":\"2026-09-02T01:00:00Z\",\"title\":\"second\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	var events []sentryx.Event
	err := streamEvents(context.Background(), "jsonl", path, "", "1", since, 1, func(event sentryx.Event) {
		events = append(events, event)
	})
	if err != nil || len(events) != 1 || events[0].EventID != "first" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestTransitionReportsIncludeSamplesAndRespectTop(t *testing.T) {
	accumulator := newReplayAccumulator("1", "v2", "v1", 2)
	accumulator.result.Events = 5
	accumulator.result.Hashes["new-a"] = 3
	accumulator.result.Hashes["new-b"] = 2
	accumulator.newToOld["new-a"] = &transitionBucket{hashes: map[string]struct{}{"old-a": {}, "old-b": {}}, events: 3, samples: []string{"e1"}, titles: []string{"boom"}}
	accumulator.newToOld["new-b"] = &transitionBucket{hashes: map[string]struct{}{"old-c": {}, "old-d": {}}, events: 2}
	accumulator.oldToNew["old-a"] = &transitionBucket{hashes: map[string]struct{}{"new-a": {}, "new-c": {}}, events: 4, samples: []string{"e1", "e2"}, titles: []string{"boom", "bang"}}
	result := accumulator.finish(1)
	if len(result.Merges) != 1 || result.Merges[0].NewHash != "new-a" || result.Merges[0].Samples[0] != "e1" {
		t.Fatalf("merges=%#v", result.Merges)
	}
	if len(result.Splits) != 1 || result.Splits[0].OldHash != "old-a" || len(result.Splits[0].Titles) != 2 {
		t.Fatalf("splits=%#v", result.Splits)
	}
}
