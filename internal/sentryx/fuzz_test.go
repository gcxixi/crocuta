package sentryx

import "testing"

func FuzzParseEnvelopeNeverPanics(f *testing.F) {
	event := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","message":"seed"}`)
	f.Add(testEnvelope(envelopePart(`{"type":"event","length":`+itoa(len(event))+`}`, event)))
	f.Add([]byte("{}\n"))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = parseEnvelope(body)
	})
}
