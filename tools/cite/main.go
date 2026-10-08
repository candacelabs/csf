// Command cite resolves and renders canonical bibliography entries from citation keys.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Citation struct {
	Title   string
	Authors string
	Year    interface{}
	Where   string
	URL     string
}

type findingKind int

const (
	findingDuplicate findingKind = iota
	findingUnresolved
	findingCitedNotKey
	findingNeverCited
	findingVacuous
)

type Finding struct {
	Kind    findingKind
	Message string
}

var (
	httpClient = &http.Client{Timeout: 20 * time.Second}

	doiRx       = regexp.MustCompile(`10\.\d{4,9}/\S+`)
	arxivRx     = regexp.MustCompile(`(\d{4}\.\d{4,5})`)
	htmlTitleRx = regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	yearRx      = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	refRx       = regexp.MustCompile(`\[([a-z0-9-]+)\]`)
)

func main() {
	check := flag.Bool("check", false, "check mode: validate against documents")
	bibtex := flag.Bool("bibtex", false, "output BibTeX format instead of Markdown")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cite [-check] [-bibtex] keys.txt [documents...]\n")
		os.Exit(2)
	}

	keysPath := args[0]
	docPaths := args[1:]

	keys, _, findings := loadKeys(keysPath)

	var labels []string
	for label := range keys {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		key := keys[label]
		entry := resolve(key)
		if entry == nil {
			findings = append(findings, Finding{
				Kind:    findingUnresolved,
				Message: fmt.Sprintf("unresolved: %s (%s)", label, key),
			})
			continue
		}
		if vacuous(entry) {
			findings = append(findings, Finding{
				Kind:    findingVacuous,
				Message: fmt.Sprintf("vacuous: %s (%s)", label, key),
			})
		}
		if *bibtex {
			fmt.Print(renderBibTeX(label, entry))
		} else {
			fmt.Printf("- <a id=\"ref-%s\"></a>**[%s]** %s. *%s.* %s, %v. <%s>\n",
				label, label, entry.Authors, entry.Title, entry.Where, entry.Year, entry.URL)
		}
	}

	if *check && len(docPaths) > 0 {
		cited := extractCited(docPaths)
		known := make(map[string]bool)
		for label := range keys {
			known[label] = true
		}
		var citedNotKeys []string
		for k := range cited {
			if !known[k] && !isNumeric(k) {
				citedNotKeys = append(citedNotKeys, k)
			}
		}
		sort.Strings(citedNotKeys)
		for _, k := range citedNotKeys {
			findings = append(findings, Finding{
				Kind:    findingCitedNotKey,
				Message: fmt.Sprintf("cited but not a key: [%s]", k),
			})
		}

		var neverCited []string
		for k := range known {
			if !cited[k] {
				neverCited = append(neverCited, k)
			}
		}
		sort.Strings(neverCited)
		for _, k := range neverCited {
			findings = append(findings, Finding{
				Kind:    findingNeverCited,
				Message: fmt.Sprintf("key never cited: [%s]", k),
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Message < findings[j].Message
	})

	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "cite: %s\n", f.Message)
	}

	if len(findings) > 0 {
		os.Exit(1)
	}
}

func loadKeys(keysPath string) (map[string]string, map[string]string, []Finding) {
	keys := make(map[string]string)
	seen := make(map[string]string)
	var findings []Finding

	f, err := os.Open(keysPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cite: cannot open keys: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			fmt.Fprintf(os.Stderr, "cite: malformed key line: %s\n", line)
			os.Exit(1)
		}
		label := parts[0]
		raw := strings.Join(parts[1:], " ")

		c := canonicalize(raw)
		if c == "" {
			fmt.Fprintf(os.Stderr, "cite: cannot canonicalize: %s\n", raw)
			os.Exit(1)
		}
		if prev, exists := seen[c]; exists {
			findings = append(findings, Finding{
				Kind:    findingDuplicate,
				Message: fmt.Sprintf("duplicate: %s and %s are both %s", prev, label, c),
			})
		}
		seen[c] = label
		keys[label] = c
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "cite: reading keys: %v\n", err)
		os.Exit(1)
	}

	return keys, seen, findings
}

