// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bm25

import "encoding/json"

type serializedIndex struct {
	InvertedIndex map[string]map[int]int `json:"inverted_index"`
	DocLengths    []int                  `json:"doc_lengths"`
	AvgDocLength  float64                `json:"avg_doc_length"`
	CorpusSize    int                    `json:"corpus_size"`
	K1            float64                `json:"k1"`
	B             float64                `json:"b"`
	Delta         float64                `json:"delta"`
}

func (idx *Index) MarshalJSON() ([]byte, error) {
	s := serializedIndex{
		InvertedIndex: idx.invertedIndex,
		DocLengths:    idx.docLengths,
		AvgDocLength:  idx.avgDocLength,
		CorpusSize:    idx.corpusSize,
		K1:            idx.k1,
		B:             idx.b,
		Delta:         idx.delta,
	}
	return json.Marshal(s)
}

func (idx *Index) UnmarshalJSON(data []byte) error {
	var s serializedIndex
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	idx.invertedIndex = s.InvertedIndex
	idx.docLengths = s.DocLengths
	idx.avgDocLength = s.AvgDocLength
	idx.corpusSize = s.CorpusSize
	idx.k1 = s.K1
	idx.b = s.B
	idx.delta = s.Delta
	return nil
}
