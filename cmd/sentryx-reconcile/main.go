package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

type event struct {
	EventID string `json:"event_id"`
	IssueID string `json:"issue_id"`
}

func main() {
	newURL := flag.String("new-url", "", "SentryX events API base URL")
	oldURL := flag.String("old-url", "", "legacy Sentry events API base URL")
	project := flag.String("project", "", "project ID")
	newToken := flag.String("new-token", "", "optional bearer token for SentryX")
	oldToken := flag.String("old-token", "", "optional bearer token for legacy Sentry")
	flag.Parse()
	if *newURL == "" || *oldURL == "" || *project == "" {
		fmt.Fprintln(os.Stderr, "--new-url, --old-url, and --project are required")
		os.Exit(2)
	}
	newEvents, err := fetch(strings.TrimRight(*newURL, "/")+"/api/0/events?project="+*project, *newToken)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	oldEvents, err := fetch(strings.TrimRight(*oldURL, "/")+"/api/0/events?project="+*project, *oldToken)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	newSet, oldSet := ids(newEvents), ids(oldEvents)
	missingFromNew, missingFromOld := difference(oldSet, newSet), difference(newSet, oldSet)
	mismatched := []string{}
	oldByID := map[string]string{}
	for _, item := range oldEvents {
		oldByID[item.EventID] = item.IssueID
	}
	for _, item := range newEvents {
		if oldIssue, ok := oldByID[item.EventID]; ok && oldIssue != "" && item.IssueID != "" && oldIssue != item.IssueID {
			mismatched = append(mismatched, item.EventID)
		}
	}
	sort.Strings(mismatched)
	result := map[string]any{"project": *project, "new_count": len(newSet), "old_count": len(oldSet), "matched": len(newSet) - len(missingFromOld), "missing_from_new": missingFromNew, "missing_from_old": missingFromOld, "grouping_mismatch": mismatched}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
}

func fetch(url, token string) ([]event, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %s", url, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var result []event
	if json.Unmarshal(body, &result) == nil {
		return result, nil
	}
	var envelope struct {
		Data []event `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	return envelope.Data, nil
}
func ids(events []event) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range events {
		if item.EventID != "" {
			result[item.EventID] = struct{}{}
		}
	}
	return result
}
func difference(left, right map[string]struct{}) []string {
	result := []string{}
	for id := range left {
		if _, ok := right[id]; !ok {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
