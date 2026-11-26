// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"fmt"
	"reflect"
)

type Stability string

const (
	StabilityDraft  Stability = "draft"
	StabilityStable Stability = "stable"
)

const (
	stabilityExtension = "x-stability-level"
	tagGroupsExtension = "x-tagGroups"
)

func (s Stability) validate() error {
	switch s {
	case StabilityDraft, StabilityStable:
		return nil
	}
	return fmt.Errorf("unknown stability %q", string(s))
}

type Operation struct {
	Method    string
	Path      string
	Published bool
	Stability Stability

	OperationID string
	Summary     string
	Description string
	Tags        []string

	Access Access

	Query          reflect.Type
	Page           *Page
	SortFields     []string
	Request        reflect.Type
	Response       reflect.Type
	SuccessStatus  int
	ExtraErrors    []int
	Form           *Form
	RawBody        *RawBody
	File           *RawContent
	NoContentToo   bool
	CreatedToo     bool
	LocationHeader bool
}

type Form struct {
	Type  reflect.Type
	Parts []FormPart
}

type FormPart struct {
	Name  string
	Field string
	File  bool
}

type RawBody struct {
	ContentType string
	Description string
}

type RawContent struct {
	ContentTypes []string
	Format       string
	Attachment   bool
}

type Access struct {
	Scopes  []string
	Gate    string
	APIKeys bool
	Public  bool
}

type Page struct {
	DefaultSize int
	MaxSize     int
	MaxNumber   int
}

func (o Operation) label() string {
	return o.Method + " " + o.Path
}

func (o Operation) documentedPath() string {
	return wildcardPathParam.ReplaceAllString(o.Path, "{$1}")
}
