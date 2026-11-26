// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bm25

import (
	"cmp"
	"math"
	"slices"
)

const (
	DefaultK1    = 1.5
	DefaultB     = 0.75
	DefaultDelta = 0.5
)

type Result struct {
	DocIndex int
	Score    float64
}

type Index struct {
	invertedIndex map[string]map[int]int
	docLengths    []int
	avgDocLength  float64
	corpusSize    int
	k1, b, delta  float64
}

func NewIndex(corpus [][]string) *Index {
	idx := &Index{
		invertedIndex: make(map[string]map[int]int),
		docLengths:    make([]int, len(corpus)),
		corpusSize:    len(corpus),
		k1:            DefaultK1,
		b:             DefaultB,
		delta:         DefaultDelta,
	}

	totalLength := 0
	for docIdx, tokens := range corpus {
		idx.docLengths[docIdx] = len(tokens)
		totalLength += len(tokens)

		tf := make(map[string]int)
		for _, tok := range tokens {
			tf[tok]++
		}

		for term, freq := range tf {
			if idx.invertedIndex[term] == nil {
				idx.invertedIndex[term] = make(map[int]int)
			}
			idx.invertedIndex[term][docIdx] = freq
		}
	}

	if len(corpus) > 0 {
		idx.avgDocLength = float64(totalLength) / float64(len(corpus))
	}

	return idx
}

func (idx *Index) Search(queryTokens []string, topK int) []Result {
	if idx.corpusSize == 0 || len(queryTokens) == 0 {
		return nil
	}

	scores := make([]float64, idx.corpusSize)
	for _, term := range queryTokens {
		postings, ok := idx.invertedIndex[term]
		if !ok {
			continue
		}

		df := float64(len(postings))
		n := float64(idx.corpusSize)
		idf := math.Log(1.0 + (n-df+0.5)/(df+0.5))

		for docIdx, freq := range postings {
			dl := float64(idx.docLengths[docIdx])
			tf := float64(freq)
			tfc := tf / (idx.k1*((1.0-idx.b)+idx.b*(dl/idx.avgDocLength)) + tf)
			scores[docIdx] += idf * tfc
		}
	}

	results := make([]Result, 0, idx.corpusSize)
	for i, s := range scores {
		results = append(results, Result{DocIndex: i, Score: s})
	}

	slices.SortFunc(results, func(a, b Result) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.DocIndex, b.DocIndex)
	})

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	return results
}

func (idx *Index) CorpusSize() int {
	return idx.corpusSize
}
