// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
)

const headerContentType = "Content-Type"

type Handler struct {
	Request      reflect.Type
	Response     reflect.Type
	Wire         reflect.Type
	HasBody      bool
	FormParts    []FormPart
	RawBody      bool
	IsFile       bool
	NoContentToo bool
	Stream       bool
	Resource     reflect.Type
	Status       int
	serve        http.HandlerFunc
	writeSample  http.HandlerFunc
}

type FormPart struct {
	Name  string
	Field string
	File  bool
}

type List[T any] struct {
	Items []T
	Total int
}

func (l List[T]) write(w http.ResponseWriter, p Page) {
	WriteList(w, l.Items, p, l.Total)
}

func (List[T]) wireType() reflect.Type {
	return reflect.TypeFor[PaginatedResponse[T]]()
}

func ListOf[S, T any](resp *PaginatedResponse[S], to func(*S) T) List[T] {
	items := make([]T, len(resp.Data))
	for i := range resp.Data {
		items[i] = to(&resp.Data[i])
	}
	return List[T]{Items: items, Total: resp.Meta.Pagination.Count}
}

type HTMLPage []byte

func (HTMLPage) wireType() reflect.Type { return nil }

func (HTMLPage) status() int { return http.StatusOK }

func (p HTMLPage) WriteTo(w http.ResponseWriter) {
	w.Header().Set(headerContentType, "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(p.status())
	_, _ = w.Write(p)
}

type pageWriter interface {
	write(w http.ResponseWriter, p Page)
}

type selfWriter interface {
	WriteTo(w http.ResponseWriter)
	status() int
}

type wireTyper interface {
	wireType() reflect.Type
}

type optionalResponse interface {
	mayBeAbsent() bool
}

func isOptional[Resp any]() bool {
	var zero Resp
	o, ok := any(zero).(optionalResponse)
	return ok && o.mayBeAbsent()
}

func statusOf[Resp any]() int {
	var zero Resp
	if s, ok := any(zero).(selfWriter); ok {
		return s.status()
	}
	return 0
}

func wireOf[Resp any]() reflect.Type {
	var zero Resp
	if l, ok := any(zero).(wireTyper); ok {
		return l.wireType()
	}
	if reflect.TypeFor[Resp]() == reflect.TypeFor[struct{}]() {
		return nil
	}
	return reflect.TypeFor[DataResponse[Resp]]()
}

func HandleEnvelope(request, wire, resource reflect.Type, serve http.HandlerFunc) Handler {
	return Handler{
		Request:  request,
		Response: wire,
		Wire:     wire,
		Resource: resource,
		HasBody:  true,
		serve:    serve,
		writeSample: func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, http.StatusOK, reflect.New(wire).Interface())
		},
	}
}

func HandleStream(request reflect.Type, serve http.HandlerFunc) Handler {
	return Handler{
		Request:  request,
		Response: reflect.TypeFor[struct{}](),
		Stream:   true,
		Status:   http.StatusOK,
		serve:    serve,
		writeSample: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(headerContentType, "text/event-stream")
			w.WriteHeader(http.StatusOK)
		},
	}
}

