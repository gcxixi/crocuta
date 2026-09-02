package sentryx

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// ItemPolicy controls non-error Sentry envelope items at the Relay edge.
// Values are store, drop, or sample:N (N is a percentage from 0 to 100).
type ItemPolicy map[string]string

func ParseItemPolicy(value string) ItemPolicy {
	policy := ItemPolicy{}
	for _, entry := range strings.Split(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(parts[1]))
		if action != "store" && action != "drop" && !strings.HasPrefix(action, "sample:") {
			continue
		}
		policy[strings.TrimSpace(parts[0])] = action
	}
	return policy
}

func ItemPolicyFromEnv() ItemPolicy { return ParseItemPolicy(os.Getenv("SENTRYX_ITEM_POLICY")) }

func (p ItemPolicy) action(item envelopeItem) string {
	action := strings.ToLower(strings.TrimSpace(p[item.Type]))
	if action == "" {
		if item.Type == "event" || item.Type == "error" || item.Type == "client_report" || item.Type == "attachment" || isExtendedSignalType(item.Type) {
			return "store"
		}
		return "drop"
	}
	if strings.HasPrefix(action, "sample:") {
		percent, _ := strconv.Atoi(strings.TrimPrefix(action, "sample:"))
		if percent <= 0 || hashSample(item.EventID+":"+item.Type+":"+string(item.Payload))%100 >= uint64(percent) {
			return "drop"
		}
		return "store"
	}
	return action
}

// ApplyItemPolicy filters an envelope and returns the number of dropped items.
// Rebuilding headers with the standard Sentry fields keeps the resulting
// envelope valid while allowing future item types to pass through by default.
func ApplyItemPolicy(body []byte, policy ItemPolicy) ([]byte, int, error) {
	if len(policy) == 0 {
		return body, 0, nil
	}
	items, err := parseEnvelope(body)
	if err != nil {
		return nil, 0, err
	}
	result := bytes.NewBufferString("{}\n")
	dropped := 0
	for _, item := range items {
		if policy.action(item) == "drop" {
			dropped++
			DefaultMetrics.Inc("sentryx_envelope_items_total", map[string]string{"type": item.Type, "action": "drop"})
			continue
		}
		DefaultMetrics.Inc("sentryx_envelope_items_total", map[string]string{"type": item.Type, "action": "store"})
		header := map[string]any{"type": item.Type, "length": len(item.Payload)}
		if item.EventID != "" {
			header["event_id"] = item.EventID
		}
		if item.Filename != "" {
			header["filename"] = item.Filename
		}
		if item.ContentType != "" {
			header["content_type"] = item.ContentType
		}
		encoded, _ := json.Marshal(header)
		result.Write(encoded)
		result.WriteByte('\n')
		result.Write(item.Payload)
		result.WriteByte('\n')
	}
	return result.Bytes(), dropped, nil
}