func canonicalize(key string) string {
	key = strings.TrimSpace(key)

	if m := doiRx.FindStringSubmatch(key); m != nil {
		doi := m[0]
		doi = strings.TrimRight(doi, ".);")
		return "doi:" + strings.ToLower(doi)
	}

	if m := arxivRx.FindStringSubmatch(key); m != nil {
		return "arxiv:" + m[1]
	}

	if strings.HasPrefix(key, "https://") {
		parts := strings.Fields(key)
		url := strings.TrimRight(parts[0], "/")
		return "url:" + url
	}

	return ""
}

func resolve(canonKey string) *Citation {
	kind, ident, ok := strings.Cut(canonKey, ":")
	if !ok {
		return nil
	}

	switch kind {
	case "doi":
		if e := crossref(ident); e != nil {
			return e
		}
		return datacite(ident)
	case "url":
		return webpage(ident)
	case "arxiv":
		return arxiv(ident)
	}
	return nil
}

func crossref(doi string) *Citation {
	url := "https://api.crossref.org/works/" + url.QueryEscape(doi)
	body := fetch(url)
	if body == nil {
		return nil
	}
	defer body.Close()

	var resp struct {
		Message struct {
			Title  []string `json:"title"`
			Author []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
			} `json:"author"`
			Issued struct {
				DateParts [][]interface{} `json:"date-parts"`
			} `json:"issued"`
			ContainerTitle []string `json:"container-title"`
			Volume         string   `json:"volume"`
			Issue          string   `json:"issue"`
			Page           string   `json:"page"`
		} `json:"message"`
	}

	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&resp); err != nil {
		return nil
	}

	m := resp.Message
	if len(m.Title) == 0 {
		return nil
	}

	var authors []string
	for _, a := range m.Author {
		if a.Given != "" || a.Family != "" {
			authors = append(authors, strings.TrimSpace(a.Given+" "+a.Family))
		}
	}

	var year interface{}
	if len(m.Issued.DateParts) > 0 && len(m.Issued.DateParts[0]) > 0 {
		year = m.Issued.DateParts[0][0]
	}

	venue := ""
	if len(m.ContainerTitle) > 0 {
		venue = m.ContainerTitle[0]
	}
	where := venue
	if m.Volume != "" {
		where += " " + m.Volume
		if m.Issue != "" {
			where += " (" + m.Issue + ")"
		}
	}
	if m.Page != "" {
		where += ":" + m.Page
	}

	return &Citation{
		Title:   m.Title[0],
		Authors: strings.Join(authors, ", "),
		Year:    year,
		Where:   where,
		URL:     "https://doi.org/" + doi,
	}
}

func datacite(doi string) *Citation {
	url := "https://api.datacite.org/dois/" + url.QueryEscape(doi)
	body := fetch(url)
	if body == nil {
		return nil
	}
	defer body.Close()

	var resp struct {
		Data struct {
			Attributes struct {
				Titles []struct {
					Title string `json:"title"`
				} `json:"titles"`
				Creators []struct {
					Name string `json:"name"`
				} `json:"creators"`
				PublicationYear interface{} `json:"publicationYear"`
				Container       struct {
					Title string `json:"title"`
				} `json:"container"`
				Publisher string `json:"publisher"`
			} `json:"attributes"`
		} `json:"data"`
	}

	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&resp); err != nil {
		return nil
	}

	a := resp.Data.Attributes
	if len(a.Titles) == 0 {
		return nil
	}

	var creators []string
	for _, c := range a.Creators {
		if c.Name != "" {
			creators = append(creators, c.Name)
		}
	}

	where := a.Container.Title
	if where == "" {
		where = a.Publisher
	}

	return &Citation{
		Title:   a.Titles[0].Title,
		Authors: strings.Join(creators, ", "),
		Year:    a.PublicationYear,
		Where:   where,
		URL:     "https://doi.org/" + doi,
	}
}

