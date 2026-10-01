package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	linksCommand     = "links"
	handoffFlag      = "handoff"
	handoffRoute     = "ui/current.json"
	handoffLimit     = 32768
	handoffVersion   = 1
	handoffTimeout   = 10 * time.Second
	gitSuffix        = ".git"
	readmePath       = "/csf/README.md"
	agentExamplePath = "/examples/csf-agent"
	extensionPath    = agentExamplePath + "/README.md#mechanical-extension-path"
	gitBlobRoute     = "/blob/"
	gitTreeRoute     = "/tree/"
	httpScheme       = "http"
	httpsScheme      = "https"
	encodedTableBar  = "%7C"
	invalidLinkChars = "<>\r\n"
)

// The handoff is an operator-owned document, not an API message. Read only
// its existing navigation fields; leave unrelated and future fields intact.
type handoffLinks struct {
	SchemaVersion int `json:"schema_version"`
	Repository    struct {
		Origin string `json:"origin"`
	} `json:"repository"`
	Export struct {
		SourceRevision string `json:"source_revision"`
		ReviewURL      string `json:"review_url"`
	} `json:"export"`
	Live struct {
		Workbench struct {
			URL       string `json:"url"`
			KanbanURL string `json:"kanban_url"`
		} `json:"workbench"`
	} `json:"live"`
}

func links(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(linksCommand, flag.ContinueOnError)
	source := flags.String(handoffFlag, dashboardURL+handoffRoute, "handoff JSON file or HTTP URL")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("links accepts only --handoff FILE_OR_URL")
	}
	content, err := readHandoffLinks(*source)
	if err != nil {
		return err
	}
	return renderHandoffLinks(content, output)
}

func readHandoffLinks(source string) ([]byte, error) {
	address, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid handoff location")
	}
	if address.Scheme == "" {
		file, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return readBoundedHandoff(file)
	}
	if err := validateNavigationURL(source); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: handoffTimeout}
	response, err := client.Get(source)
	if err != nil {
		return nil, fmt.Errorf("read handoff: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read handoff: HTTP %d", response.StatusCode)
	}
	return readBoundedHandoff(response.Body)
}

func readBoundedHandoff(reader io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, handoffLimit+1))
	if err != nil {
		return nil, err
	}
	if len(content) > handoffLimit {
		return nil, fmt.Errorf("handoff exceeds %d bytes", handoffLimit)
	}
	return content, nil
}

func renderHandoffLinks(content []byte, output io.Writer) error {
	var handoff handoffLinks
	if err := json.Unmarshal(content, &handoff); err != nil {
		return fmt.Errorf("decode handoff: %w", err)
	}
	if handoff.SchemaVersion != handoffVersion {
		return fmt.Errorf("unsupported handoff schema version")
	}
	revision := handoff.Export.SourceRevision
	if decoded, err := hex.DecodeString(revision); err != nil || len(decoded) != 20 {
		return fmt.Errorf("handoff export requires a full source commit")
	}
	origin := strings.TrimSuffix(strings.TrimRight(handoff.Repository.Origin, "/"), gitSuffix)
	rows := []struct{ name, address string }{
		{"README", origin + gitBlobRoute + revision + readmePath},
		{"Release receipt", handoff.Export.ReviewURL},
		{"Example", origin + gitTreeRoute + revision + agentExamplePath},
		{"Extension guide", origin + gitBlobRoute + revision + extensionPath},
		{"Workbench", handoff.Live.Workbench.URL},
		{"Kanban", handoff.Live.Workbench.KanbanURL},
	}
	var table strings.Builder
	table.WriteString("| Resource | Link |\n|---|---|\n")
	for _, row := range rows {
		if err := validateNavigationURL(row.address); err != nil {
			return fmt.Errorf("%s: %w", row.name, err)
		}
		fmt.Fprintf(&table, "| %s | [%s](<%s>) |\n", row.name, row.name, strings.ReplaceAll(row.address, "|", encodedTableBar))
	}
	_, err := io.WriteString(output, table.String())
	return err
}

func validateNavigationURL(value string) error {
	address, err := url.Parse(value)
	if err != nil || address.Host == "" || address.User != nil ||
		(address.Scheme != httpScheme && address.Scheme != httpsScheme) || strings.ContainsAny(value, invalidLinkChars) {
		return fmt.Errorf("expected an absolute HTTP URL without embedded credentials")
	}
	return nil
}
