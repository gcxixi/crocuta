package sentryx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// PIIRule is intentionally close to Sentry's relayPiiConfig shape while
// remaining small enough for the first Go implementation. Selectors use
// Sentry-style paths such as "$message", "extra.password", or
// "request.headers.authorization".
type PIIRule struct {
	ID          string `json:"id,omitempty"`
	Selector    string `json:"selector"`
	Type        string `json:"type,omitempty"`
	Action      string `json:"action,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// PIIConfig controls scrubbing at the edge and at server ingest. A disabled
// config is useful for local debugging, but the defaults are privacy-first.
type PIIConfig struct {
	Enabled          bool      `json:"enabled"`
	ScrubDefaults    bool      `json:"scrub_defaults"`
	ScrubIPAddresses bool      `json:"scrub_ip_addresses"`
	SensitiveFields  []string  `json:"sensitive_fields,omitempty"`
	SafeFields       []string  `json:"safe_fields,omitempty"`
	Rules            []PIIRule `json:"rules,omitempty"`
}

func DefaultPIIConfig() PIIConfig {
	return PIIConfig{Enabled: true, ScrubDefaults: true, ScrubIPAddresses: true}
}

// PIIConfigFromEnv provides an operator-friendly configuration for both the
// Relay and Server binaries. SENTRYX_PII_RULES accepts a JSON array of PIIRule
// or an object containing a "rules" array.
func PIIConfigFromEnv() PIIConfig {
	config := DefaultPIIConfig()
	setBool := func(key string, target *bool) {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if parsed, err := strconv.ParseBool(value); err == nil {
				*target = parsed
			}
		}
	}
	setBool("SENTRYX_PII_ENABLED", &config.Enabled)
	setBool("SENTRYX_PII_SCRUB_DEFAULTS", &config.ScrubDefaults)
	setBool("SENTRYX_PII_SCRUB_IP_ADDRESSES", &config.ScrubIPAddresses)
	config.SensitiveFields = splitCSV(os.Getenv("SENTRYX_PII_SENSITIVE_FIELDS"))
	config.SafeFields = splitCSV(os.Getenv("SENTRYX_PII_SAFE_FIELDS"))
	if raw := strings.TrimSpace(os.Getenv("SENTRYX_PII_RULES")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &config.Rules); err != nil {
			var wrapper struct {
				Rules []PIIRule `json:"rules"`
			}
			if json.Unmarshal([]byte(raw), &wrapper) == nil {
				config.Rules = wrapper.Rules
			}
		}
	}
	return config
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func (c PIIConfig) normalized() PIIConfig {
	if !c.Enabled {
		return c
	}
	if !c.ScrubDefaults && !c.ScrubIPAddresses && len(c.SensitiveFields) == 0 && len(c.Rules) == 0 {
		// Explicitly enabled configurations still protect IP addresses unless
		// the caller supplied a meaningful custom policy.
		c.ScrubDefaults = true
		c.ScrubIPAddresses = true
	}
	return c
}

type piiProcessor struct {
	config        PIIConfig
	keyPattern    *regexp.Regexp
	secretPattern *regexp.Regexp
	creditCard    *regexp.Regexp
	custom        []*compiledPIIRule
	safe          map[string]struct{}
}

type compiledPIIRule struct {
	rule    PIIRule
	pattern *regexp.Regexp
}

func newPIIProcessor(config PIIConfig) *piiProcessor {
	config = config.normalized()
	p := &piiProcessor{
		config:        config,
		keyPattern:    regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|cookie|api[_-]?key|private[_-]?key)`),
		secretPattern: regexp.MustCompile(`(?i)(bearer\s+|token=|password=)[^\s&]+`),
		creditCard:    regexp.MustCompile(`(?i)\b(?:\d[ -]*?){13,19}\b`),
		safe:          make(map[string]struct{}, len(config.SafeFields)),
	}
	for _, field := range config.SafeFields {
		p.safe[strings.ToLower(strings.TrimSpace(field))] = struct{}{}
	}
	for _, rule := range config.Rules {
		compiled := &compiledPIIRule{rule: rule}
		if rule.Pattern != "" {
			if pattern, err := regexp.Compile(rule.Pattern); err == nil {
				compiled.pattern = pattern
			}
		}
		p.custom = append(p.custom, compiled)
	}
	return p
}

