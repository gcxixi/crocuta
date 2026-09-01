package sentryx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
)

// ArtifactStore is the in-memory implementation used by the vertical slice.
// Its API is intentionally equivalent to the BlobStore + artifact metadata
// boundary planned for PostgreSQL/S3.
type ArtifactStore struct {
	mu        sync.RWMutex
	artifacts map[string]SourceMap
}

func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{artifacts: make(map[string]SourceMap)}
}

type SourceMap struct {
	Version    int
	File       string
	SourceRoot string
	Sources    []string
	Names      []string
	Lines      [][]sourceMapping
}

type sourceMapping struct {
	GeneratedColumn int
	SourceIndex     int
	OriginalLine    int
	OriginalColumn  int
	NameIndex       int
}

func (a *ArtifactStore) Upload(projectID, release, dist, name string, body []byte) error {
	sourceMap, err := parseSourceMap(body)
	if err != nil {
		return err
	}
	key := artifactKey(projectID, release, dist, name)
	a.mu.Lock()
	a.artifacts[key] = sourceMap
	a.mu.Unlock()
	return nil
}

func (a *ArtifactStore) Lookup(projectID, release, dist, filename string, line, column int) (StackFrame, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, candidate := range artifactCandidates(projectID, release, dist, filename) {
		if sourceMap, ok := a.artifacts[candidate]; ok {
			return sourceMap.Map(filename, line, column)
		}
	}
	return StackFrame{}, false
}

func artifactCandidates(projectID, release, dist, filename string) []string {
	name := normalizeArtifactName(filename)
	result := []string{artifactKey(projectID, release, dist, name)}
	if dist != "" {
		result = append(result, artifactKey(projectID, release, "", name))
	}
	return result
}

func artifactKey(projectID, release, dist, name string) string {
	return projectID + "\x00" + release + "\x00" + dist + "\x00" + normalizeArtifactName(name)
}

func normalizeArtifactName(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	value = strings.TrimPrefix(value, "~/")
	return path.Clean(strings.TrimPrefix(value, "/"))
}

func (m SourceMap) Map(filename string, line, column int) (StackFrame, bool) {
	lineIndex := line - 1
	if lineIndex < 0 || lineIndex >= len(m.Lines) {
		return StackFrame{}, false
	}
	var best *sourceMapping
	for i := range m.Lines[lineIndex] {
		mapping := &m.Lines[lineIndex][i]
		if mapping.GeneratedColumn > column {
			break
		}
		best = mapping
	}
	if best == nil || best.SourceIndex < 0 || best.SourceIndex >= len(m.Sources) {
		return StackFrame{}, false
	}
	original := m.Sources[best.SourceIndex]
	if m.SourceRoot != "" {
		original = strings.TrimSuffix(m.SourceRoot, "/") + "/" + original
	}
	function := ""
	if best.NameIndex >= 0 && best.NameIndex < len(m.Names) {
		function = m.Names[best.NameIndex]
	}
	return StackFrame{Filename: original, Function: function, Lineno: best.OriginalLine + 1, Colno: best.OriginalColumn, InApp: true}, true
}

func parseSourceMap(body []byte) (SourceMap, error) {
	var wire struct {
		Version    int      `json:"version"`
		File       string   `json:"file"`
		SourceRoot string   `json:"sourceRoot"`
		Sources    []string `json:"sources"`
		Names      []string `json:"names"`
		Mappings   string   `json:"mappings"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return SourceMap{}, err
	}
	if wire.Version != 3 || wire.Mappings == "" {
		return SourceMap{}, errors.New("only version 3 source maps with mappings are supported")
	}
	lines, err := decodeMappings(wire.Mappings)
	if err != nil {
		return SourceMap{}, err
	}
	return SourceMap{Version: wire.Version, File: wire.File, SourceRoot: wire.SourceRoot, Sources: wire.Sources, Names: wire.Names, Lines: lines}, nil
}

const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func decodeMappings(value string) ([][]sourceMapping, error) {
	previousSource, previousLine, previousColumn, previousName := 0, 0, 0, 0
	result := make([][]sourceMapping, 1)
	for _, lineValue := range strings.Split(value, ";") {
		generatedColumn := 0
		if lineValue != "" {
			for _, segment := range strings.Split(lineValue, ",") {
				values, err := decodeVLQ(segment)
				if err != nil || len(values) < 4 {
					return nil, errors.New("invalid source map segment")
				}
				generatedColumn += values[0]
				previousSource += values[1]
				previousLine += values[2]
				previousColumn += values[3]
				nameIndex := -1
				if len(values) >= 5 {
					previousName += values[4]
					nameIndex = previousName
				}
				result[len(result)-1] = append(result[len(result)-1], sourceMapping{GeneratedColumn: generatedColumn, SourceIndex: previousSource, OriginalLine: previousLine, OriginalColumn: previousColumn, NameIndex: nameIndex})
			}
		}
		result = append(result, nil)
	}
	return result[:len(result)-1], nil
}

func decodeVLQ(value string) ([]int, error) {
	result := make([]int, 0, 5)
	current, shift := 0, 0
	for _, char := range value {
		index := strings.IndexRune(base64Chars, char)
		if index < 0 {
			return nil, errors.New("invalid base64 VLQ")
		}
		continuation := index & 32
		current |= (index & 31) << shift
		if continuation == 0 {
			sign := current & 1
			magnitude := current >> 1
			if sign != 0 {
				magnitude = -magnitude
			}
			result = append(result, magnitude)
			current, shift = 0, 0
		} else {
			shift += 5
		}
	}
	if shift != 0 {
		return nil, errors.New("truncated base64 VLQ")
	}
	return result, nil
}

func artifactDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
