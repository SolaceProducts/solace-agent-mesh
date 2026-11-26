// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/google/uuid"
)

const (
	MsgInvalidBody       = "Invalid request body"
	MsgValidationFailed  = "Request validation failed"
	MsgInvalidPathParam  = "Invalid path parameter"
	MsgInvalidQueryParam = "Invalid query parameter"
	MsgInvalidHeader     = "Invalid request header"
	MsgBodyTooLarge      = "Request body too large"
	MsgBodyUnreadable    = "Unable to read the request body"

	DefaultPageSize = 20
	MaxPageSize     = 100
	MaxPageNumber   = 1_000_000

	MultipartMemory = 32 << 20
)

type Page struct {
	Number int
	Size   int
}

type SortOrder struct {
	Field string
	Desc  bool
}

type RequestOrigin struct {
	Scheme string
	Host   string
}

func (o RequestOrigin) URL(path string) string {
	return o.Scheme + "://" + o.Host + path
}

func originOf(r *http.Request) RequestOrigin {
	scheme := "http"
	switch {
	case r.TLS != nil:
		scheme = "https"
	case r.Header.Get("X-Forwarded-Proto") == "https":
		scheme = "https"
	}
	return RequestOrigin{Scheme: scheme, Host: r.Host}
}

type IDRequest struct {
	ID string `path:"id" json:"-"`
}

type UploadedFile struct {
	Name        string
	ContentType string
	Data        []byte
}

type RawBody []byte

type pathReader func(w http.ResponseWriter, r *http.Request, name string) (any, bool)

func stringPath(read func(http.ResponseWriter, *http.Request, string) (string, bool)) pathReader {
	return func(w http.ResponseWriter, r *http.Request, name string) (any, bool) {
		return read(w, r, name)
	}
}

func decodeForm(w http.ResponseWriter, r *http.Request, fields []param, req reflect.Value) bool {
	if err := r.ParseMultipartForm(MultipartMemory); err != nil {
		writeBodyReadError(w, err)
		return false
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	details := map[string][]string{}
	status := http.StatusUnprocessableEntity
	known := make(map[string]bool, len(fields))
	for _, fp := range fields {
		known[fp.name] = true
		dst := req.FieldByIndex(fp.index)
		parts := r.MultipartForm.File[fp.name]
		if dst.Type() != uploadedFileType {
			if len(parts) > 0 {
				details[fp.name] = []string{"must be a text field, not a file"}
				status = http.StatusBadRequest
			} else if values := r.MultipartForm.Value[fp.name]; len(values) > 0 {
				dst.SetString(values[0])
			}
			continue
		}
		switch {
		case len(parts) == 0:
			details[fp.name] = []string{"is required"}
			continue
		case len(parts) > 1:
			details[fp.name] = []string{"must be a single file"}
			status = http.StatusBadRequest
			continue
		}
		data, err := readPart(parts[0])
		if err != nil {
			WriteErrorWithCause(w, http.StatusInternalServerError, MsgBodyUnreadable, nil, err)
			return false
		}
		dst.Set(reflect.ValueOf(UploadedFile{Name: parts[0].Filename, ContentType: parts[0].Header.Get(headerContentType), Data: data}))
	}
	flagUnknown := func(name string) {
		if !known[name] {
			details[name] = []string{"unknown field"}
			status = http.StatusBadRequest
		}
	}
	for name := range r.MultipartForm.Value {
		flagUnknown(name)
	}
	for name := range r.MultipartForm.File {
		flagUnknown(name)
	}
	if len(details) > 0 {
		message := MsgValidationFailed
		if status == http.StatusBadRequest {
			message = MsgInvalidBody
		}
		WriteErrorWithCause(w, status, message, details, nil)
		return false
	}
	return true
}

func readPart(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func readRawBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRawBodyBytes))
	if err != nil {
		writeBodyReadError(w, err)
		return nil, false
	}
	return data, true
}

func writeBodyReadError(w http.ResponseWriter, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		WriteErrorWithCause(w, http.StatusRequestEntityTooLarge, MsgBodyTooLarge, nil, err)
		return
	}
	WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidBody, nil, err)
}

func decodeRequest(w http.ResponseWriter, r *http.Request, v any, strict bool) bool {
	var body bytes.Buffer
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(r.Body, &body), r.Body}
	if err := DecodeJSON(r, v); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			WriteErrorWithCause(w, http.StatusRequestEntityTooLarge, MsgBodyTooLarge, nil, err)
			return false
		}
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidBody, decodeDetails(err, body.Bytes(), v), err)
		return false
	}
	if strict && !drainedOneObject(w, r, &body) {
		return false
	}
	if err := validateBody(v); err != nil {
		WriteServiceError(w, err, MsgValidationFailed)
		return false
	}
	return true
}

