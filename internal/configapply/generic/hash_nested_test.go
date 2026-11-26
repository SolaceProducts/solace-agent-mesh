// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import "testing"

type sampleNestedItem struct {
	Name string         `json:"name"`
	Tags []string       `json:"tags"`
	Meta map[string]int `json:"meta"`
}

type sampleNestedDTO struct {
	Items    []sampleNestedItem  `json:"items"`
	ByName   map[string][]string `json:"byName"`
	Pointer  *sampleNestedItem   `json:"pointer"`
	Open     map[string]any      `json:"open"`
	Embedded sampleNestedItem    `json:"embedded"`
}

func TestHash_NestedNilContainersHashAsEmpty(t *testing.T) {
	authored := sampleNestedDTO{
		Items:   []sampleNestedItem{{Name: "a"}},
		ByName:  map[string][]string{"x": nil},
		Pointer: &sampleNestedItem{Name: "p"},
	}
	served := sampleNestedDTO{
		Items:    []sampleNestedItem{{Name: "a", Tags: []string{}, Meta: map[string]int{}}},
		ByName:   map[string][]string{"x": {}},
		Pointer:  &sampleNestedItem{Name: "p", Tags: []string{}, Meta: map[string]int{}},
		Embedded: sampleNestedItem{Tags: []string{}, Meta: map[string]int{}},
	}
	if mustHash(t, authored) != mustHash(t, served) {
		t.Errorf("nested nil and empty containers hash differently:\nauthored %v\nserved   %v",
			ProjectForHash(authored, nil), ProjectForHash(served, nil))
	}
}

func TestHash_NestedContentStillCounts(t *testing.T) {
	without := sampleNestedDTO{Items: []sampleNestedItem{{Name: "a"}}}
	with := sampleNestedDTO{Items: []sampleNestedItem{{Name: "a", Tags: []string{"t"}}}}
	if mustHash(t, without) == mustHash(t, with) {
		t.Error("a nested list gaining a value must change the hash")
	}
}

func TestHash_OpenObjectValuesKeepNullApartFromEmpty(t *testing.T) {
	null := sampleNestedDTO{Open: map[string]any{"k": nil}}
	empty := sampleNestedDTO{Open: map[string]any{"k": []any{}}}
	if mustHash(t, null) == mustHash(t, empty) {
		t.Error("a null inside an open object must not hash as an empty list")
	}
}
