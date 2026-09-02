package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/gcxixi/crocuta/internal/sentryx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type replayResult struct {
	ProjectID string         `json:"project_id"`
	Version   string         `json:"version"`
	Compare   string         `json:"compare,omitempty"`
	Events    int            `json:"events"`
	Groups    int            `json:"groups"`
	Changed   int            `json:"changed_events"`
	Hashes    map[string]int `json:"hashes"`
}

func main() {
	source := flag.String("source", "jsonl", "event source: jsonl or pg")
	version := flag.String("version", "v2", "grouping version to calculate")
	compare := flag.String("compare", "", "optional second version to compare")
	project := flag.String("project", "", "project ID filter")
	input := flag.String("input", "", "JSONL file for --source=jsonl")
	dsn := flag.String("dsn", os.Getenv("SENTRYX_DATABASE_URL"), "PostgreSQL DSN for --source=pg")
	flag.Parse()
	events, err := loadEvents(*source, *input, *dsn, *project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := replayResult{ProjectID: *project, Version: *version, Compare: *compare, Events: len(events), Hashes: map[string]int{}}
	changed := 0
	for _, event := range events {
		newHash := sentryx.GroupingHashForVersion(event, *version)
		result.Hashes[newHash]++
		if *compare != "" && newHash != sentryx.GroupingHashForVersion(event, *compare) {
			changed++
		}
	}
	result.Groups = len(result.Hashes)
	result.Changed = changed
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
}

func loadEvents(source, input, dsn, project string) ([]sentryx.Event, error) {
	if source == "pg" {
		if dsn == "" {
			return nil, fmt.Errorf("--dsn or SENTRYX_DATABASE_URL is required")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		rows, err := db.QueryContext(context.Background(), `SELECT canonical_json FROM sentryx_events WHERE ($1='' OR project_id=$1) ORDER BY received_at`, project)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []sentryx.Event{}
		for rows.Next() {
			var raw []byte
			var event sentryx.Event
			if rows.Scan(&raw) == nil && json.Unmarshal(raw, &event) == nil {
				result = append(result, event)
			}
		}
		return result, rows.Err()
	}
	if input == "" {
		return nil, fmt.Errorf("--input is required for --source=jsonl")
	}
	file, err := os.Open(input)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	result := []sentryx.Event{}
	for scanner.Scan() {
		var event sentryx.Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil && (project == "" || event.ProjectID == project) {
			result = append(result, event)
		}
	}
	return result, scanner.Err()
}
