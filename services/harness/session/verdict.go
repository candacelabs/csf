// Copyright 2026 Candace Labs

package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// VerdictFence tags the fenced block holding a verifier station's typed
// verdict: the final message of a session that read only a slice record and
// its diff (#420).
const VerdictFence = "verdict"

// The verdict's field names, as Incomplete reports a missing one.
const (
	VerdictFieldPass    = "pass"
	VerdictFieldDefects = "defects"
)

// Verdict is what the verifier station returns: whether the slice record and
// its diff hold, and every defect it found. Pass is true exactly when there
// is no defect.
type Verdict struct {
	Pass    *bool    `json:"pass"`
	Defects []Defect `json:"defects"`
}

// Defect is one thing the verifier found wrong: the statement it contradicts
// (an acceptance item, a decision, the proof), where in the record that
// statement is, and the evidence, each a diff location (path:line) or a
// record field.
type Defect struct {
	Statement string   `json:"statement"`
	Field     string   `json:"field"`
	Evidence  []string `json:"evidence"`
}

// Incomplete names the fields a verdict leaves missing or inconsistent; a
// complete verdict has none.
func (verdict Verdict) Incomplete() []string {
	missing := []string{}
	if verdict.Pass == nil || *verdict.Pass != (len(verdict.Defects) == 0) {
		missing = append(missing, VerdictFieldPass)
	}
	if verdict.Defects == nil {
		missing = append(missing, VerdictFieldDefects)
	}
	for index, defect := range verdict.Defects {
		if strings.TrimSpace(defect.Statement) == "" || strings.TrimSpace(defect.Field) == "" || !anyNonBlank(defect.Evidence) {
			missing = append(missing, fmt.Sprintf("%s[%d]", VerdictFieldDefects, index))
		}
	}
	return missing
}

func anyNonBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// ParseVerdict reads the last verdict block of a reply; found is false when
// the reply carries none, and err is set when the block is not one JSON
// object.
func ParseVerdict(reply string) (verdict Verdict, found bool, err error) {
	block, found := LastFencedBlock(reply, VerdictFence)
	if !found {
		return Verdict{}, false, nil
	}
	if err := json.Unmarshal([]byte(block), &verdict); err != nil {
		return Verdict{}, true, fmt.Errorf("the %s block is not a JSON object: %w", VerdictFence, err)
	}
	return verdict, true, nil
}

// LastFencedBlock is the body of the last fenced block a text tags with tag,
// trimmed; found is false when it has none.
func LastFencedBlock(text string, tag string) (body string, found bool) {
	blocks := regexp.MustCompile("(?s)```"+regexp.QuoteMeta(tag)+"[ \t]*\n(.*?)\n[ \t]*```").FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		return "", false
	}
	return strings.TrimSpace(blocks[len(blocks)-1][1]), true
}

// LastReply is the text of the last assistant message in records that said
// anything, its text blocks joined by a blank line: a session's final
// message so far.
func LastReply(records []Record) string {
	for index := len(records) - 1; index >= 0; index-- {
		if text, _ := records[index].Assistant(); len(text) > 0 {
			return strings.Join(text, "\n\n")
		}
	}
	return ""
}
