// Copyright 2026 Candace Labs

package codes

import (
	"cmp"
	"slices"
)

// proposedPrefix marks a cluster of complaints that share a proposed code
// name rather than a catalogue code.
const proposedPrefix = "proposed:"

// Count is how many complaints one key holds.
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// CodeCount is one catalogue code's complaints.
type CodeCount struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Edge  Edge   `json:"edge"`
	Side  Side   `json:"side"`
	Route string `json:"route"`
	Count int    `json:"count"`
}

// ProposalCount is the complaints whose proposals share one name, with the
// first proposal made under it.
type ProposalCount struct {
	Name     string   `json:"name"`
	Proposal Proposal `json:"proposal"`
	Count    int      `json:"count"`
}

// Summary is a coded corpus counted per code, edge and side; codes and
// proposals are ordered by count, most first, so the first code is the one
// whose gate pays most.
type Summary struct {
	Complaints int             `json:"complaints"`
	Coded      int             `json:"coded"`
	Proposed   int             `json:"proposed"`
	Rejected   int             `json:"rejected"`
	Codes      []CodeCount     `json:"codes"`
	Proposals  []ProposalCount `json:"proposals"`
	// Edges and Sides count every kept answer: a catalogue code's edge and
	// side, or a proposal's.
	Edges []Count `json:"edges"`
	Sides []Count `json:"sides"`
}

// Tally counts coded complaints against catalogue.
func Tally(coded []Coded, catalogue []Code) Summary {
	summary := Summary{Complaints: len(coded), Codes: []CodeCount{}, Proposals: []ProposalCount{}}
	codes := map[string]int{}
	proposals := map[string]*ProposalCount{}
	edges := map[string]int{}
	sides := map[Side]int{}
	for _, entry := range coded {
		switch {
		case entry.Rejected != "":
			summary.Rejected++
		case entry.Proposal != nil:
			summary.Proposed++
			key := normalName(entry.Proposal.Name)
			if proposals[key] == nil {
				proposals[key] = &ProposalCount{Name: key, Proposal: *entry.Proposal}
			}
			proposals[key].Count++
			edges[entry.Proposal.Edge.String()]++
			sides[entry.Proposal.Side]++
		default:
			summary.Coded++
			codes[entry.Code]++
		}
	}
	for _, code := range catalogue {
		if count := codes[code.ID]; count > 0 {
			summary.Codes = append(summary.Codes, CodeCount{Code: code.ID, Name: code.Name, Edge: code.Edge, Side: code.Side, Route: code.Side.Route(), Count: count})
			edges[code.Edge.String()] += count
			sides[code.Side] += count
		}
	}
	slices.SortStableFunc(summary.Codes, func(left CodeCount, right CodeCount) int { return cmp.Compare(right.Count, left.Count) })
	for _, proposal := range proposals {
		summary.Proposals = append(summary.Proposals, *proposal)
	}
	slices.SortFunc(summary.Proposals, func(left ProposalCount, right ProposalCount) int {
		return cmp.Or(cmp.Compare(right.Count, left.Count), cmp.Compare(left.Name, right.Name))
	})
	summary.Edges = sortedCounts(edges)
	sideCounts := map[string]int{}
	for side, count := range sides {
		sideCounts[string(side)] = count
	}
	summary.Sides = sortedCounts(sideCounts)
	return summary
}

func sortedCounts(counts map[string]int) []Count {
	sorted := make([]Count, 0, len(counts))
	for key, count := range counts {
		sorted = append(sorted, Count{Key: key, Count: count})
	}
	slices.SortFunc(sorted, func(left Count, right Count) int {
		return cmp.Or(cmp.Compare(right.Count, left.Count), cmp.Compare(left.Key, right.Key))
	})
	return sorted
}

// Clusters is each kept complaint's cluster, keyed by its ref: its catalogue
// code, or its proposal's name; a rejected answer is in no cluster.
func Clusters(coded []Coded) map[string]string {
	clusters := map[string]string{}
	for _, entry := range coded {
		switch {
		case entry.Rejected != "":
		case entry.Proposal != nil:
			clusters[entry.Complaint.Ref] = proposedPrefix + normalName(entry.Proposal.Name)
		default:
			clusters[entry.Complaint.Ref] = entry.Code
		}
	}
	return clusters
}

