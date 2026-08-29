// Package eval is the offline retrieval benchmark harness (axis #7): graded IR
// metrics (Recall@k, nDCG@k, MRR, Success@1) over a hand-curated query set with
// known-relevant clause refs. It is read-only against a built DuckDB snapshot
// and never touches the query-time server — it picks the V2 semantic stack from
// data, not priors. The eval set's relevance comes from spec clause numbers we
// already trust (sentinel events), never from an LLM.
package eval

import "math"

// Ref identifies a clause (the unit of relevance).
type Ref struct {
	SpecID string
	Clause string
}

// Relevant is a graded relevance judgement: 2 = exact target clause,
// 1 = acceptable sibling/parent/descendant context, 0 = irrelevant.
type Relevant struct {
	SpecID string `json:"spec_id"`
	Clause string `json:"clause"`
	Grade  int    `json:"grade"`
}

// gradeOf returns the relevance grade of a retrieved ref against the judgements:
// an exact (spec, clause) match scores the full grade; a descendant clause
// (e.g. 6.2.2.2.2 under a 6.2.2.2 target) scores acceptable-context (capped at
// 1) so retrieving finer detail still counts, but not as the exact hit.
func gradeOf(hit Ref, rels []Relevant) int {
	best := 0
	for _, r := range rels {
		if r.SpecID != hit.SpecID {
			continue
		}
		switch {
		case r.Clause == hit.Clause:
			if r.Grade > best {
				best = r.Grade
			}
		case isDescendant(hit.Clause, r.Clause) || isDescendant(r.Clause, hit.Clause):
			if g := min(r.Grade, 1); g > best {
				best = g
			}
		}
	}
	return best
}

// isDescendant reports whether child is a dotted sub-clause of parent.
func isDescendant(child, parent string) bool {
	return len(child) > len(parent) && child[:len(parent)] == parent && child[len(parent)] == '.'
}

// Metrics are the per-query (then macro-averaged) IR scores.
type Metrics struct {
	NDCG5    float64 `json:"ndcg@5"`
	NDCG10   float64 `json:"ndcg@10"`
	Recall5  float64 `json:"recall@5"`
	Recall10 float64 `json:"recall@10"`
	Recall20 float64 `json:"recall@20"`
	MRR      float64 `json:"mrr@10"`
	Success1 float64 `json:"success@1"`
}

// Score computes all metrics for one ranked result list (best-first) against
// the graded judgements for a query.
func Score(ranked []Ref, rels []Relevant) Metrics {
	return Metrics{
		NDCG5:    ndcg(ranked, rels, 5),
		NDCG10:   ndcg(ranked, rels, 10),
		Recall5:  recall(ranked, rels, 5),
		Recall10: recall(ranked, rels, 10),
		Recall20: recall(ranked, rels, 20),
		MRR:      mrr(ranked, rels, 10),
		Success1: success1(ranked, rels),
	}
}

func ndcg(ranked []Ref, rels []Relevant, k int) float64 {
	idcg := idealDCG(rels, k)
	if idcg == 0 {
		return 0
	}
	// Each relevant judgement may be credited at most once, so DCG never exceeds
	// IDCG (multiple retrieved descendants of one target don't double-count).
	used := make([]bool, len(rels))
	var dcg float64
	for i, hit := range ranked {
		if i >= k {
			break
		}
		g := bestUnusedGrade(hit, rels, used)
		if g > 0 {
			dcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
		}
	}
	return dcg / idcg
}

// bestUnusedGrade returns the grade of the highest not-yet-credited relevant
// judgement this hit satisfies, marking it used. Exact match scores full grade;
// a descendant/ancestor relation scores acceptable-context (capped at 1).
func bestUnusedGrade(hit Ref, rels []Relevant, used []bool) int {
	best, bestIdx := 0, -1
	for j, r := range rels {
		if used[j] || r.SpecID != hit.SpecID {
			continue
		}
		var g int
		switch {
		case r.Clause == hit.Clause:
			g = r.Grade
		case isDescendant(hit.Clause, r.Clause) || isDescendant(r.Clause, hit.Clause):
			g = min(r.Grade, 1)
		default:
			continue
		}
		if g > best {
			best, bestIdx = g, j
		}
	}
	if bestIdx >= 0 {
		used[bestIdx] = true
	}
	return best
}

// idealDCG ranks the distinct relevant grades highest-first.
func idealDCG(rels []Relevant, k int) float64 {
	grades := make([]int, 0, len(rels))
	for _, r := range rels {
		grades = append(grades, r.Grade)
	}
	// simple insertion sort desc (small slices)
	for i := 1; i < len(grades); i++ {
		for j := i; j > 0 && grades[j] > grades[j-1]; j-- {
			grades[j], grades[j-1] = grades[j-1], grades[j]
		}
	}
	var idcg float64
	for i, g := range grades {
		if i >= k {
			break
		}
		idcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	return idcg
}

// recall is the fraction of judged relevant clauses credited within the top k.
//
// IT MUST USE THE SAME NOTION OF RELEVANCE AS ndcg AND mrr. It did not: this
// function matched the clause string EXACTLY, while gradeOf/bestUnusedGrade —
// and therefore nDCG, MRR and Success@1 — deliberately credit a descendant or
// ancestor of a target at grade 1, so that "retrieving finer detail still
// counts".
//
// The disagreement was not academic. On the committed LI/5GC set the harness
// reported nDCG@10 = 0.072 and MRR = 0.042 next to **Recall@10 = 0.000**: a
// ranking that did surface a parent of the target was scored as having found
// nothing at all, and the one number an operator reads as "does retrieval work"
// said no. Two metrics over one query set must not answer two different
// questions — that is how a green gate ends up guarding nothing.
//
// Credit is per judgement and at most once (the same bestUnusedGrade bookkeeping
// nDCG uses), so several retrieved descendants of one target cannot push recall
// above 1.
func recall(ranked []Ref, rels []Relevant, k int) float64 {
	total := 0
	for _, r := range rels {
		if r.Grade > 0 {
			total++
		}
	}
	if total == 0 {
		return 0
	}
	used := make([]bool, len(rels))
	credited := 0
	for i, hit := range ranked {
		if i >= k {
			break
		}
		if bestUnusedGrade(hit, rels, used) > 0 {
			credited++
		}
	}
	return float64(credited) / float64(total)
}

func mrr(ranked []Ref, rels []Relevant, k int) float64 {
	for i, hit := range ranked {
		if i >= k {
			break
		}
		if gradeOf(hit, rels) > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

func success1(ranked []Ref, rels []Relevant) float64 {
	if len(ranked) == 0 {
		return 0
	}
	if gradeOf(ranked[0], rels) >= 2 {
		return 1
	}
	return 0
}

// Mean macro-averages a set of per-query metrics.
func Mean(ms []Metrics) Metrics {
	if len(ms) == 0 {
		return Metrics{}
	}
	var acc Metrics
	for _, m := range ms {
		acc.NDCG5 += m.NDCG5
		acc.NDCG10 += m.NDCG10
		acc.Recall5 += m.Recall5
		acc.Recall10 += m.Recall10
		acc.Recall20 += m.Recall20
		acc.MRR += m.MRR
		acc.Success1 += m.Success1
	}
	n := float64(len(ms))
	return Metrics{
		NDCG5: acc.NDCG5 / n, NDCG10: acc.NDCG10 / n,
		Recall5: acc.Recall5 / n, Recall10: acc.Recall10 / n, Recall20: acc.Recall20 / n,
		MRR: acc.MRR / n, Success1: acc.Success1 / n,
	}
}
