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
		if isKnownEnvelopeItemType(item.Type) {
			return "store"
		}
		return "drop"
	}
	if strings.HasPrefix(action, "sample:") {
		percent, _ := strconv.Atoi(strings.TrimPrefix(action, "sample:"))
		sampleID := item.EventID
		if sampleID == "" {
			if value, ok := item.Header["session_id"].(string); ok {
				sampleID = value
			}
		}
		if sampleID == "" {
			sampleID = artifactDigest(item.Payload)
		}
		if percent <= 0 || hashSample(sampleID+":"+item.Type)%100 >= uint64(percent) {
			return "drop"
		}
		return "store"
	}
	return action
}

func isKnownEnvelopeItemType(itemType string) bool {
	return itemType == "event" || itemType == "error" || itemType == "client_report" || itemType == "attachment" || isExtendedSignalType(itemType)
}

// ApplyItemPolicy filters an envelope and returns the number of dropped items.
// Rebuilding headers with the standard Sentry fields keeps the resulting
// envelope valid. Unknown item types are deliberately dropped when a policy is
// active and are exposed separately in metrics instead of being silently lost.
func ApplyItemPolicy(body []byte, policy ItemPolicy) ([]byte, int, error) {
	if len(policy) == 0 {
		return body, 0, nil
	}
	items, err := parseEnvelope(body)
	if err != nil {
		return nil, 0, err
	}
	header, _, err := nextLine(body, 0)
	if err != nil {
		return nil, 0, err
	}
	result := bytes.NewBuffer(nil)
	result.Write(header)
	result.WriteByte('\n')
	dropped := 0
	for _, item := range items {
		if policy.action(item) == "drop" {
			dropped++
			action := "drop"
			if _, configured := policy[item.Type]; !configured && !isKnownEnvelopeItemType(item.Type) {
				action = "drop_unknown"
			}
			DefaultMetrics.Inc("sentryx_envelope_items_total", map[string]string{"type": item.Type, "action": action})
			continue
		}
		DefaultMetrics.Inc("sentryx_envelope_items_total", map[string]string{"type": item.Type, "action": "store"})
		itemHeader := make(map[string]any, len(item.Header)+1)
		for key, value := range item.Header {
			itemHeader[key] = value
		}
		itemHeader["type"] = item.Type
		itemHeader["length"] = len(item.Payload)
		encoded, _ := json.Marshal(itemHeader)
		result.Write(encoded)
		result.WriteByte('\n')
		result.Write(item.Payload)
		result.WriteByte('\n')
	}
	return result.Bytes(), dropped, nil
}
