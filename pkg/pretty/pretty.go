// Package pretty renders proto messages as human-readable tables or other formats.
package pretty

import (
	"fmt"
	"io"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Render writes a proto message as a formatted table to w, with one row per
// message if messages is a repeated field, or a single row otherwise.
func Render(w io.Writer, msg proto.Message) error {
	if msg == nil {
		return nil
	}
	return renderMessage(w, msg)
}

// RenderList writes one row per message from msgs as a formatted table to w.
func RenderList(w io.Writer, msgs []proto.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return renderList(w, msgs)
}

func renderMessage(w io.Writer, msg proto.Message) error {
	reflection := msg.ProtoReflect()
	fields := fieldsByOrder(reflection)
	if len(fields) == 0 {
		return nil
	}

	// Build headers and rows
	var headers []string
	var row []string

	for _, fd := range fields {
		if fd.Cardinality() == protoreflect.Repeated && fd.Message() != nil {
			continue
		}
		headers = append(headers, toDisplayName(fd.Name()))
		row = append(row, formatValue(reflection.Get(fd)))
	}

	return writeTable(w, headers, [][]string{row})
}

func renderList(w io.Writer, msgs []proto.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	reflection := msgs[0].ProtoReflect()
	fields := fieldsByOrder(reflection)
	if len(fields) == 0 {
		return nil
	}

	// Build headers
	var headers []string
	for _, fd := range fields {
		if fd.Cardinality() == protoreflect.Repeated && fd.Message() != nil {
			continue
		}
		headers = append(headers, toDisplayName(fd.Name()))
	}

	// Build rows
	var rows [][]string
	for _, msg := range msgs {
		reflection := msg.ProtoReflect()
		var row []string
		for _, fd := range fields {
			if fd.Cardinality() == protoreflect.Repeated && fd.Message() != nil {
				continue
			}
			row = append(row, formatValue(reflection.Get(fd)))
		}
		rows = append(rows, row)
	}

	return writeTable(w, headers, rows)
}

func fieldsByOrder(reflection protoreflect.Message) []protoreflect.FieldDescriptor {
	var fields []protoreflect.FieldDescriptor
	reflection.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fields = append(fields, fd)
		return true
	})
	return fields
}

func toDisplayName(name protoreflect.Name) string {
	s := string(name)
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func formatValue(v protoreflect.Value) string {
	switch v.Interface().(type) {
	case nil:
		return ""
	case string:
		return v.String()
	case int32, int64, uint32, uint64, float32, float64:
		return fmt.Sprint(v.Interface())
	case bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v.Interface())
	}
}

func writeTable(w io.Writer, headers []string, rows [][]string) error {
	// Calculate column widths
	colWidths := make([]int, len(headers))
	for i, h := range headers {
		if len(h) > colWidths[i] {
			colWidths[i] = len(h)
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}

	// Write header
	var headerLine, separatorLine strings.Builder
	for i, h := range headers {
		if i > 0 {
			headerLine.WriteString(" | ")
			separatorLine.WriteString("-+-")
		}
		headerLine.WriteString(pad(h, colWidths[i]))
		separatorLine.WriteString(strings.Repeat("-", colWidths[i]))
	}
	if _, err := fmt.Fprintln(w, headerLine.String()); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, separatorLine.String()); err != nil {
		return err
	}

	// Write rows
	for _, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			if i > 0 {
				line.WriteString(" | ")
			}
			line.WriteString(pad(cell, colWidths[i]))
		}
		if _, err := fmt.Fprintln(w, line.String()); err != nil {
			return err
		}
	}

	return nil
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