type piiState struct {
	meta map[string]string
}

func (p *piiProcessor) scrubEnvelope(body []byte) ([]byte, error) {
	if !p.config.Enabled {
		return append([]byte(nil), body...), nil
	}
	items, err := parseEnvelope(body)
	if err != nil {
		return nil, err
	}
	_, firstNext, err := nextLine(body, 0)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(body)+128)
	result = append(result, body[:firstNext]...)
	for _, item := range items {
		state := &piiState{meta: make(map[string]string)}
		payload := p.scrubItem(item, state)
		header := make(map[string]any, len(item.Header)+1)
		for key, value := range item.Header {
			header[key] = value
		}
		header["type"] = item.Type
		header["length"] = len(payload)
		headerBytes, _ := json.Marshal(header)
		result = append(result, headerBytes...)
		result = append(result, '\n')
		result = append(result, payload...)
		result = append(result, '\n')
	}
	return result, nil
}

func (p *piiProcessor) scrubItem(item envelopeItem, state *piiState) []byte {
	if json.Valid(item.Payload) {
		var value any
		if json.Unmarshal(item.Payload, &value) == nil {
			cleaned, _ := p.scrubValue(value, "", state)
			if item.Type == "event" || item.Type == "error" || item.Type == "log" {
				if root, ok := cleaned.(map[string]any); ok && len(state.meta) > 0 {
					meta, _ := root["_meta"].(map[string]any)
					if meta == nil {
						meta = make(map[string]any, len(state.meta))
					}
					for key, reason := range state.meta {
						meta[key] = map[string]any{"reason": reason}
					}
					root["_meta"] = meta
				}
			}
			encoded, err := json.Marshal(cleaned)
			if err == nil {
				return encoded
			}
		}
	}
	if isTextAttachment(item) {
		return p.scrubText(item.Payload, "", state)
	}
	// Binary signals and unknown binary attachments cannot be safely parsed.
	// Their metadata is still retained, but their bytes are never rewritten.
	return append([]byte(nil), item.Payload...)
}

func isTextAttachment(item envelopeItem) bool {
	if strings.HasPrefix(strings.ToLower(item.ContentType), "text/") || strings.Contains(strings.ToLower(item.ContentType), "json") {
		return true
	}
	ext := strings.ToLower(path.Ext(item.Filename))
	return ext == ".txt" || ext == ".log" || ext == ".json" || ext == ".csv" || ext == ".ndjson"
}

func (p *piiProcessor) scrubText(value []byte, fieldPath string, state *piiState) []byte {
	text := string(value)
	if p.config.ScrubDefaults {
		text = p.secretPattern.ReplaceAllString(text, "$1[Filtered]")
		text = p.creditCard.ReplaceAllStringFunc(text, func(match string) string {
			return p.redact("@creditcard", match, "mask", "", fieldPath, state)
		})
	}
	for _, rule := range p.custom {
		if rule.pattern != nil && (rule.rule.Selector == "" || selectorMatches(rule.rule.Selector, fieldPath)) {
			text = rule.pattern.ReplaceAllStringFunc(text, func(match string) string {
				return p.redact("@"+ruleType(rule.rule), match, rule.rule.Action, rule.rule.Replacement, fieldPath, state)
			})
		}
	}
	return []byte(text)
}

func (p *piiProcessor) scrubValue(value any, fieldPath string, state *piiState) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "_meta" {
				result[key] = item
				continue
			}
			currentPath := joinSelectorPath(fieldPath, key)
			if p.shouldSkip(key, currentPath) {
				result[key] = item
				continue
			}
			if action, reason, replacement, ok := p.actionFor(key, currentPath, item); ok {
				redacted := p.redact(reason, stringValue(item), action, replacement, currentPath, state)
				if action == "remove" {
					continue
				}
				result[key] = redacted
				continue
			}
			cleaned, keep := p.scrubValue(item, currentPath, state)
			if keep {
				result[key] = cleaned
			}
		}
		return result, true
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			cleaned, keep := p.scrubValue(item, fieldPath, state)
			if keep {
				result = append(result, cleaned)
			}
		}
		return result, true
	case string:
		return string(p.scrubText([]byte(typed), fieldPath, state)), true
	default:
		return value, true
	}
}