func Handle[Req, Resp any](fn func(context.Context, Req) (Resp, error), failure string) Handler {
	b := newBinding(reflect.TypeFor[Req]())
	id := idField(reflect.TypeFor[Resp]())
	return Handler{
		Request:      reflect.TypeFor[Req](),
		Response:     reflect.TypeFor[Resp](),
		Wire:         wireOf[Resp](),
		HasBody:      b.body,
		FormParts:    formPartsOf(reflect.TypeFor[Req](), b),
		RawBody:      b.raw != nil,
		IsFile:       reflect.TypeFor[Resp]() == fileResponseType,
		Status:       statusOf[Resp](),
		NoContentToo: isOptional[Resp](),
		writeSample: func(w http.ResponseWriter, r *http.Request) {
			writeResponse(w, r, sampleOf[Resp](), Page{Number: 1, Size: 1}, id)
		},
		serve: func(w http.ResponseWriter, r *http.Request) {
			var req Req
			p, ok := b.bind(w, r, reflect.ValueOf(&req).Elem())
			if !ok {
				return
			}
			if r.MultipartForm != nil {
				defer func() { _ = r.MultipartForm.RemoveAll() }()
			}
			resp, err := fn(r.Context(), req)
			if abandoned(r, err) {
				slog.Default().DebugContext(r.Context(), msgRequestAbandoned, "logger_name", "platform-admin", "route", r.Pattern)
				return
			}
			if err != nil {
				writeHandlerError(w, err, failure)
				return
			}
			writeResponse(w, r, resp, p, id)
		},
	}
}

func sampleOf[Resp any]() Resp {
	var sample Resp
	if f, ok := any(sample).(FileResponse); ok {
		f.Body = io.NopCloser(strings.NewReader(""))
		return any(f).(Resp)
	}
	if a, ok := any(sample).(Accepted); ok {
		a.Location = "/sample"
		return any(a).(Resp)
	}
	return sample
}

func formPartsOf(t reflect.Type, b binding) []FormPart {
	parts := make([]FormPart, 0, len(b.form))
	for _, p := range b.form {
		field := t.FieldByIndex(p.index)
		parts = append(parts, FormPart{Name: p.name, Field: field.Name, File: field.Type == uploadedFileType})
	}
	return parts
}

type headerSetter interface {
	ResponseHeaders() map[string]string
}

func writeResponse[T any](w http.ResponseWriter, r *http.Request, resp T, p Page, id []int) {
	if h, ok := any(resp).(headerSetter); ok {
		for name, value := range h.ResponseHeaders() {
			w.Header().Set(name, value)
		}
	}
	if l, ok := any(resp).(pageWriter); ok {
		l.write(w, p)
		return
	}
	if s, ok := any(resp).(selfWriter); ok {
		s.WriteTo(w)
		return
	}
	if _, ok := any(resp).(struct{}); ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		WriteResource(w, http.StatusOK, resp)
		return
	}
	if l, ok := any(resp).(locator); ok {
		w.Header().Set("Location", l.Location())
	} else if id != nil {
		if v, err := reflect.ValueOf(resp).FieldByIndexErr(id); err == nil {
			w.Header().Set("Location", r.URL.Path+"/"+url.PathEscape(v.String()))
		}
	}
	WriteResource(w, http.StatusCreated, resp)
}

type locator interface {
	Location() string
}

const locationKeyTag = "locationKey"

type FileResponse struct {
	ContentType string
	Filename    string
	Size        int64
	Inline      bool
	Headers     map[string]string
	Body        io.ReadCloser
}

func (FileResponse) wireType() reflect.Type { return nil }

func (FileResponse) status() int { return http.StatusOK }

func (f FileResponse) WriteTo(w http.ResponseWriter) {
	defer func() { _ = f.Body.Close() }()
	disposition := "attachment"
	if f.Inline {
		disposition = "inline"
	}
	h := w.Header()
	for name, value := range f.Headers {
		h.Set(name, value)
	}
	h.Set(headerContentType, f.ContentType)
	h.Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(f.Filename))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	if f.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	}
	w.WriteHeader(f.status())
	_, _ = io.Copy(w, f.Body)
}

