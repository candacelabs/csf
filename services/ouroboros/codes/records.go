// Copyright 2026 Candace Labs

package codes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// RecordFile is the coded complaints' file under the harness state
// directory, one [Record] per line, appended by every coding run.
const RecordFile = "failure_codes.jsonl"

// maxRecordBytes bounds one record line read back.
const maxRecordBytes = 1 << 20

// Record is one coded complaint as the state directory keeps it: where the
// complaint is, the code it was given with its edge and side, and the
// model's evidence. The complaint's own text stays in its source.
type Record struct {
	Source string    `json:"source"`
	Ref    string    `json:"ref"`
	At     time.Time `json:"at"`
	// Code is the catalogue code, empty for a proposal or a rejection.
	Code     string    `json:"code"`
	Edge     Edge      `json:"edge"`
	Side     Side      `json:"side"`
	Quote    string    `json:"quote"`
	Proposal *Proposal `json:"proposal,omitempty"`
	Rejected string    `json:"rejected,omitempty"`
	// Model names the provider and model that coded it.
	Model string `json:"model"`
}

// NewRecord is the record of one coded complaint, coded by modelName,
// against catalogue.
func NewRecord(coded Coded, catalogue []Code, modelName string) Record {
	record := Record{Source: coded.Complaint.Source, Ref: coded.Complaint.Ref, At: coded.Complaint.At,
		Code: coded.Code, Quote: coded.Quote, Proposal: coded.Proposal, Rejected: coded.Rejected, Model: modelName}
	for _, code := range catalogue {
		if code.ID == coded.Code {
			record.Edge, record.Side = code.Edge, code.Side
		}
	}
	if coded.Proposal != nil {
		record.Edge, record.Side = coded.Proposal.Edge, coded.Proposal.Side
	}
	return record
}

// referenceComment starts a comment line in a reference clustering.
const referenceComment = "#"

// ReadReference reads a hand clustering of complaints, the reference a
// coding is measured against: one complaint per line, its ref, class and
// fault side separated by tabs. Blank lines and lines starting with # are
// skipped; a side may be left empty.
func ReadReference(reference io.Reader) (classes map[string]string, sides map[string]string, err error) {
	classes, sides = map[string]string{}, map[string]string{}
	scanner := bufio.NewScanner(reference)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, referenceComment) {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			return nil, nil, fmt.Errorf("failure codes: reference line %d: want ref, class and side separated by tabs", line)
		}
		classes[fields[0]] = fields[1]
		if len(fields) > 2 && fields[2] != "" {
			sides[fields[0]] = fields[2]
		}
	}
	return classes, sides, scanner.Err()
}

// ReadRecords reads every record in records, in order. A later record for
// the same source and ref replaces an earlier one, so a corpus coded again
// counts once, by its latest coding.
func ReadRecords(records io.Reader) ([]Record, error) {
	scanner := bufio.NewScanner(records)
	scanner.Buffer(make([]byte, 0, 64<<10), maxRecordBytes)
	latest := map[[2]string]int{}
	read := []Record{}
	for line := 1; scanner.Scan(); line++ {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("failure codes: %s line %d: %w", RecordFile, line, err)
		}
		key := [2]string{record.Source, record.Ref}
		if index, seen := latest[key]; seen {
			read[index] = record
			continue
		}
		latest[key] = len(read)
		read = append(read, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("failure codes: read %s", RecordFile), err)
	}
	return read, nil
}