// SidesOf is each kept complaint's fault side, keyed by its ref.
func SidesOf(coded []Coded, catalogue []Code) map[string]string {
	sides := map[string]string{}
	for _, entry := range coded {
		record := NewRecord(entry, catalogue, "")
		if entry.Rejected == "" && record.Side != "" {
			sides[entry.Complaint.Ref] = string(record.Side)
		}
	}
	return sides
}

// ClusterAgreement compares one clustering of complaints with a reference
// clustering of the same complaints: purity is the share of complaints in
// their cluster's majority reference class, inverse purity the share in
// their class's majority cluster, and F their harmonic mean. A clustering
// that puts every complaint alone scores purity 1, and one that puts all in
// one cluster scores inverse purity 1, so only F rewards matching both.
type ClusterAgreement struct {
	Complaints    int     `json:"complaints"`
	Clusters      int     `json:"clusters"`
	Classes       int     `json:"classes"`
	Purity        float64 `json:"purity"`
	InversePurity float64 `json:"inverse_purity"`
	F             float64 `json:"f"`
}

// Agree measures clusters against reference over the complaints both hold.
func Agree(clusters map[string]string, reference map[string]string) ClusterAgreement {
	byCluster := map[string]map[string]int{}
	byClass := map[string]map[string]int{}
	total := 0
	for ref, cluster := range clusters {
		class, found := reference[ref]
		if !found {
			continue
		}
		total++
		tallyPair(byCluster, cluster, class)
		tallyPair(byClass, class, cluster)
	}
	agreement := ClusterAgreement{Complaints: total, Clusters: len(byCluster), Classes: len(byClass)}
	if total == 0 {
		return agreement
	}
	agreement.Purity = float64(majoritySum(byCluster)) / float64(total)
	agreement.InversePurity = float64(majoritySum(byClass)) / float64(total)
	if sum := agreement.Purity + agreement.InversePurity; sum > 0 {
		agreement.F = 2 * agreement.Purity * agreement.InversePurity / sum
	}
	return agreement
}

func tallyPair(counts map[string]map[string]int, outer string, inner string) {
	if counts[outer] == nil {
		counts[outer] = map[string]int{}
	}
	counts[outer][inner]++
}

// majoritySum is the sum, over every outer key, of its largest inner count.
func majoritySum(counts map[string]map[string]int) int {
	sum := 0
	for _, inner := range counts {
		largest := 0
		for _, count := range inner {
			largest = max(largest, count)
		}
		sum += largest
	}
	return sum
}

// Matching is the share of the keys both labelings hold on which they give
// the same label, and how many keys that is. Kappa corrects it for chance,
// but kappa is 0 whenever one labeling uses a single label, so a reference
// that puts every complaint on one side is read by this share.
func Matching(left map[string]string, right map[string]string) (share float64, shared int) {
	agreed := 0
	for key, label := range left {
		if other, found := right[key]; found {
			shared++
			if label == other {
				agreed++
			}
		}
	}
	if shared == 0 {
		return 0, 0
	}
	return float64(agreed) / float64(shared), shared
}

// Kappa is Cohen's kappa between two labelings over the keys both hold: the
// agreement beyond what the two labelings' own frequencies give by chance.
// It is 0 with no shared key and 1 when both labelings use one label alike.
func Kappa(left map[string]string, right map[string]string) float64 {
	leftFrequency := map[string]int{}
	rightFrequency := map[string]int{}
	agreed, total := 0, 0
	for key, label := range left {
		other, found := right[key]
		if !found {
			continue
		}
		total++
		leftFrequency[label]++
		rightFrequency[other]++
		if label == other {
			agreed++
		}
	}
	if total == 0 {
		return 0
	}
	observed := float64(agreed) / float64(total)
	expected := 0.0
	for label, count := range leftFrequency {
		expected += float64(count) * float64(rightFrequency[label]) / float64(total*total)
	}
	if expected == 1 {
		return 1
	}
	return (observed - expected) / (1 - expected)
}