func idField(t reflect.Type) []int {
	if t.Kind() != reflect.Struct {
		return nil
	}
	var key []int
	for _, f := range reflect.VisibleFields(t) {
		if f.Type.Kind() != reflect.String {
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "id" {
			return f.Index
		}
		if _, ok := f.Tag.Lookup(locationKeyTag); ok && key == nil {
			key = f.Index
		}
	}
	return key
}

type CallerError struct {
	Status  int
	Message string
	Details map[string][]string
	Cause   error
	ErrorID string
}

func (e *CallerError) Error() string { return e.Message }
func (e *CallerError) Unwrap() error { return e.Cause }

type LocatedError struct {
	Err      error
	Location string
}

func (e *LocatedError) Error() string { return e.Err.Error() }
func (e *LocatedError) Unwrap() error { return e.Err }

type RetryAfterError struct {
	err   error
	After time.Duration
}

func (e *RetryAfterError) Error() string { return e.err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.err }

func RetryAfter(err error, after time.Duration) error {
	return &RetryAfterError{err: err, After: max(after, time.Second)}
}

const msgRequestAbandoned = "request abandoned by the client"

func abandoned(r *http.Request, err error) bool {
	return r.Context().Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

func writeHandlerError(w http.ResponseWriter, err error, failure string) {
	if e, ok := errors.AsType[*RetryAfterError](err); ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(e.After/time.Second)))
	}
	if e, ok := errors.AsType[*LocatedError](err); ok {
		w.Header().Set("Location", e.Location)
	}
	if at, ok := budget.RetryAt(err); ok {
		budget.SetRetryAfter(w, at, time.Now())
	}
	if e, ok := errors.AsType[*CallerError](err); ok {
		if e.ErrorID != "" {
			WriteErrorWithLoggedID(w, e.Status, e.Message, e.Details, e.ErrorID)
			return
		}
		WriteErrorWithCause(w, e.Status, e.Message, e.Details, e.Cause)
		return
	}
	WriteServiceError(w, err, failure)
}

type binding struct {
	path   []param
	query  []param
	header []param
	form   []param
	custom []customField
	page   []int
	sort   sortParam
	raw    []int
	origin []int
	body   bool

	strictQuery bool
	strict      bool
}

type StrictRequest struct{}

func (StrictRequest) refusesUnread() bool { return true }

type strictMarker interface{ refusesUnread() bool }

type customField struct {
	index []int
	read  func(http.ResponseWriter, *http.Request) (any, bool)
}

var fieldReaders = map[reflect.Type]func(http.ResponseWriter, *http.Request) (any, bool){}

func RegisterFieldReader[T any](read func(http.ResponseWriter, *http.Request) (T, bool)) {
	typ := reflect.TypeFor[T]()
	if _, dup := fieldReaders[typ]; dup {
		panic(fmt.Sprintf("apicontract: a field reader for %s is already registered", typ))
	}
	fieldReaders[typ] = func(w http.ResponseWriter, r *http.Request) (any, bool) {
		return read(w, r)
	}
}

type sortParam struct {
	index  []int
	fields []string
}

type param struct {
	name     string
	index    []int
	read     pathReader
	key      bool
	text     bool
	nonEmpty bool
}

var (
	pageType         = reflect.TypeFor[Page]()
	sortOrderType    = reflect.TypeFor[SortOrder]()
	uploadedFileType = reflect.TypeFor[UploadedFile]()
	rawBodyType      = reflect.TypeFor[RawBody]()
	originType       = reflect.TypeFor[RequestOrigin]()
	fileResponseType = reflect.TypeFor[FileResponse]()
)

