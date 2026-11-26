// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bm25

import (
	"math"
	"slices"
	"testing"
)

func TestParametersMatchPythonFixture(t *testing.T) {
	vectors := loadVectors(t)
	if vectors.BM25Params.Method != "lucene" {
		t.Errorf("fixture method = %q, want %q - Go implements the Lucene TFC formula", vectors.BM25Params.Method, "lucene")
	}
	if DefaultK1 != vectors.BM25Params.K1 {
		t.Errorf("DefaultK1 = %v, fixture says %v", DefaultK1, vectors.BM25Params.K1)
	}
	if DefaultB != vectors.BM25Params.B {
		t.Errorf("DefaultB = %v, fixture says %v", DefaultB, vectors.BM25Params.B)
	}
	if DefaultDelta != vectors.BM25Params.Delta {
		t.Errorf("DefaultDelta = %v, fixture says %v", DefaultDelta, vectors.BM25Params.Delta)
	}
}

func TestStopwordsMatchPythonFixture(t *testing.T) {
	vectors := loadVectors(t)
	fixtureSet := make(map[string]bool, len(vectors.Stopwords))
	for _, w := range vectors.Stopwords {
		fixtureSet[w] = true
	}
	if len(fixtureSet) != len(stopwords) {
		t.Errorf("stopword count mismatch: go=%d fixture=%d", len(stopwords), len(fixtureSet))
	}
	for w := range stopwords {
		if !fixtureSet[w] {
			t.Errorf("Go stopword %q absent from fixture", w)
		}
	}
	for w := range fixtureSet {
		if !stopwords[w] {
			t.Errorf("fixture stopword %q absent from Go set", w)
		}
	}
}

func TestTokenizerEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"lowercases uppercase tokens", "The QUICK Brown FOX", []string{"quick", "brown", "fox"}},
		{"stopwords are case-insensitive", "THE that BuT of", []string{}},
		{"single-char tokens dropped", "a b cat dog e", []string{"cat", "dog"}},
		{"apostrophe splits token", "don't forget", []string{"don", "forget"}},
		{"hyphen splits token", "state-of-the-art", []string{"state", "art"}},
		{"dot splits token", "U.S.A. tools", []string{"tools"}},
		{"accented latin chars retained", "café résumé naïve", []string{"café", "résumé", "naïve"}},
		{"cjk runs retained", "hello 你好 world 世界", []string{"hello", "你好", "world", "世界"}},
		{"underscore joins token", "user_id session_token", []string{"user_id", "session_token"}},
		{"digit runs kept", "123 abc456 42", []string{"123", "abc456", "42"}},
		{"single digit dropped", "version 3 release", []string{"version", "release"}},
		{"decimal splits", "pi is 3.14 and e 2.71", []string{"pi", "14", "71"}},
		{"empty string", "", []string{}},
		{"whitespace only", "   \t\n  ", []string{}},
		{"only stopwords", "the an and but if", []string{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Tokenize(c.input)
			if got == nil {
				got = []string{}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestSearchTopKBoundaries(t *testing.T) {
	corpus := [][]string{
		{"fox", "quick"},
		{"fox", "lazy"},
		{"fox", "brown"},
		{"dog", "cat"},
	}
	idx := NewIndex(corpus)

	t.Run("topK equal to corpus size", func(t *testing.T) {
		if got := len(idx.Search([]string{"fox"}, 4)); got != 4 {
			t.Errorf("got %d results, want 4", got)
		}
	})

	t.Run("topK larger than corpus returns at most corpus size", func(t *testing.T) {
		got := idx.Search([]string{"fox"}, 1000)
		if len(got) != 4 {
			t.Errorf("got %d results, want 4 (capped at corpus size)", len(got))
		}
	})

	t.Run("topK zero returns all scored docs", func(t *testing.T) {
		got := idx.Search([]string{"fox"}, 0)
		if len(got) != 4 {
			t.Errorf("got %d results for topK=0, want 4 (no clamp)", len(got))
		}
	})

	t.Run("topK negative also returns all", func(t *testing.T) {
		got := idx.Search([]string{"fox"}, -5)
		if len(got) != 4 {
			t.Errorf("got %d results for topK=-5, want 4 (no clamp)", len(got))
		}
	})
}

func TestSearchOrderingDeterministic(t *testing.T) {
	corpus := [][]string{
		{"fox", "quick"},
		{"fox", "quick"},
		{"fox", "quick"},
		{"fox", "quick"},
	}
	idx := NewIndex(corpus)
	got := idx.Search([]string{"fox", "quick"}, 4)
	if len(got) != 4 {
		t.Fatalf("expected 4 results, got %d", len(got))
	}
	for i, r := range got {
		if r.DocIndex != i {
			t.Errorf("rank %d: DocIndex = %d, want %d (ties must break by ascending doc index)", i, r.DocIndex, i)
		}
	}
}

func TestSearchAllZeroScoresReturnsAllDocs(t *testing.T) {
	corpus := [][]string{
		{"apple", "banana"},
		{"cherry", "date"},
	}
	idx := NewIndex(corpus)
	got := idx.Search([]string{"zzz_absent_term"}, 10)
	for _, r := range got {
		if r.Score != 0 {
			t.Errorf("expected zero score for absent term, got DocIndex=%d Score=%v", r.DocIndex, r.Score)
		}
	}
}

func TestBM25ScoreFormula(t *testing.T) {
	corpus := [][]string{
		{"fox", "fox"},
		{"fox", "dog"},
		{"dog", "dog"},
	}
	idx := NewIndex(corpus)
	got := idx.Search([]string{"fox"}, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	idf := math.Log(1.0 + (3.0-2.0+0.5)/(2.0+0.5))
	want0 := idf * (2.0 / 3.5)
	want1 := idf * (1.0 / 2.5)

	if got[0].DocIndex != 0 {
		t.Errorf("top result DocIndex = %d, want 0", got[0].DocIndex)
	}
	if math.Abs(got[0].Score-want0) > 1e-12 {
		t.Errorf("got[0].Score = %v, want %v", got[0].Score, want0)
	}
	if got[1].DocIndex != 1 {
		t.Errorf("rank 1 DocIndex = %d, want 1", got[1].DocIndex)
	}
	if math.Abs(got[1].Score-want1) > 1e-12 {
		t.Errorf("got[1].Score = %v, want %v", got[1].Score, want1)
	}
	if got[2].DocIndex != 2 || got[2].Score != 0 {
		t.Errorf("rank 2 = (%d, %v), want (2, 0)", got[2].DocIndex, got[2].Score)
	}
}

func TestTokenizeCorpusMatchesIndividual(t *testing.T) {
	docs := []string{
		"The quick brown fox",
		"A lazy brown dog",
		"",
		"THE THAT and",
		"café naïve 你好",
	}
	batch := TokenizeCorpus(docs)
	if len(batch) != len(docs) {
		t.Fatalf("TokenizeCorpus returned %d results for %d docs", len(batch), len(docs))
	}
	for i, doc := range docs {
		want := Tokenize(doc)
		if want == nil {
			want = []string{}
		}
		got := batch[i]
		if got == nil {
			got = []string{}
		}
		if !slices.Equal(got, want) {
			t.Errorf("doc %d: TokenizeCorpus = %v, Tokenize = %v", i, got, want)
		}
	}
}
