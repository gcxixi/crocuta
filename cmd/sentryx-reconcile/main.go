package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type event struct {
	EventID       string `json:"event_id"`
	LegacyEventID string `json:"eventID"`
	IssueID       string `json:"issue_id"`
	LegacyGroupID string `json:"groupID"`
}

func (e event) id() string {
	if e.EventID != "" {
		return e.EventID
	}
	return e.LegacyEventID
}

func (e event) group() string {
	if e.IssueID != "" {
		return e.IssueID
	}
	return e.LegacyGroupID
}

func main() {
	newURL := flag.String("new-url", "", "SentryX base URL")
	oldURL := flag.String("old-url", "", "legacy Sentry base URL")
	project := flag.String("project", "", "SentryX project ID")
	oldOrg := flag.String("old-org", "", "legacy Sentry organization slug")
	oldProject := flag.String("old-project", "", "legacy Sentry project slug (defaults to --project)")
	start := flag.String("start", "", "optional RFC3339 start time")
	end := flag.String("end", "", "optional RFC3339 end time")
	pageSize := flag.Int("page-size", 100, "page size from 1 to 100")
	newToken := flag.String("new-token", "", "optional bearer token for SentryX")
	oldToken := flag.String("old-token", "", "bearer token for legacy Sentry")
	oldSentryX := flag.Bool("old-sentryx", false, "treat old endpoint as SentryX instead of official Sentry")
	flag.Parse()
	if *newURL == "" || *oldURL == "" || *project == "" {
		fatal("--new-url, --old-url, and --project are required")
	}
	if !*oldSentryX && *oldOrg == "" {
		fatal("--old-org is required for official Sentry")
	}
	if *oldProject == "" {
		*oldProject = *project
	}
	if *pageSize < 1 || *pageSize > 100 {
		fatal("--page-size must be between 1 and 100")
	}

	newEvents, err := fetchSentryX(*newURL, *project, *newToken, *start, *end, *pageSize)
	if err != nil {
		fatal(err.Error())
	}
	var oldEvents []event
	if *oldSentryX {
		oldEvents, err = fetchSentryX(*oldURL, *oldProject, *oldToken, *start, *end, *pageSize)
	} else {
		oldEvents, err = fetchOfficialSentry(*oldURL, *oldOrg, *oldProject, *oldToken, *start, *end, *pageSize)
	}
	if err != nil {
		fatal(err.Error())
	}

	newSet, oldSet := ids(newEvents), ids(oldEvents)
	missingFromNew, missingFromOld := difference(oldSet, newSet), difference(newSet, oldSet)
	oldByID := map[string]string{}
	for _, item := range oldEvents {
		oldByID[item.id()] = item.group()
	}
	mismatched := []string{}
	for _, item := range newEvents {
		if oldGroup, ok := oldByID[item.id()]; ok && oldGroup != "" && item.group() != "" && oldGroup != item.group() {
			mismatched = append(mismatched, item.id())
		}
	}
	sort.Strings(mismatched)
	result := map[string]any{
		"project": *project, "start": *start, "end": *end,
		"new_count": len(newSet), "old_count": len(oldSet), "matched": len(newSet) - len(missingFromOld),
		"missing_from_new": missingFromNew, "missing_from_old": missingFromOld, "grouping_mismatch": mismatched,
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
}

func fetchSentryX(base, project, token, start, end string, pageSize int) ([]event, error) {
	endpoint := strings.TrimRight(base, "/") + "/api/0/events"
	result := []event{}
	cursor := ""
	for {
		query := url.Values{"project": {project}, "limit": {strconv.Itoa(pageSize)}}
		if start != "" {
			query.Set("start", start)
		}
		if end != "" {
			query.Set("end", end)
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		page, header, err := fetch(endpoint+"?"+query.Encode(), token)
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		cursor = cursorFromLink(header.Get("Link"))
		if cursor == "" || len(page) == 0 {
			return result, nil
		}
	}
}

func fetchOfficialSentry(base, org, project, token, start, end string, pageSize int) ([]event, error) {
	endpoint := strings.TrimRight(base, "/") + "/api/0/projects/" + url.PathEscape(org) + "/" + url.PathEscape(project) + "/events/"
	query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "full": {"true"}}
	if start != "" {
		query.Set("start", start)
	}
	if end != "" {
		query.Set("end", end)
	}
	next := endpoint + "?" + query.Encode()
	result := []event{}
	for next != "" {
		page, header, err := fetch(next, token)
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		next = nextURLFromLink(header.Get("Link"))
		if len(page) == 0 {
			break
		}
	}
	return result, nil
}

func fetch(endpoint, token string) ([]event, http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 30 * 1e9}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return nil, response.Header, fmt.Errorf("GET %s: status %s", endpoint, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.Header, err
	}
	var result []event
	if json.Unmarshal(body, &result) == nil {
		return result, response.Header, nil
	}
	var envelope struct {
		Data []event `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, response.Header, err
	}
	return envelope.Data, response.Header, nil
}

var cursorPattern = regexp.MustCompile(`cursor="([^"]+)"`)
var nextPattern = regexp.MustCompile(`<([^>]+)>;[^,]*rel="next"[^,]*results="true"`)

func cursorFromLink(value string) string {
	match := cursorPattern.FindStringSubmatch(value)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func nextURLFromLink(value string) string {
	match := nextPattern.FindStringSubmatch(value)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func ids(events []event) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range events {
		if item.id() != "" {
			result[item.id()] = struct{}{}
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

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
