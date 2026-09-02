package main

import "testing"

func TestCursorFromSentryXLink(t *testing.T) {
	if got := cursorFromLink(`; rel="next"; cursor="opaque-value"`); got != "opaque-value" {
		t.Fatalf("cursor=%q", got)
	}
}

func TestNextURLFromOfficialSentryLink(t *testing.T) {
	link := `<https://sentry.example/api/0/projects/acme/web/events/?cursor=next>; rel="next"; results="true"; cursor="next", <https://sentry.example/api/0/projects/acme/web/events/?cursor=prev>; rel="previous"; results="false"`
	if got := nextURLFromLink(link); got != "https://sentry.example/api/0/projects/acme/web/events/?cursor=next" {
		t.Fatalf("next=%q", got)
	}
}

func TestLegacyEventFieldsNormalize(t *testing.T) {
	event := event{LegacyEventID: "event", LegacyGroupID: "group"}
	if event.id() != "event" || event.group() != "group" {
		t.Fatalf("event=%#v", event)
	}
}
