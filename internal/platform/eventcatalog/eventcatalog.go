// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	FieldName           = "name"
	FieldKey            = "key"
	FieldDescription    = "description"
	FieldEntrypointID   = "entrypointId"
	FieldTopicPattern   = "topicPattern"
	FieldPayloadSchema  = "payloadSchema"
	FieldExamplePayload = "examplePayload"
	FieldEnabled        = "enabled"
)

const (
	MaxNameLength        = 255
	MaxKeyLength         = 64
	MaxDescriptionLength = 2000
	MaxDocumentBytes     = 64 * 1024
	MaxSchemaNesting     = 32

	MaxExampleBytes       = 8 * 1024
	MaxExampleNesting     = 32
	MaxSchemaPatterns     = 16
	MaxSchemaPatternBytes = 256
	MaxSchemaPatternSteps = 256
	MaxSchemaNodes        = 256
)

const (
	eventMeshEntrypointType = "event_mesh"
)

const (
	resourceName        = "event catalog entry"
	msgInvalid          = "The event catalog entry is invalid."
	msgTaken            = "An event catalog entry already uses this value."
	problemEmpty        = "cannot be empty"
	problemNotUUID      = "must be a UUID"
	problemNotEventMesh = "must name an existing event_mesh entrypoint"
	problemBadKey       = "must be lowercase snake_case: a lowercase letter, then lowercase letters, digits and single underscores"
	problemBadSchema    = "must be a valid, self-contained JSON Schema document"
	problemRefs         = "must not use $ref, $dynamicRef or $recursiveRef; give a self-contained schema"
	problemNameTaken    = "another event catalog entry has this name"
	problemKeyTaken     = "another event catalog entry has this key"
)

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

type Entry struct {
	ID             string
	Name           string
	Key            string
	Description    string
	EntrypointID   string
	TopicPattern   string
	PayloadSchema  map[string]any
	ExamplePayload map[string]any
	Enabled        bool
	CreatedBy      string
	UpdatedBy      string
	CreatedTime    int64
	UpdatedTime    int64
}

type Fields struct {
	Name           string
	Key            string
	Description    string
	EntrypointID   string
	TopicPattern   string
	PayloadSchema  map[string]any
	ExamplePayload map[string]any
	Enabled        bool
}

type Change struct {
	Name              *string
	Description       *string
	EntrypointID      *string
	TopicPattern      *string
	Enabled           *bool
	SetPayloadSchema  bool
	PayloadSchema     map[string]any
	SetExamplePayload bool
	ExamplePayload    map[string]any
	Problems          map[string][]string
}

type ListOptions = store.EventCatalogListOptions

type TakenError struct {
	Details map[string][]string
}

func (e *TakenError) Error() string { return msgTaken }

func (e *TakenError) Unwrap() error { return &apierr.ConflictError{Message: msgTaken} }

type Service struct {
	entries     *store.EventCatalogRepo
	entrypoints *store.GatewayRepo
	inUse       func(ctx context.Context, entry *Entry) (bool, error)
}

func NewService(db *store.DB) *Service {
	return &Service{
		entries:     store.NewEventCatalogRepo(db),
		entrypoints: store.NewGatewayRepo(db),
		inUse:       entryInUse,
	}
}

func entryInUse(context.Context, *Entry) (bool, error) {
	return false, nil
}

func (s *Service) Create(ctx context.Context, f Fields, actor string) (*Entry, error) {
	row := &store.EventCatalogEntry{ID: idgen.New(), CreatedBy: actor, UpdatedBy: actor}
	if err := s.check(ctx, &f, row.ID, true, nil, checkEverything); err != nil {
		return nil, err
	}
	if err := fill(row, f); err != nil {
		return nil, err
	}
	if err := s.entries.Create(ctx, row); err != nil {
		return nil, takenOr(err)
	}
	return toEntry(row)
}

func (s *Service) Get(ctx context.Context, id string) (*Entry, error) {
	row, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return toEntry(row)
}