func newBinding(t reflect.Type) binding {
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("apicontract: request type %s is not a struct", t))
	}
	var b binding
	if marker, ok := reflect.Zero(t).Interface().(strictMarker); ok && marker.refusesUnread() {
		b.strict, b.strictQuery = true, true
	}
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || f.Anonymous {
			continue
		}
		pathName, fromPath := f.Tag.Lookup("path")
		queryName, fromQuery := f.Tag.Lookup("query")
		headerName, fromHeader := f.Tag.Lookup("header")
		formName, fromForm := f.Tag.Lookup("form")
		isPage := f.Type == pageType
		isSort := f.Type == sortOrderType
		isRaw := f.Type == rawBodyType
		isOrigin := f.Type == originType
		reader, isCustom := fieldReaders[f.Type]
		jsonName, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !fromPath && !fromQuery && !fromHeader && !fromForm && !isPage && !isSort && !isRaw && !isOrigin && !isCustom {
			if jsonName == "-" {
				panic(fmt.Sprintf(`apicontract: %s.%s is tagged json:"-" but nothing fills it; give it a path, query, header or form tag, or register a field reader for its type`, t, f.Name))
			}
			b.body = b.body || jsonName != "-"
			continue
		}
		if jsonName != "-" {
			panic(fmt.Sprintf(`apicontract: %s.%s is not read from the JSON body; tag it json:"-"`, t, f.Name))
		}
		if _, ok := f.Tag.Lookup("validate"); ok {
			panic(fmt.Sprintf("apicontract: %s.%s is not read from the JSON body; check it in the handler, not with a validate rule", t, f.Name))
		}
		switch {
		case isOrigin:
			b.origin = f.Index
		case isPage:
			b.page = f.Index
		case isCustom:
			b.custom = append(b.custom, customField{index: f.Index, read: reader})
		case isSort:
			fields := strings.Split(f.Tag.Get("sort"), ",")
			if slices.Contains(fields, "") {
				panic(fmt.Sprintf(`apicontract: %s.%s needs the fields it may sort by, tagged sort:"name,createdTime"`, t, f.Name))
			}
			b.sort = sortParam{index: f.Index, fields: fields}
		case isRaw:
			b.raw = f.Index
		case fromForm:
			if f.Type != uploadedFileType && f.Type.Kind() != reflect.String {
				panic(fmt.Sprintf("apicontract: %s.%s must be an uploadedFile or a string", t, f.Name))
			}
			b.form = append(b.form, param{name: formName, index: f.Index})
		case fromPath:
			b.path = append(b.path, pathParam(t, f, pathName))
		case f.Type.Kind() != reflect.String:
			panic(fmt.Sprintf("apicontract: %s.%s must be a string", t, f.Name))
		case fromHeader:
			b.header = append(b.header, param{name: headerName, index: f.Index})
		default:
			name, option, _ := strings.Cut(queryName, ",")
			if option != "" && option != queryNonEmptyOption {
				panic(fmt.Sprintf("apicontract: %s.%s has unknown query option %q", t, f.Name, option))
			}
			b.query = append(b.query, param{name: name, index: f.Index, nonEmpty: option == queryNonEmptyOption})
			b.strictQuery = b.strictQuery || option == queryNonEmptyOption
		}
	}
	sources := 0
	for _, used := range []bool{b.body, b.form != nil, b.raw != nil} {
		if used {
			sources++
		}
	}
	if sources > 1 {
		panic(fmt.Sprintf("apicontract: %s reads its body as more than one of JSON, a form and raw bytes", t))
	}
	return b
}

func pathParam(t reflect.Type, f reflect.StructField, tag string) param {
	name, option, _ := strings.Cut(tag, ",")
	p := param{name: name, index: f.Index, key: option == "key" || option == foldedKeyOption, text: option == textOption}
	switch {
	case f.Type.Kind() == reflect.Int && option == "":
		p.read = pathIndex
	case f.Type.Kind() != reflect.String:
		panic(fmt.Sprintf("apicontract: %s.%s must be a string, or an int for an index", t, f.Name))
	case option == "":
		p.read = stringPath(PathUUID)
	case option == "key":
		p.read = stringPath(pathKey)
	case option == foldedKeyOption:
		p.read = stringPath(pathFoldedKey)
	case option == "builtin":
		p.read = stringPath(pathUUIDOrBuiltin)
	case option == textOption:
		p.read = stringPath(pathText)
	case option == patternOption:
		p.read = stringPath(pathPattern(t, f))
	default:
		panic(fmt.Sprintf("apicontract: %s.%s has unknown path option %q", t, f.Name, option))
	}
	return p
}