func (p *piiProcessor) shouldSkip(key, fieldPath string) bool {
	_, safeByPath := p.safe[strings.ToLower(fieldPath)]
	_, safeByKey := p.safe[strings.ToLower(key)]
	return safeByPath || safeByKey
}

func (p *piiProcessor) actionFor(key, fieldPath string, value any) (action, reason, replacement string, ok bool) {
	for _, rule := range p.custom {
		if selectorMatches(rule.rule.Selector, fieldPath) || selectorMatches(rule.rule.Selector, "$"+key) {
			return normalizeAction(rule.rule.Action), "@" + ruleType(rule.rule), rule.rule.Replacement, true
		}
	}
	if p.config.ScrubIPAddresses && strings.EqualFold(key, "ip_address") {
		return "mask", "@ip", "", true
	}
	if p.config.ScrubDefaults && p.keyPattern.MatchString(key) {
		return "mask", "@password", "", true
	}
	if p.config.ScrubDefaults {
		if text, ok := value.(string); ok && p.creditCard.MatchString(text) {
			return "mask", "@creditcard", "", true
		}
	}
	for _, field := range p.config.SensitiveFields {
		if strings.EqualFold(strings.TrimSpace(field), key) || strings.EqualFold(strings.TrimSpace(field), fieldPath) {
			return "mask", "@sensitive", "", true
		}
	}
	return "", "", "", false
}

func selectorMatches(selector, fieldPath string) bool {
	selector = strings.TrimSpace(strings.ToLower(selector))
	fieldPath = strings.TrimSpace(strings.ToLower(fieldPath))
	if selector == "" {
		return false
	}
	if selector == fieldPath {
		return true
	}
	if strings.HasSuffix(selector, ".*") {
		return strings.HasPrefix(fieldPath, strings.TrimSuffix(selector, "*"))
	}
	return false
}

func joinSelectorPath(parent, key string) string {
	if parent == "" {
		if key == "message" || key == "title" {
			return "$" + key
		}
		return key
	}
	return parent + "." + key
}

func normalizeAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "remove", "hash", "replace":
		return strings.ToLower(strings.TrimSpace(action))
	default:
		return "mask"
	}
}

func ruleType(rule PIIRule) string {
	if rule.Type != "" {
		return strings.ToLower(rule.Type)
	}
	return "sensitive"
}

func (p *piiProcessor) redact(reason, value, action, replacement, fieldPath string, state *piiState) string {
	if state != nil && fieldPath != "" {
		state.meta[fieldPath] = reason
	}
	switch normalizeAction(action) {
	case "remove":
		return ""
	case "hash":
		digest := sha256.Sum256([]byte(value))
		return "sha256:" + hex.EncodeToString(digest[:])
	case "replace":
		if replacement != "" {
			return replacement
		}
		return "[Filtered]"
	default:
		return "[Filtered]"
	}
}

// ScrubEnvelope applies the configured policy to every JSON item and to
// textual attachments, then rebuilds a valid Envelope with updated lengths.
func ScrubEnvelope(body []byte, config PIIConfig) ([]byte, error) {
	if !config.Enabled {
		return append([]byte(nil), body...), nil
	}
	return newPIIProcessor(config).scrubEnvelope(body)
}

func ScrubJSON(payload []byte, config PIIConfig) ([]byte, error) {
	if !config.Enabled {
		return append([]byte(nil), payload...), nil
	}
	if !json.Valid(payload) {
		return nil, errors.New("invalid JSON payload")
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	state := &piiState{meta: make(map[string]string)}
	cleaned, _ := newPIIProcessor(config).scrubValue(value, "", state)
	return json.Marshal(cleaned)
}

func scrubEnvelopeWithDefault(body []byte) []byte {
	cleaned, err := ScrubEnvelope(body, DefaultPIIConfig())
	if err != nil {
		return append([]byte(nil), body...)
	}
	return cleaned
}