func (s *Service) List(ctx context.Context, opts ListOptions) ([]*Entry, int, error) {
	rows, total, err := s.entries.ListPage(ctx, opts)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*Entry, len(rows))
	for i, row := range rows {
		if out[i], err = toEntry(row); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, id string, c Change, actor string) (*Entry, error) {
	row, err := s.changed(ctx, id, c)
	if err != nil {
		return nil, err
	}
	row.UpdatedBy = actor
	found, err := s.entries.Update(ctx, row)
	if err != nil {
		return nil, takenOr(err)
	}
	if !found {
		return nil, &apierr.NotFoundError{Resource: resourceName, ID: id}
	}
	return toEntry(row)
}

func (s *Service) changed(ctx context.Context, id string, c Change) (*store.EventCatalogEntry, error) {
	row, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	current, err := toEntry(row)
	if err != nil {
		return nil, err
	}
	f := c.applyTo(current)
	if err := s.check(ctx, &f, id, false, c.Problems, c.recheck()); err != nil {
		return nil, err
	}
	return row, fill(row, f)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	entry, err := toEntry(row)
	if err != nil {
		return err
	}
	used, err := s.inUse(ctx, entry)
	if err != nil {
		return fmt.Errorf("check event catalog entry use: %w", err)
	}
	if used {
		return &apierr.ConflictError{Message: "The event catalog entry is in use and cannot be deleted."}
	}
	found, err := s.entries.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return &apierr.NotFoundError{Resource: resourceName, ID: id}
	}
	return nil
}

func (s *Service) load(ctx context.Context, id string) (*store.EventCatalogEntry, error) {
	row, err := s.entries.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, &apierr.NotFoundError{Resource: resourceName, ID: id}
	}
	return row, nil
}

type recheck struct {
	entrypoint bool
	topic      bool
}

var checkEverything = recheck{entrypoint: true, topic: true}

func (c Change) recheck() recheck {
	enabling := c.Enabled != nil && *c.Enabled
	return recheck{
		entrypoint: c.EntrypointID != nil || enabling,
		topic:      c.TopicPattern != nil || enabling,
	}
}

func (c Change) applyTo(e *Entry) Fields {
	f := Fields{
		Name: e.Name, Key: e.Key, Description: e.Description, EntrypointID: e.EntrypointID,
		TopicPattern: e.TopicPattern, PayloadSchema: e.PayloadSchema, ExamplePayload: e.ExamplePayload, Enabled: e.Enabled,
	}
	assign(&f.Name, c.Name)
	assign(&f.Description, c.Description)
	assign(&f.EntrypointID, c.EntrypointID)
	assign(&f.TopicPattern, c.TopicPattern)
	assign(&f.Enabled, c.Enabled)
	if c.SetPayloadSchema {
		f.PayloadSchema = c.PayloadSchema
	}
	if c.SetExamplePayload {
		f.ExamplePayload = c.ExamplePayload
	}
	return f
}

func assign[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func (s *Service) check(ctx context.Context, f *Fields, id string, newKey bool, problems map[string][]string, again recheck) error {
	details := map[string][]string{}
	for field, list := range problems {
		details[field] = append(details[field], list...)
	}
	add := func(field, problem string) { details[field] = append(details[field], problem) }

	f.Name = strings.TrimSpace(f.Name)
	f.Description = strings.TrimSpace(f.Description)
	checkText(add, FieldName, f.Name, MaxNameLength)
	checkText(add, FieldDescription, f.Description, MaxDescriptionLength)
	if newKey {
		checkKey(add, f.Key)
	}
	if again.topic {
		if err := broker.ValidateSubscription(f.TopicPattern); err != nil {
			add(FieldTopicPattern, err.Error())
		}
	}
	if again.entrypoint {
		if err := s.checkEntrypoint(ctx, add, f.EntrypointID); err != nil {
			return err
		}
	}
	checkDocuments(add, f.PayloadSchema, f.ExamplePayload)

	taken, err := s.taken(ctx, f, id, newKey, details)
	if err != nil {
		return err
	}
	if len(details) == 0 && len(taken) > 0 {
		return &TakenError{Details: taken}
	}
	for field, list := range taken {
		details[field] = append(details[field], list...)
	}
	if len(details) > 0 {
		return &apierr.ValidationError{Message: msgInvalid, Details: details}
	}
	return nil
}

func checkText(add func(field, problem string), field, value string, maxLen int) {
	switch {
	case value == "":
		add(field, problemEmpty)
	case utf8.RuneCountInString(value) > maxLen:
		add(field, fmt.Sprintf("must be at most %d characters", maxLen))
	}
}

func checkKey(add func(field, problem string), key string) {
	switch {
	case key == "":
		add(FieldKey, problemEmpty)
	case len(key) > MaxKeyLength:
		add(FieldKey, fmt.Sprintf("must be at most %d characters", MaxKeyLength))
	case !keyPattern.MatchString(key):
		add(FieldKey, problemBadKey)
	}
}

func (s *Service) checkEntrypoint(ctx context.Context, add func(field, problem string), id string) error {
	switch {
	case id == "":
		add(FieldEntrypointID, problemEmpty)
		return nil
	case !idgen.IsCanonical(id):
		add(FieldEntrypointID, problemNotUUID)
		return nil
	}
	ep, err := s.entrypoints.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("look up entrypoint: %w", err)
	}
	if ep == nil || ep.Type != eventMeshEntrypointType {
		add(FieldEntrypointID, problemNotEventMesh)
	}
	return nil
}