const textOption = "text"

const patternOption = "pattern"

const foldedKeyOption = "foldedkey"

const queryNonEmptyOption = "nonempty"

func (b binding) readQuery(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	if !b.strictQuery {
		return r.URL.Query(), true
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidQueryParam,
			map[string][]string{"query": {"must be a well-formed query string"}}, nil)
		return nil, false
	}
	details := map[string][]string{}
	for name, values := range query {
		switch {
		case !b.readsQueryParam(name):
			details[name] = []string{"is not a query parameter of this operation"}
		case b.strict && len(values) > 1 && name != "sort":
			details[name] = []string{"must be given once"}
		}
	}
	for _, qp := range b.query {
		if qp.nonEmpty && slices.Contains(query[qp.name], "") {
			details[qp.name] = []string{"must not be empty; leave it out to list everything"}
		}
	}
	if len(details) > 0 {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidQueryParam, details, nil)
		return nil, false
	}
	return query, true
}

func (b binding) readsQueryParam(name string) bool {
	switch name {
	case "pageNumber", "pageSize":
		return b.page != nil
	case "sort":
		return b.sort.index != nil
	}
	return slices.ContainsFunc(b.query, func(qp param) bool { return qp.name == name })
}

func (b binding) bind(w http.ResponseWriter, r *http.Request, req reflect.Value) (Page, bool) {
	for _, pp := range b.path {
		v, ok := pp.read(w, r, pp.name)
		if !ok {
			return Page{}, false
		}
		req.FieldByIndex(pp.index).Set(reflect.ValueOf(v))
	}
	var p Page
	if b.page != nil {
		var ok bool
		if p, ok = parseListQuery(w, r); !ok {
			return Page{}, false
		}
		req.FieldByIndex(b.page).Set(reflect.ValueOf(p))
	}
	if b.sort.index != nil {
		order, ok := parseSort(w, r, b.sort.fields)
		if !ok {
			return Page{}, false
		}
		req.FieldByIndex(b.sort.index).Set(reflect.ValueOf(order))
	}
	if b.origin != nil {
		req.FieldByIndex(b.origin).Set(reflect.ValueOf(originOf(r)))
	}
	query, ok := b.readQuery(w, r)
	if !ok {
		return Page{}, false
	}
	for _, qp := range b.query {
		req.FieldByIndex(qp.index).SetString(query.Get(qp.name))
	}
	for _, hp := range b.header {
		req.FieldByIndex(hp.index).SetString(r.Header.Get(hp.name))
	}
	for _, cf := range b.custom {
		v, ok := cf.read(w, r)
		if !ok {
			return Page{}, false
		}
		req.FieldByIndex(cf.index).Set(reflect.ValueOf(v))
	}
	switch {
	case b.body:
		return p, decodeRequest(w, r, req.Addr().Interface(), b.strict)
	case b.form != nil:
		return p, decodeForm(w, r, b.form, req)
	case b.raw != nil:
		data, ok := readRawBody(w, r)
		req.FieldByIndex(b.raw).SetBytes(data)
		return p, ok
	case b.strict:
		return p, refuseBody(w, r)
	}
	return p, true
}

type PathVar struct {
	Name string
	Key  bool
	Text bool
}

type QueryVar struct {
	Name string
}

type Binding struct {
	Path    []PathVar
	Query   []QueryVar
	HasPage bool
}

func BindingOf(request reflect.Type) Binding {
	b := newBinding(request)
	out := Binding{HasPage: b.page != nil}
	for _, p := range b.path {
		out.Path = append(out.Path, PathVar{Name: p.name, Key: p.key, Text: p.text})
	}
	for _, q := range b.query {
		out.Query = append(out.Query, QueryVar{Name: q.name})
	}
	return out
}

func IsList(response reflect.Type) bool { return isList(response) }

func (h Handler) Bound() bool { return h.serve != nil }
