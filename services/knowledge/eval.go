// Copyright 2026 Candace Labs

package knowledge

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// maxRank is the deepest hit@k the evaluation reports; the proof asks for the
// answer within the top 3.
const maxRank = 10

// lookupsJSON is the held-out lookups every pass is measured against.
//
//go:embed lookups.json
var lookupsJSON []byte

// Lookup is one held-out lookup: a query and the repository paths that answer
// it, matched by prefix.
type Lookup struct {
	Query string   `json:"query"`
	Paths []string `json:"paths"`
}

// Evaluation is the index's hit@k over the held-out lookups, once in the fused
// order and once in the reranked order. hit@k is the share of lookups whose
// answer appears within the first k hits; an unanswered lookup is a miss at
// every k.
type Evaluation struct {
	Fused    map[int]float64
	Reranked map[int]float64
	Queries  int
}

// Lookups is the held-out lookups of lookups.json.
func Lookups() []Lookup {
	var wrapped struct {
		Lookups []Lookup `json:"lookups"`
	}
	if err := json.Unmarshal(lookupsJSON, &wrapped); err != nil {
		// The file is a compile-time constant of this package; a decode
		// failure is a programming error, not an input error.
		panic("knowledge: decode lookups.json: " + err.Error())
	}
	return wrapped.Lookups
}

// Evaluate answers every lookup in both orders and returns the hit@k curves.
// A lookup that Compare fails to answer stops the pass: the measurement would
// otherwise read too low for a reason no fix to the index can address.
func Evaluate(ctx context.Context, index IIndex, lookups []Lookup) (Evaluation, error) {
	evaluation := Evaluation{Fused: map[int]float64{}, Reranked: map[int]float64{}, Queries: len(lookups)}
	if len(lookups) == 0 {
		return evaluation, nil
	}
	fusedRanks := make([]int, 0, len(lookups))
	rerankedRanks := make([]int, 0, len(lookups))
	for _, lookup := range lookups {
		fused, reranked, err := index.Compare(ctx, &pb.SearchRequest{Query: lookup.Query, Limit: maxRank})
		if err != nil {
			return Evaluation{}, err
		}
		fusedRanks = append(fusedRanks, rankOf(fused.GetHits(), lookup.Paths))
		rerankedRanks = append(rerankedRanks, rankOf(reranked.GetHits(), lookup.Paths))
	}
	for k := 1; k <= maxRank; k++ {
		evaluation.Fused[k] = shareWithin(fusedRanks, k)
		evaluation.Reranked[k] = shareWithin(rerankedRanks, k)
	}
	return evaluation, nil
}

// rankOf is the 1-based position of the first hit whose path carries one of
// the prefixes, and 0 when none does.
func rankOf(hits []*pb.SearchHit, prefixes []string) int {
	for position, hit := range hits {
		for _, prefix := range prefixes {
			if strings.HasPrefix(hit.GetPath(), prefix) {
				return position + 1
			}
		}
	}
	return 0
}

// shareWithin is the share of ranks at or under k; a rank of 0 (unanswered)
// is outside every k.
func shareWithin(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	var within int
	for _, rank := range ranks {
		if rank >= 1 && rank <= k {
			within++
		}
	}
	return float64(within) / float64(len(ranks))
}