func checkDocuments(add func(field, problem string), schema, example map[string]any) {
	if schema != nil {
		if problem := schemaLimitProblem(schema); problem != "" {
			add(FieldPayloadSchema, problem)
		} else if _, err := structured.Compile(schema); err != nil {
			add(FieldPayloadSchema, problemBadSchema)
		}
	}
	if example == nil {
		return
	}
	if problem := exampleLimitProblem(example); problem != "" {
		add(FieldExamplePayload, problem)
	}
}

func bytesProblem(limit int) string {
	return fmt.Sprintf("must be at most %d bytes as JSON", limit)
}

func schemaLimitProblem(schema map[string]any) string {
	raw, err := json.Marshal(schema)
	switch {
	case err != nil || len(raw) > MaxDocumentBytes:
		return bytesProblem(MaxDocumentBytes)
	case nestsDeeperThan(raw, MaxSchemaNesting):
		return fmt.Sprintf("must nest at most %d levels deep", MaxSchemaNesting)
	}
	scan := scanSchema(schema)
	switch {
	case scan.refs:
		return problemRefs
	case scan.nodes > MaxSchemaNodes:
		return fmt.Sprintf("must have at most %d subschemas", MaxSchemaNodes)
	case scan.patterns > MaxSchemaPatterns:
		return fmt.Sprintf("must use at most %d pattern and patternProperties keywords in total", MaxSchemaPatterns)
	case scan.longestPattern > MaxSchemaPatternBytes:
		return fmt.Sprintf("must keep every pattern and patternProperties regular expression to at most %d bytes", MaxSchemaPatternBytes)
	case scan.hasPatternOverSteps():
		return fmt.Sprintf("must keep every pattern and patternProperties regular expression simple enough to compile to at most %d steps", MaxSchemaPatternSteps)
	}
	return ""
}

func exampleLimitProblem(example map[string]any) string {
	raw, err := json.Marshal(example)
	switch {
	case err != nil || len(raw) > MaxExampleBytes:
		return bytesProblem(MaxExampleBytes)
	case nestsDeeperThan(raw, MaxExampleNesting):
		return fmt.Sprintf("must nest at most %d levels deep", MaxExampleNesting)
	}
	return ""
}

var (
	referenceKeywords = []string{"$ref", "$dynamicRef", "$recursiveRef"}
	dataKeywords      = []string{"const", "enum", "default", "examples"}
	namedSchemas      = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"}
)

type schemaScan struct {
	refs           bool
	nodes          int
	patterns       int
	longestPattern int
	sources        []string
}

func scanSchema(schema map[string]any) schemaScan {
	var scan schemaScan
	scan.visit(schema)
	return scan
}

func (s *schemaScan) visit(node any) {
	switch v := node.(type) {
	case []any:
		for _, item := range v {
			if _, boolean := item.(bool); boolean {
				s.nodes++
			}
			s.visit(item)
		}
	case map[string]any:
		s.nodes++
		for keyword, value := range v {
			s.visitKeyword(keyword, value)
		}
	}
}

