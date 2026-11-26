// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var pathParamPattern = regexp.MustCompile(`\{([^}/]+)\}`)

var wildcardPathParam = regexp.MustCompile(`\{([^}/.]+)\.\.\.\}`)

const wildcardSuffix = "..."

func (b *builder) pathParams(path string, declared map[string]bool) any {
	var fields []reflect.StructField
	for i, match := range pathParamPattern.FindAllStringSubmatch(path, -1) {
		name, wildcard := strings.CutSuffix(match[1], wildcardSuffix)
		if declared[name] {
			continue
		}
		description := fmt.Sprintf("The %s of the addressed resource.", name)
		if wildcard {
			description = fmt.Sprintf("The %s of the addressed resource; it may contain /.", name)
		}
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("Param%d", i),
			Type: reflect.TypeFor[string](),
			Tag:  reflect.StructTag(fmt.Sprintf(`path:"%s" description:"%s"`, name, description)),
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return b.syntheticValue(fields)
}

const (
	sortParam       = "sort"
	pageNumberParam = "pageNumber"
	pageSizeParam   = "pageSize"
)

func (b *builder) pageParams(p Page) any {
	return b.syntheticValue([]reflect.StructField{
		{
			Name: "PageNumber",
			Type: reflect.TypeFor[int](),
			Tag:  reflect.StructTag(fmt.Sprintf(`query:"%s" default:"1" minimum:"1"%s description:"The 1-based number of the page to return."`, pageNumberParam, maximumTag(p.MaxNumber))),
		},
		{
			Name: "PageSize",
			Type: reflect.TypeFor[int](),
			Tag: reflect.StructTag(fmt.Sprintf(`query:"%s" default:"%d" minimum:"1" maximum:"%d" description:"The maximum number of items on a page."`,
				pageSizeParam, p.DefaultSize, p.MaxSize)),
		},
	})
}

func maximumTag(limit int) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf(` maximum:"%d"`, limit)
}

func (b *builder) sortParams(fields []string) any {
	pattern := fmt.Sprintf("^(%s)(:(asc|desc))?$", strings.Join(fields, "|"))
	return b.syntheticValue([]reflect.StructField{{
		Name: "Sort",
		Type: reflect.TypeFor[string](),
		Tag: reflect.StructTag(fmt.Sprintf(`query:"%s" pattern:"%s" example:"%s:asc" description:"The field to sort by and, optionally, the direction, as field, field:asc or field:desc. A field alone sorts ascending. Sortable fields: %s."`,
			sortParam, pattern, fields[0], strings.Join(fields, ", "))),
	}})
}

func (b *builder) syntheticValue(fields []reflect.StructField) any {
	t := reflect.StructOf(fields)
	b.synthetic[t] = true
	return reflect.New(t).Interface()
}

func (b *builder) checkQueryFields(t reflect.Type) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			b.checkQueryFields(field.Type)
			continue
		}
		if tag, ok := field.Tag.Lookup("query"); !ok || tag == "-" || !field.IsExported() {
			continue
		}
		element := field.Type
		for element.Kind() == reflect.Pointer {
			element = element.Elem()
		}
		if element.Kind() == reflect.Slice {
			element = element.Elem()
		}
		switch element.Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Interface:
			if element != reflect.TypeFor[time.Time]() {
				b.problem("query parameter %s.%s must be a scalar or a slice of scalars, not %s", t, field.Name, field.Type)
			}
		}
	}
}

func declaredPathParams(op Operation) map[string]bool {
	declared := map[string]bool{}
	for _, t := range []reflect.Type{op.Query, op.Request} {
		if t == nil {
			continue
		}
		for _, f := range reflect.VisibleFields(t) {
			if tag, ok := f.Tag.Lookup("path"); ok {
				name, _, _ := strings.Cut(tag, ",")
				declared[name] = true
			}
		}
	}
	return declared
}
