// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bm25

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"testing"
)

type testVectors struct {
	BM25Params struct {
		Method string  `json:"method"`
		K1     float64 `json:"k1"`
		B      float64 `json:"b"`
		Delta  float64 `json:"delta"`
	} `json:"bm25_params"`
	Stopwords    []string   `json:"stopwords"`
	TokenPattern string     `json:"token_pattern"`
	TestCases    []testCase `json:"test_cases"`
}

type testCase struct {
	Name          string     `json:"name"`
	Corpus        []string   `json:"corpus"`
	TokenizedDocs [][]string `json:"tokenized_docs"`
	Queries       []query    `json:"queries"`
}

type query struct {
	Query         string    `json:"query"`
	QueryTokens   []string  `json:"query_tokens"`
	RankedDocIdxs []int     `json:"ranked_doc_indices"`
	Scores        []float64 `json:"scores"`
}

func loadVectors(t *testing.T) *testVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatalf("failed to read test vectors: %v", err)
	}
	var v testVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("failed to parse test vectors: %v", err)
	}
	return &v
}

const scoreTolerance = 1e-6

func assertScoresClose(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > scoreTolerance {
		t.Errorf("%s: score mismatch: got %v, want %v (diff %v)", label, got, want, got-want)
	}
}

func TestTokenization(t *testing.T) {
	vectors := loadVectors(t)
	for _, tc := range vectors.TestCases {
		t.Run(tc.Name, func(t *testing.T) {
			for i, doc := range tc.Corpus {
				got := Tokenize(doc)
				want := tc.TokenizedDocs[i]
				if len(got) != len(want) {
					t.Errorf("doc %d: token count mismatch: got %d, want %d\n  got:  %v\n  want: %v", i, len(got), len(want), got, want)
					continue
				}
				for j := range got {
					if got[j] != want[j] {
						t.Errorf("doc %d, token %d: got %q, want %q", i, j, got[j], want[j])
					}
				}
			}
		})
	}
}

func canonicalOrder(results []Result) {
	slices.SortFunc(results, func(a, b Result) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.DocIndex, b.DocIndex)
	})
}

func pythonVectorResults(docIdxs []int, scores []float64) []Result {
	results := make([]Result, len(docIdxs))
	for i := range docIdxs {
		results[i] = Result{DocIndex: docIdxs[i], Score: scores[i]}
	}
	canonicalOrder(results)
	return results
}

func TestScoring(t *testing.T) {
	vectors := loadVectors(t)
	for _, tc := range vectors.TestCases {
		t.Run(tc.Name, func(t *testing.T) {
			idx := NewIndex(tc.TokenizedDocs)

			for _, q := range tc.Queries {
				if len(q.QueryTokens) == 0 {
					allZero := true
					for _, s := range q.Scores {
						if s > 0 {
							allZero = false
							break
						}
					}
					if allZero {
						continue
					}
				}

				results := idx.Search(q.QueryTokens, len(tc.Corpus))
				want := pythonVectorResults(q.RankedDocIdxs, q.Scores)

				if len(results) != len(want) {
					t.Errorf("query %q: result count mismatch: got %d, want %d", q.Query, len(results), len(want))
					continue
				}

				for i, r := range results {
					if r.DocIndex != want[i].DocIndex {
						t.Errorf("query %q, rank %d: doc index mismatch: got %d, want %d", q.Query, i, r.DocIndex, want[i].DocIndex)
					}
					assertScoresClose(t, fmt.Sprintf("query %q rank %d", q.Query, i), r.Score, want[i].Score)
				}
			}
		})
	}
}

func TestEndToEnd(t *testing.T) {
	vectors := loadVectors(t)
	for _, tc := range vectors.TestCases {
		t.Run(tc.Name, func(t *testing.T) {
			corpus := TokenizeCorpus(tc.Corpus)
			idx := NewIndex(corpus)

			for _, q := range tc.Queries {
				queryTokens := Tokenize(q.Query)

				if len(queryTokens) == 0 {
					allZero := true
					for _, s := range q.Scores {
						if s > 0 {
							allZero = false
							break
						}
					}
					if allZero {
						continue
					}
				}

				results := idx.Search(queryTokens, len(tc.Corpus))
				want := pythonVectorResults(q.RankedDocIdxs, q.Scores)

				if len(results) != len(want) {
					t.Errorf("query %q: result count mismatch: got %d, want %d", q.Query, len(results), len(want))
					continue
				}

				for i, r := range results {
					if r.DocIndex != want[i].DocIndex {
						t.Errorf("query %q, rank %d: doc index mismatch: got %d, want %d", q.Query, i, r.DocIndex, want[i].DocIndex)
					}
					assertScoresClose(t, fmt.Sprintf("query %q rank %d", q.Query, i), r.Score, want[i].Score)
				}
			}
		})
	}
}

func TestSerializationRoundTrip(t *testing.T) {
	corpus := [][]string{
		{"quick", "brown", "fox"},
		{"lazy", "brown", "dog"},
		{"quick", "fox", "jumps"},
	}
	idx := NewIndex(corpus)

	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var idx2 Index
	if err := json.Unmarshal(data, &idx2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	query := []string{"quick", "fox"}
	r1 := idx.Search(query, 3)
	r2 := idx2.Search(query, 3)

	if len(r1) != len(r2) {
		t.Fatalf("result count mismatch after round-trip: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].DocIndex != r2[i].DocIndex {
			t.Errorf("rank %d: doc index mismatch: %d vs %d", i, r1[i].DocIndex, r2[i].DocIndex)
		}
		if math.Abs(r1[i].Score-r2[i].Score) > 1e-12 {
			t.Errorf("rank %d: score mismatch: %v vs %v", i, r1[i].Score, r2[i].Score)
		}
	}
}

func TestEmptyCorpus(t *testing.T) {
	idx := NewIndex(nil)
	results := idx.Search([]string{"hello"}, 10)
	if results != nil {
		t.Errorf("expected nil results for empty corpus, got %v", results)
	}
}

func TestEmptyQuery(t *testing.T) {
	idx := NewIndex([][]string{{"hello", "world"}})
	results := idx.Search(nil, 10)
	if results != nil {
		t.Errorf("expected nil results for empty query, got %v", results)
	}
}

func TestTopK(t *testing.T) {
	corpus := [][]string{
		{"fox", "quick"},
		{"fox", "lazy"},
		{"fox", "brown"},
		{"dog", "cat"},
	}
	idx := NewIndex(corpus)
	results := idx.Search([]string{"fox"}, 2)
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}