func (s *schemaScan) visitKeyword(keyword string, value any) {
	switch {
	case slices.Contains(referenceKeywords, keyword):
		s.refs = true
	case slices.Contains(dataKeywords, keyword):
	case keyword == "pattern":
		if source, ok := value.(string); ok {
			s.countPattern(source)
		}
	case slices.Contains(namedSchemas, keyword):
		s.visitNamed(keyword, value)
	default:
		s.visit(value)
	}
}

func (s *schemaScan) visitNamed(keyword string, value any) {
	named, ok := value.(map[string]any)
	if !ok {
		s.visit(value)
		return
	}
	if keyword == "patternProperties" {
		for source := range named {
			s.countPattern(source)
		}
	}
	for _, schema := range named {
		s.visit(schema)
	}
}

func nestsDeeperThan(raw []byte, limit int) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err != io.EOF
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
			if depth > limit {
				return true
			}
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
}

func (s *Service) taken(ctx context.Context, f *Fields, id string, newKey bool, details map[string][]string) (map[string][]string, error) {
	taken := map[string][]string{}
	if len(details[FieldName]) == 0 {
		held, err := s.entries.NameTaken(ctx, f.Name, id)
		if err != nil {
			return nil, err
		}
		if held {
			taken[FieldName] = []string{problemNameTaken}
		}
	}
	if newKey && len(details[FieldKey]) == 0 {
		held, err := s.entries.KeyTaken(ctx, f.Key)
		if err != nil {
			return nil, err
		}
		if held {
			taken[FieldKey] = []string{problemKeyTaken}
		}
	}
	return taken, nil
}

func takenOr(err error) error {
	switch {
	case errors.Is(err, store.ErrEventCatalogKeyTaken):
		return &TakenError{Details: map[string][]string{FieldKey: {problemKeyTaken}}}
	case errors.Is(err, store.ErrEventCatalogNameTaken):
		return &TakenError{Details: map[string][]string{FieldName: {problemNameTaken}}}
	default:
		return err
	}
}

func fill(row *store.EventCatalogEntry, f Fields) error {
	schema, err := marshalOptional(f.PayloadSchema)
	if err != nil {
		return err
	}
	example, err := marshalOptional(f.ExamplePayload)
	if err != nil {
		return err
	}
	row.Name, row.Description, row.EntrypointID, row.TopicPattern = f.Name, f.Description, f.EntrypointID, f.TopicPattern
	row.PayloadSchema, row.ExamplePayload, row.Enabled = schema, example, f.Enabled
	if row.Key == "" {
		row.Key = f.Key
	}
	return nil
}

func marshalOptional(doc map[string]any) (*string, error) {
	if doc == nil {
		return nil, nil
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}
	text := string(raw)
	return &text, nil
}

func unmarshalOptional(text *string) (map[string]any, error) {
	if text == nil {
		return nil, nil
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(*text), &doc); err != nil {
		return nil, fmt.Errorf("decode stored document: %w", err)
	}
	return doc, nil
}

func toEntry(row *store.EventCatalogEntry) (*Entry, error) {
	schema, err := unmarshalOptional(row.PayloadSchema)
	if err != nil {
		return nil, err
	}
	example, err := unmarshalOptional(row.ExamplePayload)
	if err != nil {
		return nil, err
	}
	return &Entry{
		ID: row.ID, Name: row.Name, Key: row.Key, Description: row.Description,
		EntrypointID: row.EntrypointID, TopicPattern: row.TopicPattern,
		PayloadSchema: schema, ExamplePayload: example, Enabled: row.Enabled,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedTime: row.CreatedTime, UpdatedTime: row.UpdatedTime,
	}, nil
}

func (s *schemaScan) countPattern(source string) {
	s.patterns++
	s.longestPattern = max(s.longestPattern, len(source))
	s.sources = append(s.sources, source)
}

func (s *schemaScan) hasPatternOverSteps() bool {
	return slices.ContainsFunc(s.sources, func(source string) bool {
		return compiledSteps(source) > MaxSchemaPatternSteps
	})
}

func compiledSteps(source string) int {
	parsed, err := syntax.Parse(source, syntax.Perl)
	if err != nil {
		return 0
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return 0
	}
	return len(prog.Inst)
}
