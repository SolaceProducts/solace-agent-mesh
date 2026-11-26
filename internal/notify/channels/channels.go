// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package channels

type Payload struct {
	Title   string
	Message string
	Fields  map[string]any
}

func (p Payload) asMap() map[string]any {
	m := make(map[string]any, len(p.Fields)+2)
	for k, v := range p.Fields {
		m[k] = v
	}
	if p.Title != "" {
		m["title"] = p.Title
	}
	if p.Message != "" {
		m["message"] = p.Message
	}
	return m
}
