// Copyright 2026 Candace Labs

package docker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// settingsKey is where the current shape nests an owner's settings. The
// previous shape, written by CSF-DB's csf (e9437ce) before the record moved
// here, kept the settings' fields flat beside the location's.
const settingsKey = "settings"

// ErrRecordInvalid is what every [RecordError] wraps.
var ErrRecordInvalid = errors.New("ipc/docker: state record cannot be used")

// RecordError names a state record a host refuses to act on: the file, the
// field, what is wrong with it and how to fix it.
type RecordError struct {
	Path    string
	Field   string
	Problem string
	Fix     string
}

// Error names the file, the field, the problem and the fix.
func (recordError *RecordError) Error() string {
	return fmt.Sprintf("ipc/docker: %s: field %q %s; fix: %s", recordError.Path, recordError.Field, recordError.Problem, recordError.Fix)
}

// Unwrap is [ErrRecordInvalid].
func (recordError *RecordError) Unwrap() error { return ErrRecordInvalid }

// RequiredField is a [RecordError] for a field a record must carry, for an
// owner's Validate: the decoder fills the path.
func RequiredField(field string, fix string) *RecordError {
	return &RecordError{Field: field, Problem: "is missing or empty", Fix: fix}
}

// DecodeOwnedRecord decodes an owned record in the current shape or the
// previous flat one, strictly: an unknown field is an error, not ignored,
// and a required field that is missing is an error, not a zero value the host
// then acts on. previous reports a record in the previous shape, which the
// caller rewrites. validate checks the owner's settings; nil checks none.
func DecodeOwnedRecord[Settings any](path string, content []byte,
	validate func(settings Settings) *RecordError) (record OwnedRecord[Settings], previous bool, err error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return record, false, &RecordError{Path: path, Field: "(file)", Problem: "is not a JSON object: " + err.Error(),
			Fix: "restore the file from the copy beside it, or move it aside so the host provisions a new record"}
	}
	if _, nested := fields[settingsKey]; nested {
		err = strictDecode(content, &record)
	} else {
		previous = true
		err = decodePrevious(content, fields, &record)
	}
	if err != nil {
		return record, previous, withPath(path, err)
	}
	if problem := validateLocation(record.OwnedLocation); problem != nil {
		return record, previous, withPath(path, problem)
	}
	if validate != nil {
		if problem := validate(record.Settings); problem != nil {
			return record, previous, withPath(path, problem)
		}
	}
	return record, previous, nil
}

// decodePrevious decodes the flat shape: every key must belong to the
// location or to the settings.
func decodePrevious[Settings any](content []byte, fields map[string]json.RawMessage, record *OwnedRecord[Settings]) error {
	known, err := jsonKeys(OwnedLocation{})
	if err != nil {
		return err
	}
	settingsKeys, err := jsonKeys(record.Settings)
	if err != nil {
		return err
	}
	for key := range settingsKeys {
		known[key] = true
	}
	for key := range fields {
		if !known[key] {
			return unknownField(key)
		}
	}
	if err := json.Unmarshal(content, &record.OwnedLocation); err != nil {
		return err
	}
	return json.Unmarshal(content, &record.Settings)
}

// jsonKeys is the set of keys value's type writes.
func jsonKeys(value any) (map[string]bool, error) {
	// any: the zero value of whatever type is being described is marshalled
	// once only to read its field names.
	content, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(fields))
	for key := range fields {
		keys[key] = true
	}
	return keys, nil
}

// strictDecode decodes content into target, refusing a field target lacks.
func strictDecode(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if field, unknown := strings.CutPrefix(errorText(err), unknownFieldPrefix); unknown {
		return unknownField(strings.Trim(field, `"`))
	}
	return err
}

const unknownFieldPrefix = "json: unknown field "

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func unknownField(field string) *RecordError {
	return &RecordError{Field: field, Problem: "is not part of this record",
		Fix: "remove the field, or run the csf binary that wrote it (csf upgrade can roll back)"}
}

// validateLocation requires every location field.
func validateLocation(location OwnedLocation) *RecordError {
	const fix = "restore the field from the copy beside the file (a host keeps <file>.pre-<revision> when it rewrites one), or read it from `docker inspect` of the container"
	switch {
	case location.Container == "":
		return RequiredField("container", fix)
	case location.Volume == "":
		return RequiredField("volume", fix)
	case location.Image == "":
		return RequiredField("image", fix)
	case location.Host == "":
		return RequiredField("host", fix)
	case location.Port == 0:
		return RequiredField("port", fix)
	}
	return nil
}

// withPath sets the file on a RecordError, or names the file on any other
// decoding error.
func withPath(path string, err error) error {
	if recordError := (*RecordError)(nil); errors.As(err, &recordError) {
		recordError.Path = path
		return recordError
	}
	return &RecordError{Path: path, Field: "(file)", Problem: "does not decode: " + err.Error(),
		Fix: "restore the file from the copy beside it, or run the csf binary that wrote it"}
}