func drainedOneObject(w http.ResponseWriter, r *http.Request, body *bytes.Buffer) bool {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		writeBodyReadError(w, err)
		return false
	}
	if !isOneJSONObject(body.Bytes()) {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidBody,
			map[string][]string{"body": {"must be one JSON object"}}, nil)
		return false
	}
	return true
}

func isOneJSONObject(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil || value[0] != '{' {
		return false
	}
	return errors.Is(dec.Decode(&value), io.EOF)
}

const maxRefusedBodyBytes = 64 << 10

func refuseBody(w http.ResponseWriter, r *http.Request) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRefusedBodyBytes))
	if err != nil {
		writeBodyReadError(w, err)
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidBody,
		map[string][]string{"body": {"this operation takes no body"}}, nil)
	return false
}

type crossFieldChecker interface {
	CrossFieldProblems() map[string][]string
}

func validateBody(v any) error {
	err := ValidateRequest(v)
	checker, ok := v.(crossFieldChecker)
	if !ok {
		return err
	}
	extra := checker.CrossFieldProblems()
	if len(extra) == 0 {
		return err
	}
	merged := map[string][]string{}
	message := MsgValidationFailed
	if err != nil {
		invalid, isValidation := errors.AsType[*apierr.ValidationError](err)
		if !isValidation {
			return err
		}
		if invalid.Message != "" {
			message = invalid.Message
		}
		if len(invalid.Details) == 0 && invalid.Field != "" {
			merged[invalid.Field] = []string{invalid.Message}
		}
		for field, problems := range invalid.Details {
			merged[field] = slices.Clone(problems)
		}
	}
	for field, problems := range extra {
		merged[field] = append(merged[field], problems...)
	}
	return &apierr.ValidationError{Message: message, Details: merged}
}

func decodeDetails(err error, body []byte, v any) map[string][]string {
	member, memberErr := failingMember(body, reflect.TypeOf(v).Elem())
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		field := typeErr.Field
		if memberTypeErr, ok := errors.AsType[*json.UnmarshalTypeError](memberErr); ok {
			field = strings.TrimSuffix(member+"."+memberTypeErr.Field, ".")
		}
		if field != "" {
			return map[string][]string{field: {expectedType(typeErr.Type)}}
		}
	}
	if field, ok := unknownField(err); ok {
		if inner, ok := unknownField(memberErr); ok {
			field = member + "." + inner
		}
		return map[string][]string{field: {"unknown field"}}
	}
	return nil
}

func unknownField(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	field, ok := strings.CutPrefix(err.Error(), `json: unknown field "`)
	return strings.TrimSuffix(field, `"`), ok
}

func failingMember(body []byte, t reflect.Type) (string, error) {
	fields := map[string]reflect.Type{}
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "-" {
			fields[name] = t.Field(i).Type
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", nil
	}
	for dec.More() {
		key, err := dec.Token()
		var raw json.RawMessage
		if err != nil || dec.Decode(&raw) != nil {
			return "", nil
		}
		name, _ := key.(string)
		fieldType, ok := fields[name]
		if !ok {
			return "", nil
		}
		if err := decodeStrict(raw, reflect.New(fieldType).Interface()); err != nil {
			return name, err
		}
	}
	return "", nil
}

func expectedType(t reflect.Type) string {
	switch t {
	case InstantType:
		return "must be an ISO 8601 date-time"
	case jsonObjectType:
		return "must be a JSON object"
	}
	switch t.Kind() {
	case reflect.String:
		return "must be a string"
	case reflect.Bool:
		return "must be a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "must be an integer"
	case reflect.Float32, reflect.Float64:
		return "must be a number"
	case reflect.Slice, reflect.Array:
		return "must be an array"
	case reflect.Map, reflect.Struct:
		return "must be an object"
	default:
		return "has the wrong type"
	}
}

func PathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !IsCanonicalUUID(id) {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must be a UUID"}}, nil)
		return "", false
	}
	return id, true
}

func IsCanonicalUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

