package evals

import (
	"math"
	"sort"

	"github.com/context-maximiser/code-graph/libs/search-go"
)

// ComputeRecallAtK returns the fraction of relevant items found in the top-k retrieved results.
func ComputeRecallAtK(retrieved []string, relevant map[string]RelevanceGrade, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}

	found := 0
	for i := 0; i < k; i++ {
		if grade, ok := relevant[retrieved[i]]; ok && grade > 0 {
			found++
		}
	}

	// Count total relevant (grade > 0)
	totalRelevant := 0
	for _, g := range relevant {
		if g > 0 {
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 0
	}
	return float64(found) / float64(totalRelevant)
}

// ComputePrecisionAtK returns the fraction of top-k retrieved results that are relevant.
func ComputePrecisionAtK(retrieved []string, relevant map[string]RelevanceGrade, k int) float64 {
	if k <= 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}

	found := 0
	for i := 0; i < k; i++ {
		if grade, ok := relevant[retrieved[i]]; ok && grade > 0 {
			found++
		}
	}
	return float64(found) / float64(k)
}

// ComputeNDCG computes Normalized Discounted Cumulative Gain at position k.
// Uses gain = 2^grade - 1 for graded relevance.
func ComputeNDCG(retrieved []string, relevant map[string]RelevanceGrade, k int) float64 {
	if k > len(retrieved) {
		k = len(retrieved)
	}
	if k <= 0 || len(relevant) == 0 {
		return 0
	}

	// DCG
	dcg := 0.0
	for i := 0; i < k; i++ {
		grade, ok := relevant[retrieved[i]]
		if !ok {
			continue
		}
		gain := math.Pow(2, float64(grade)) - 1
		dcg += gain / math.Log2(float64(i+2)) // i+2 because log2(1) = 0
	}

	// IDCG: sort all grades descending, compute best possible DCG
	grades := make([]int, 0, len(relevant))
	for _, g := range relevant {
		grades = append(grades, int(g))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))

	idcgK := k
	if idcgK > len(grades) {
		idcgK = len(grades)
	}

	idcg := 0.0
	for i := 0; i < idcgK; i++ {
		gain := math.Pow(2, float64(grades[i])) - 1
		idcg += gain / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// ComputeMRR returns the Mean Reciprocal Rank — 1/rank of the first relevant result.
func ComputeMRR(retrieved []string, relevant map[string]RelevanceGrade) float64 {
	for i, key := range retrieved {
		if grade, ok := relevant[key]; ok && grade > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// SourceContrib tracks how many relevant results each source contributed.
type SourceContrib struct {
	VectorHits   int `json:"vectorHits"`
	FullTextHits int `json:"fullTextHits"`
	SemanticHits int `json:"semanticHits"`
	HybridHits   int `json:"hybridHits"`
	VectorOnly   int `json:"vectorOnly"`
	FullTextOnly int `json:"fullTextOnly"`
	SemanticOnly int `json:"semanticOnly"`
}

// AnalyzeSourceContribution counts how many relevant results came from each source.
func AnalyzeSourceContribution(results []search.HybridSearchResult, relevant map[string]RelevanceGrade) SourceContrib {
	var sc SourceContrib

	for _, r := range results {
		nodeKey, _ := r.Node["nodeKey"].(string)
		grade, ok := relevant[nodeKey]
		if !ok || grade <= 0 {
			continue
		}

		switch r.Source {
		case "vector":
			sc.VectorHits++
			if r.FullTextScore == 0 && r.SemanticScore == 0 {
				sc.VectorOnly++
			}
		case "fulltext":
			sc.FullTextHits++
			if r.VectorScore == 0 && r.SemanticScore == 0 {
				sc.FullTextOnly++
			}
		case "semantic":
			sc.SemanticHits++
			if r.VectorScore == 0 && r.FullTextScore == 0 {
				sc.SemanticOnly++
			}
		case "hybrid":
			sc.HybridHits++
			// Hybrid means multiple sources contributed
		}
	}

	return sc
}

// AddContrib merges another SourceContrib into this one.
func (sc *SourceContrib) AddContrib(other SourceContrib) {
	sc.VectorHits += other.VectorHits
	sc.FullTextHits += other.FullTextHits
	sc.SemanticHits += other.SemanticHits
	sc.HybridHits += other.HybridHits
	sc.VectorOnly += other.VectorOnly
	sc.FullTextOnly += other.FullTextOnly
	sc.SemanticOnly += other.SemanticOnly
}
