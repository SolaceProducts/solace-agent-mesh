// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/swaggest/jsonschema-go"

	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
)

const (
	instantExample = "2022-03-14T15:01:34.000Z"
	patchTypeName  = "Patch["
)

func TypeMapper(t reflect.Type) (*jsonschema.Schema, bool) {
	switch t {
	case InstantType:
		instant := new(jsonschema.Schema).WithType(jsonschema.String.Type()).WithFormat("date-time")
		instant.WithExtraPropertiesItem("nullable", true)
		instant.WithExtraPropertiesItem("example", instantExample)
		return instant, true
	case jsonObjectType:
		object := new(jsonschema.Schema).WithType(jsonschema.Object.Type())
		object.WithExtraPropertiesItem("nullable", true)
		return object, true
	}
	return nil, false
}

const InstantExample = instantExample

func Unwrap(t reflect.Type) (reflect.Type, bool) {
	if t.PkgPath() != reflect.TypeFor[Route]().PkgPath() || !strings.HasPrefix(t.Name(), patchTypeName) {
		return nil, false
	}
	value, ok := t.FieldByName("Value")
	return value.Type, ok
}

func DocPackages() []string {
	return []string{
		reflect.TypeFor[Route]().PkgPath(),
	}
}

func OperationsOf(routes []Route, prefix string, access func(Route) openapigen.Access) []openapigen.Operation {
	ops := make([]openapigen.Operation, 0, len(routes))
	for _, rt := range routes {
		ops = append(ops, OperationOf(rt, prefix, access(rt)))
	}
	return ops
}

func TagDescriptionsOf(routes []Route) map[string]string {
	descriptions := map[string]string{}
	for _, rt := range routes {
		descriptions[rt.Doc.Tag.Name] = rt.Doc.Tag.Text
	}
	return descriptions
}

func TagGroupsOf(routes []Route) map[string]string {
	groups := map[string]string{}
	for _, rt := range routes {
		groups[rt.Doc.Tag.Name] = rt.Doc.Tag.Group
	}
	return groups
}

func OperationOf(rt Route, prefix string, access openapigen.Access) openapigen.Operation {
	req := rt.Handler.Request
	op := openapigen.Operation{
		Method:         rt.Method,
		Path:           prefix + rt.Path,
		Published:      rt.Published,
		Stability:      openapigen.Stability(rt.Stability),
		OperationID:    rt.Doc.ID,
		Summary:        rt.Doc.Summary,
		Description:    rt.Doc.Text,
		Tags:           []string{rt.Doc.Tag.Name},
		ExtraErrors:    rt.Doc.Errors,
		Access:         access,
		SuccessStatus:  successStatus(rt),
		CreatedToo:     rt.Handler.Response.Implements(reflect.TypeFor[CreatingUpsert]()),
		Response:       rt.Handler.Wire,
		SortFields:     sortFieldsOf(req),
		LocationHeader: locatesResult(rt),
		NoContentToo:   rt.Handler.NoContentToo,
	}
	if hasParams(req) {
		op.Query = req
	}
	if hasPage(req) {
		op.Page = &openapigen.Page{DefaultSize: DefaultPageSize, MaxSize: MaxPageSize, MaxNumber: MaxPageNumber}
	}
	if rt.Handler.HasBody {
		op.Request = req
	}
	if len(rt.Handler.FormParts) > 0 {
		op.Form = formOf(req, rt.Handler.FormParts)
	}
	if rt.Handler.RawBody {
		op.RawBody = &openapigen.RawBody{ContentType: octetStream, Description: "The bytes, sent as the request body."}
	}
	if rt.Handler.Stream {
		op.File = &openapigen.RawContent{ContentTypes: FileTypesOf(rt)}
	}
	if rt.Handler.IsFile {
		op.File = &openapigen.RawContent{ContentTypes: FileTypesOf(rt), Format: "binary", Attachment: true}
	}
	return op
}

const (
	octetStream = "application/octet-stream"
	anyFileType = "*/*"
)

func FileTypesOf(rt Route) []string {
	if len(rt.Doc.FileTypes) > 0 {
		return rt.Doc.FileTypes
	}
	return []string{octetStream}
}

func formOf(req reflect.Type, parts []FormPart) *openapigen.Form {
	form := &openapigen.Form{Type: req}
	for _, p := range parts {
		form.Parts = append(form.Parts, openapigen.FormPart{Name: p.Name, Field: p.Field, File: p.File})
	}
	return form
}

func successStatus(rt Route) int {
	switch {
	case rt.Handler.Status != 0:
		return rt.Handler.Status
	case rt.Handler.Wire == nil:
		return http.StatusNoContent
	case createsResource(rt):
		return http.StatusCreated
	}
	return http.StatusOK
}

func locatesResult(rt Route) bool {
	switch successStatus(rt) {
	case http.StatusAccepted:
		return true
	case http.StatusCreated:
		return idField(rt.Handler.Response) != nil
	}
	return false
}

func createsResource(rt Route) bool {
	return rt.Method == http.MethodPost && !isList(rt.Handler.Response)
}

func isList(t reflect.Type) bool {
	return t.Implements(reflect.TypeFor[pageWriter]())
}

func hasParams(req reflect.Type) bool {
	for _, f := range reflect.VisibleFields(req) {
		_, inQuery := f.Tag.Lookup("query")
		_, inPath := f.Tag.Lookup("path")
		_, inHeader := f.Tag.Lookup("header")
		if inQuery || inPath || inHeader {
			return true
		}
	}
	return false
}

func hasPage(req reflect.Type) bool {
	for _, f := range reflect.VisibleFields(req) {
		if f.Type == pageType {
			return true
		}
	}
	return false
}

func sortFieldsOf(req reflect.Type) []string {
	for _, f := range reflect.VisibleFields(req) {
		if f.Type == sortOrderType {
			return strings.Split(f.Tag.Get("sort"), ",")
		}
	}
	return nil
}