var BuiltinID = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*$`)

func pathUUIDOrBuiltin(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !IsCanonicalUUID(id) && !BuiltinID.MatchString(id) {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must be a UUID or a built-in id"}}, nil)
		return "", false
	}
	return id, true
}

func pathIndex(w http.ResponseWriter, r *http.Request, name string) (any, bool) {
	raw := r.PathValue(name)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || strconv.Itoa(n) != raw {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must be a non-negative integer"}}, nil)
		return 0, false
	}
	return n, true
}

var LowerSnakeCase = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

func pathKey(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	key := r.PathValue(name)
	if !LowerSnakeCase.MatchString(key) {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must be a lowercase snake_case key"}}, nil)
		return "", false
	}
	return key, true
}

func pathText(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	text := r.PathValue(name)
	if text == "" {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must not be empty"}}, nil)
		return "", false
	}
	return text, true
}

func pathPattern(t reflect.Type, f reflect.StructField) func(http.ResponseWriter, *http.Request, string) (string, bool) {
	expr := f.Tag.Get("pattern")
	if expr == "" {
		panic(fmt.Sprintf("apicontract: %s.%s has path option %q and needs a pattern tag", t, f.Name, patternOption))
	}
	re := regexp.MustCompile(expr)
	return func(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
		value := r.PathValue(name)
		if !re.MatchString(value) {
			WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
				map[string][]string{name: {"must match the pattern " + expr}}, nil)
			return "", false
		}
		return value, true
	}
}

func QueryError(status int, param, problem string) error {
	return &CallerError{Status: status, Message: MsgInvalidQueryParam, Details: map[string][]string{param: {problem}}}
}

func ExpandOnly(raw, only string) (asked bool, problem string) {
	if raw == "" {
		return false, ""
	}
	for field := range strings.SplitSeq(raw, ",") {
		switch field = strings.TrimSpace(field); field {
		case "":
		case only:
			asked = true
		default:
			return false, fmt.Sprintf("%q is not one of: %s", field, only)
		}
	}
	if !asked {
		return false, "must not be empty"
	}
	return true, ""
}

func pathFoldedKey(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	key := strings.ToLower(r.PathValue(name))
	if !LowerSnakeCase.MatchString(key) {
		WriteErrorWithCause(w, http.StatusBadRequest, MsgInvalidPathParam,
			map[string][]string{name: {"must be a snake_case key"}}, nil)
		return "", false
	}
	return key, true
}

func CheckUUIDQuery(name, value string) error {
	if value == "" {
		return nil
	}
	if !IsCanonicalUUID(value) {
		return QueryError(http.StatusBadRequest, name, "must be a UUID")
	}
	return nil
}

func CheckOneOfQuery(details map[string][]string, name, value string, allowed []string) {
	if value != "" && !slices.Contains(allowed, value) {
		details[name] = []string{"must be one of " + strings.Join(allowed, ", ")}
	}
}

func OneOfQueryError(name, value string, allowed []string) error {
	details := map[string][]string{}
	CheckOneOfQuery(details, name, value, allowed)
	if len(details) == 0 {
		return nil
	}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: MsgInvalidQueryParam, Details: details}
}

func CheckZipName(field, name string) error {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return nil
	}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: MsgValidationFailed,
		Details: map[string][]string{field: {"must be a .zip archive"}}}
}

func parseListQuery(w http.ResponseWriter, r *http.Request) (Page, bool) {
	p := Page{Number: 1, Size: DefaultPageSize}
	query := r.URL.Query()
	details := map[string][]string{}
	status := http.StatusUnprocessableEntity
	for _, param := range []struct {
		name     string
		dst      *int
		highest  int
		rangeMsg string
	}{
		{"pageNumber", &p.Number, MaxPageNumber, fmt.Sprintf("must be between 1 and %d", MaxPageNumber)},
		{"pageSize", &p.Size, MaxPageSize, fmt.Sprintf("must be between 1 and %d", MaxPageSize)},
	} {
		raw := query.Get(param.name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			details[param.name] = []string{"must be an integer"}
			status = http.StatusBadRequest
		case n < 1 || n > param.highest:
			details[param.name] = []string{param.rangeMsg}
		default:
			*param.dst = n
		}
	}
	if len(details) > 0 {
		WriteErrorWithCause(w, status, MsgInvalidQueryParam, details, nil)
		return Page{}, false
	}
	return p, true
}

func parseSort(w http.ResponseWriter, r *http.Request, fields []string) (SortOrder, bool) {
	values := r.URL.Query()["sort"]
	if len(values) == 0 || (len(values) == 1 && values[0] == "") {
		return SortOrder{}, true
	}
	field, direction, hasDirection := strings.Cut(values[0], ":")
	if !hasDirection {
		direction = "asc"
	}
	problem, status := "", http.StatusUnprocessableEntity
	switch {
	case field == "" || (direction != "asc" && direction != "desc"):
		problem, status = "must be field, field:asc or field:desc", http.StatusBadRequest
	case len(values) > 1:
		problem = "must sort by one field"
	case !slices.Contains(fields, field):
		problem = "must sort by one of " + strings.Join(fields, ", ")
	}
	if problem != "" {
		WriteErrorWithCause(w, status, MsgInvalidQueryParam, map[string][]string{"sort": {problem}}, nil)
		return SortOrder{}, false
	}
	return SortOrder{Field: field, Desc: direction == "desc"}, true
}