func arxiv(aid string) *Citation {
	url := "https://export.arxiv.org/api/query?id_list=" + url.QueryEscape(aid)
	body := fetch(url)
	if body == nil {
		return nil
	}
	defer body.Close()

	data, _ := io.ReadAll(body)
	xmlStr := string(data)

	entryMatch := regexp.MustCompile(`<entry>(.*?)</entry>`).FindStringSubmatch(xmlStr)
	if len(entryMatch) == 0 {
		return nil
	}
	entry := entryMatch[1]

	titleMatch := regexp.MustCompile(`<title>(.*?)</title>`).FindStringSubmatch(entry)
	if len(titleMatch) == 0 {
		return nil
	}
	title := regexp.MustCompile(`\s+`).ReplaceAllString(titleMatch[1], " ")
	title = strings.TrimSpace(title)

	authorsMatch := regexp.MustCompile(`<name>(.*?)</name>`).FindAllStringSubmatch(entry, -1)
	var authors []string
	for _, m := range authorsMatch {
		authors = append(authors, m[1])
	}

	yearMatch := regexp.MustCompile(`<published>(\d{4})`).FindStringSubmatch(entry)
	var year interface{} = 0
	if len(yearMatch) > 0 {
		fmt.Sscanf(yearMatch[1], "%d", &year)
	}

	return &Citation{
		Title:   title,
		Authors: strings.Join(authors, ", "),
		Year:    year,
		Where:   "arXiv:" + aid,
		URL:     "https://arxiv.org/abs/" + aid,
	}
}

func webpage(u string) *Citation {
	body := fetch(u)
	if body == nil {
		return nil
	}
	defer body.Close()

	data, _ := io.ReadAll(body)
	html := string(data)

	titleMatch := htmlTitleRx.FindStringSubmatch(html)
	if len(titleMatch) == 0 {
		return nil
	}

	title := regexp.MustCompile(`\s+`).ReplaceAllString(titleMatch[1], " ")
	title = strings.TrimSpace(title)

	var year interface{}
	yearMatch := yearRx.FindStringSubmatch(u)
	if len(yearMatch) > 0 {
		fmt.Sscanf(yearMatch[1], "%d", &year)
	}

	parts := strings.Split(u, "/")
	where := ""
	if len(parts) > 2 {
		where = parts[2]
	}

	return &Citation{
		Title:   title,
		Authors: "",
		Year:    year,
		Where:   where,
		URL:     u,
	}
}

func fetch(u string) io.ReadCloser {
	resp, err := httpClient.Get(u)
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	return resp.Body
}

func vacuous(e *Citation) bool {
	if e == nil {
		return true
	}
	if e.Title == "" || e.Where == "" {
		return true
	}
	return false
}

func extractCited(docPaths []string) map[string]bool {
	cited := make(map[string]bool)
	for _, path := range docPaths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()

		text := string(data)
		matches := refRx.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if m[1] != "verify" {
				cited[m[1]] = true
			}
		}
	}
	return cited
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func renderBibTeX(label string, e *Citation) string {
	if e == nil {
		return ""
	}

	entryType := "article"
	key := label

	authors := e.Authors
	if authors == "" {
		authors = "Unknown"
	}

	title := e.Title
	if title == "" {
		title = "Untitled"
	}

	journal := e.Where
	if journal == "" {
		journal = "Unknown"
	}

	year := "0"
	if e.Year != nil {
		year = fmt.Sprintf("%v", e.Year)
	}

	return fmt.Sprintf("@%s{%s,\n  author = {%s},\n  title = {%s},\n  journal = {%s},\n  year = {%s},\n  url = {%s}\n}\n\n", entryType, key, authors, title, journal, year, e.URL)
}
