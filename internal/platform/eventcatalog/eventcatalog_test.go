// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type catalog struct {
	*Service
	db *store.DB
}

func newCatalog(t *testing.T) *catalog {
	t.Helper()
	db := store.TestDB(t)
	return &catalog{Service: NewService(db), db: db}
}

func (c *catalog) addEntrypoint(t *testing.T, entrypointType string) string {
	t.Helper()
	gw := &store.Gateway{Name: "mesh-" + idgen.New(), Type: entrypointType, CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewGatewayRepo(c.db).Create(t.Context(), gw); err != nil {
		t.Fatalf("create entrypoint: %v", err)
	}
	return gw.ID
}

var shipmentSchema = map[string]any{
	"type":     "object",
	"required": []any{"shipmentId"},
	"properties": map[string]any{
		"shipmentId":   map[string]any{"type": "string"},
		"delayMinutes": map[string]any{"type": "integer"},
	},
}

func shipmentDelayed(entrypointID string) Fields {
	return Fields{
		Name: "Shipment delayed", Key: "shipment_delayed",
		Description:  "A carrier reports that a shipment will arrive late.",
		EntrypointID: entrypointID, TopicPattern: "acme/logistics/shipment/*/delayed",
		PayloadSchema:  shipmentSchema,
		ExamplePayload: map[string]any{"shipmentId": "SHP-1001", "delayMinutes": 90},
		Enabled:        true,
	}
}

func validationDetails(t *testing.T, err error) map[string][]string {
	t.Helper()
	ve, ok := errors.AsType[*apierr.ValidationError](err)
	if !ok {
		t.Fatalf("err = %v, want a validation error", err)
	}
	return ve.Details
}

func TestCreateStoresTheEntry(t *testing.T) {
	c := newCatalog(t)
	mesh := c.addEntrypoint(t, eventMeshEntrypointType)

	created, err := c.Create(t.Context(), shipmentDelayed(mesh), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "shipment_delayed" || got.EntrypointID != mesh || !got.Enabled || got.CreatedBy != "alice@example.com" {
		t.Errorf("stored entry = %+v", got)
	}
	if got.PayloadSchema["type"] != "object" || got.ExamplePayload["shipmentId"] != "SHP-1001" {
		t.Errorf("documents did not round-trip: schema %v, example %v", got.PayloadSchema, got.ExamplePayload)
	}
}

func TestCreateCollectsEveryProblem(t *testing.T) {
	c := newCatalog(t)
	slack := c.addEntrypoint(t, "slack")

	_, err := c.Create(t.Context(), Fields{
		Name: "  ", Key: "Shipment-Delayed", Description: "",
		EntrypointID: slack, TopicPattern: "acme//shipments",
		PayloadSchema:  map[string]any{"type": 12},
		ExamplePayload: map[string]any{"shipmentId": "SHP-1001"},
	}, "alice@example.com")

	details := validationDetails(t, err)
	want := []string{FieldName, FieldKey, FieldDescription, FieldEntrypointID, FieldTopicPattern, FieldPayloadSchema}
	if got := slices.Sorted(maps.Keys(details)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("fields refused = %v, want %v", got, want)
	}
	if strings.Contains(strings.Join(details[FieldPayloadSchema], " "), "jsonschema") {
		t.Errorf("the validator's own text reached the refusal: %v", details[FieldPayloadSchema])
	}
}

func jsonSize(doc map[string]any) (int, error) {
	raw, err := json.Marshal(doc)
	return len(raw), err
}

const refusalDeadline = 5 * time.Second

func createWithinDeadline(t *testing.T, c *catalog, f Fields) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := c.Create(t.Context(), f, "alice@example.com")
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(refusalDeadline):
		t.Fatalf("Create did not return within %v", refusalDeadline)
		return nil
	}
}

func documentOfJSONSize(t *testing.T, base map[string]any, padKey string, size int) map[string]any {
	t.Helper()
	doc := maps.Clone(base)
	doc[padKey] = ""
	empty, err := jsonSize(doc)
	if err != nil {
		t.Fatalf("jsonSize of an empty %q document: %v", padKey, err)
	}
	doc[padKey] = strings.Repeat("x", size-empty)
	if got, _ := jsonSize(doc); got != size {
		t.Fatalf("built a %d byte document, want %d", got, size)
	}
	return doc
}

func withField(t *testing.T, f *Fields, field, value string, documentBytes int) {
	t.Helper()
	switch field {
	case FieldName:
		f.Name = value
	case FieldDescription:
		f.Description = value
	case FieldKey:
		f.Key = value
	case FieldEntrypointID:
		f.EntrypointID = value
	case FieldPayloadSchema:
		f.PayloadSchema = documentOfJSONSize(t, map[string]any{"type": "object"}, "description", documentBytes)
		f.ExamplePayload = nil
	case FieldExamplePayload:
		f.PayloadSchema = map[string]any{"type": "object"}
		f.ExamplePayload = documentOfJSONSize(t, map[string]any{}, "note", documentBytes)
	default:
		t.Fatalf("withField: unknown field %q", field)
	}
}

func TestCreateChecksEachFieldAtItsLimits(t *testing.T) {
	const (
		badKey             = "must be lowercase snake_case: a lowercase letter, then lowercase letters, digits and single underscores"
		sizeProblem        = "must be at most 65536 bytes as JSON"
		exampleSizeProblem = "must be at most 8192 bytes as JSON"
		charLimit          = "must be at most %d characters"
		empty              = "cannot be empty"
		accepted           = ""
	)
	cases := []struct {
		name          string
		field         string
		value         string
		documentBytes int
		wantProblem   string
	}{
		{"name of 255 characters", FieldName, strings.Repeat("n", 255), 0, accepted},
		{"name of 256 characters", FieldName, strings.Repeat("n", 256), 0, fmt.Sprintf(charLimit, 255)},
		{"name of 255 multi-byte characters", FieldName, strings.Repeat("\u00e9", 255), 0, accepted},
		{"name of 256 multi-byte characters", FieldName, strings.Repeat("\u00e9", 256), 0, fmt.Sprintf(charLimit, 255)},
		{"name of only spaces", FieldName, "   ", 0, empty},
		{"empty name", FieldName, "", 0, empty},
		{"description of 2000 characters", FieldDescription, strings.Repeat("d", 2000), 0, accepted},
		{"description of 2001 characters", FieldDescription, strings.Repeat("d", 2001), 0, fmt.Sprintf(charLimit, 2000)},
		{"description of 2000 multi-byte characters", FieldDescription, strings.Repeat("\u00e9", 2000), 0, accepted},
		{"description of 2001 multi-byte characters", FieldDescription, strings.Repeat("\u00e9", 2001), 0, fmt.Sprintf(charLimit, 2000)},
		{"description of only spaces", FieldDescription, "   ", 0, empty},
		{"description of 2000 characters padded with spaces", FieldDescription, "  " + strings.Repeat("d", 2000) + "  ", 0, accepted},
		{"key of 64 characters", FieldKey, strings.Repeat("k", 64), 0, accepted},
		{"key of 65 characters", FieldKey, strings.Repeat("k", 65), 0, fmt.Sprintf(charLimit, 64)},
		{"empty key", FieldKey, "", 0, empty},
		{"single letter key", FieldKey, "a", 0, accepted},
		{"key with digits and single underscores", FieldKey, "a1_b2", 0, accepted},
		{"key starting with an underscore", FieldKey, "_a", 0, badKey},
		{"key ending with an underscore", FieldKey, "a_", 0, badKey},
		{"key starting with a digit", FieldKey, "1a", 0, badKey},
		{"key with an uppercase letter", FieldKey, "aB", 0, badKey},
		{"key with a hyphen", FieldKey, "a-b", 0, badKey},
		{"key with a double underscore", FieldKey, "a__b", 0, badKey},
		{"empty entrypoint id", FieldEntrypointID, "", 0, empty},
		{"entrypoint id that is not a UUID", FieldEntrypointID, "not-a-uuid", 0, "must be a UUID"},
		{"entrypoint id of an upper-case UUID", FieldEntrypointID, strings.ToUpper(idgen.New()), 0, "must be a UUID"},
		{"entrypoint id nothing has", FieldEntrypointID, idgen.New(), 0, "must name an existing event_mesh entrypoint"},
		{"schema of the size limit", FieldPayloadSchema, "", MaxDocumentBytes, accepted},
		{"schema one byte over the size limit", FieldPayloadSchema, "", MaxDocumentBytes + 1, sizeProblem},
		{"example of the size limit", FieldExamplePayload, "", MaxExampleBytes, accepted},
		{"example one byte over the size limit", FieldExamplePayload, "", MaxExampleBytes + 1, exampleSizeProblem},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCatalog(t)
			f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
			withField(t, &f, tc.field, tc.value, tc.documentBytes)

			_, err := c.Create(t.Context(), f, "alice@example.com")

			if tc.wantProblem == accepted {
				if err != nil {
					t.Fatalf("Create with %s %q refused a valid entry: %v", tc.field, tc.name, err)
				}
				return
			}
			want := map[string][]string{tc.field: {tc.wantProblem}}
			if diff := cmp.Diff(want, validationDetails(t, err)); diff != "" {
				t.Errorf("Create with %s (%s) details mismatch (-want +got):\n%s", tc.field, tc.name, diff)
			}
		})
	}
}

func nestedSchema(key string, containers int) map[string]any {
	schema := map[string]any{}
	for range containers - 1 {
		schema = map[string]any{key: schema}
	}
	return schema
}

func containersJustUnderTheByteCap(t *testing.T, key string) int {
	t.Helper()
	perContainer := len(key) + 5
	containers := (MaxDocumentBytes-2)/perContainer + 1
	size, err := jsonSize(nestedSchema(key, containers))
	if err != nil || size > MaxDocumentBytes || MaxDocumentBytes-size >= perContainer {
		t.Fatalf("%d nested %q containers are %d bytes (error %v), want just under %d", containers, key, size, err, MaxDocumentBytes)
	}
	return containers
}

func TestCreateBoundsHowDeeplyASchemaNests(t *testing.T) {
	const (
		tooDeep  = "must nest at most 32 levels deep"
		accepted = ""
	)
	cases := []struct {
		name        string
		key         string
		containers  int
		wantProblem string
	}{
		{"not nested to the limit", "not", MaxSchemaNesting, accepted},
		{"not nested one level over the limit", "not", MaxSchemaNesting + 1, tooDeep},
		{"items nested to the limit", "items", MaxSchemaNesting, accepted},
		{"items nested one level over the limit", "items", MaxSchemaNesting + 1, tooDeep},
		{"not nested to just under the byte cap", "not", 0, tooDeep},
		{"items nested to just under the byte cap", "items", 0, tooDeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCatalog(t)
			f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
			containers := tc.containers
			if containers == 0 {
				containers = containersJustUnderTheByteCap(t, tc.key)
			}
			f.PayloadSchema = nestedSchema(tc.key, containers)
			f.ExamplePayload = nil

			err := createWithinDeadline(t, c, f)

			if tc.wantProblem == accepted {
				if err != nil {
					t.Fatalf("Create with %q nested %d containers deep refused a valid schema: %v", tc.key, containers, err)
				}
				return
			}
			want := map[string][]string{FieldPayloadSchema: {tc.wantProblem}}
			if diff := cmp.Diff(want, validationDetails(t, err)); diff != "" {
				t.Errorf("Create with %q nested %d containers deep details mismatch (-want +got):\n%s", tc.key, containers, diff)
			}
		})
	}
}

func doublingRefBomb(levels int) map[string]any {
	defs := map[string]any{"a0": map[string]any{"type": "object"}}
	for i := 1; i <= levels; i++ {
		previous := map[string]any{"$ref": fmt.Sprintf("#/$defs/a%d", i-1)}
		defs[fmt.Sprintf("a%d", i)] = map[string]any{"allOf": []any{previous, previous}}
	}
	return map[string]any{"$defs": defs, "$ref": fmt.Sprintf("#/$defs/a%d", levels)}
}

func alternatingSchema(wraps int, innermost map[string]any) map[string]any {
	schema := innermost
	for range wraps {
		schema = map[string]any{"allOf": []any{schema}}
	}
	return schema
}

func copiesOf(count int, schema map[string]any) []any {
	list := make([]any, count)
	for i := range list {
		list[i] = schema
	}
	return list
}

func siblingProperties(count int, schema map[string]any) map[string]any {
	properties := map[string]any{}
	for i := range count {
		properties[fmt.Sprintf("p%d", i)] = schema
	}
	return map[string]any{"properties": properties}
}

func patternsAcrossAllOf(count int) map[string]any {
	allOf := copiesOf(count, map[string]any{"pattern": "^x"})
	return map[string]any{"properties": map[string]any{"a": map[string]any{"allOf": allOf}}}
}

func patternPropertiesOf(count int) map[string]any {
	patterns := map[string]any{}
	for i := range count {
		patterns[fmt.Sprintf("^p%d", i)] = map[string]any{"type": "string"}
	}
	return map[string]any{"patternProperties": patterns}
}

func subschemas(count int) map[string]any {
	return map[string]any{"allOf": copiesOf(count-1, map[string]any{"type": "object"})}
}

func subschemasWithBooleans(count int) map[string]any {
	branches := make([]any, count-1)
	for i := range branches {
		branches[i] = true
	}
	return map[string]any{"anyOf": branches}
}

func namesOf(count int) []any {
	names := make([]any, count)
	for i := range names {
		names[i] = fmt.Sprintf("n%d", i)
	}
	return names
}

func trueValuesInData(count int) map[string]any {
	values := func() []any {
		list := make([]any, count)
		for i := range list {
			list[i] = true
		}
		return list
	}
	return map[string]any{"enum": values(), "const": values(), "examples": values()}
}

func regexpOfBytes(size int) string {
	return strings.Repeat("[x]?", size/4) + strings.Repeat("$", size%4)
}

func patternSchema(pattern string) map[string]any {
	return map[string]any{"properties": map[string]any{"a": map[string]any{"pattern": pattern}}}
}

func heavyPatternsOverItems(count int, pattern string) map[string]any {
	patterns := make([]any, count)
	for i := range patterns {
		patterns[i] = map[string]any{"pattern": fmt.Sprintf("%s%d", pattern, i)}
	}
	return map[string]any{"properties": map[string]any{"a": map[string]any{"items": map[string]any{"allOf": patterns}}}}
}

func longStrings(count, size int) map[string]any {
	items := make([]any, count)
	for i := range items {
		items[i] = strings.Repeat("x", size)
	}
	return map[string]any{"a": items}
}

func wideArraySchemas(count int) map[string]any {
	element := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	return map[string]any{"properties": map[string]any{"a": map[string]any{"allOf": copiesOf(count, element)}}}
}

func integers(count int) map[string]any {
	items := make([]any, count)
	for i := range items {
		items[i] = 1
	}
	return map[string]any{"a": items}
}

func enumSchema(members int) map[string]any {
	values := make([]any, members)
	for i := range values {
		values[i] = 1
	}
	return map[string]any{"properties": map[string]any{"a": map[string]any{"items": map[string]any{"enum": values}}}}
}

func distinctEnumSchema(members int) map[string]any {
	values := make([]any, members)
	for i := range values {
		values[i] = i + 1
	}
	return map[string]any{"properties": map[string]any{"a": map[string]any{"items": map[string]any{"enum": values}}}}
}

func zeros(count int) map[string]any {
	items := make([]any, count)
	for i := range items {
		items[i] = 0
	}
	return map[string]any{"a": items}
}

func failingBranchesPerItem(count int, branch map[string]any) map[string]any {
	return map[string]any{"properties": map[string]any{"a": map[string]any{"items": map[string]any{"allOf": copiesOf(count, branch)}}}}
}

func deepHeavySchema(levels int) map[string]any {
	schema := heavyPatternsOverItems(16, "(?:x|xx){0,50}y")["properties"].(map[string]any)["a"].(map[string]any)["items"].(map[string]any)
	for range levels {
		schema = map[string]any{"items": schema}
	}
	return map[string]any{"properties": map[string]any{"a": schema}}
}

func deepStringArrays(levels, size int) map[string]any {
	var value any = strings.Repeat("x", size)
	for range levels {
		value = []any{value}
	}
	return map[string]any{"a": value}
}

func TestCreateBoundsTheWorkOfCheckingTheDocuments(t *testing.T) {
	const (
		noRefs        = "must not use $ref, $dynamicRef or $recursiveRef; give a self-contained schema"
		tooDeep       = "must nest at most 32 levels deep"
		tooManyRegexp = "must use at most 16 pattern and patternProperties keywords in total"
		tooManyNodes  = "must have at most 256 subschemas"
		longPattern   = "must keep every pattern and patternProperties regular expression to at most 256 bytes"
		tooManySteps  = "must keep every pattern and patternProperties regular expression simple enough to compile to at most 256 steps"
		exampleBytes  = "must be at most 8192 bytes as JSON"
		accepted      = ""
		exactly256    = "x{0,127}"
		exactly257    = "x{0,127}$"
		draft07       = "http://json-schema.org/draft-07/schema#"
		draft201909   = "https://json-schema.org/draft/2019-09/schema"
		draft202012   = "https://json-schema.org/draft/2020-12/schema"
		badSchema     = "must be a valid, self-contained JSON Schema document"
	)
	anyObject := map[string]any{"type": "object"}
	names := map[string]any{"properties": map[string]any{"pattern": anyObject}}
	cases := []struct {
		name        string
		schema      map[string]any
		example     map[string]any
		wantField   string
		wantProblem string
	}{
		{"doubling $ref bomb of 30 levels", doublingRefBomb(30), map[string]any{}, FieldPayloadSchema, noRefs},
		{"$dynamicRef", map[string]any{"$dynamicRef": "#node", "$defs": map[string]any{"n": map[string]any{"$dynamicAnchor": "node"}}}, nil, FieldPayloadSchema, noRefs},
		{"$recursiveRef", map[string]any{"$recursiveRef": "#"}, nil, FieldPayloadSchema, noRefs},
		{"$ref below items", map[string]any{"type": "array", "items": map[string]any{"$ref": "#"}}, nil, FieldPayloadSchema, noRefs},
		{"$ref inside an allOf entry", map[string]any{"allOf": []any{map[string]any{"$ref": "#/$defs/x"}}, "$defs": map[string]any{"x": anyObject}}, nil, FieldPayloadSchema, noRefs},
		{"property literally named $ref", map[string]any{"type": "object", "properties": map[string]any{"$ref": map[string]any{"type": "string"}}}, map[string]any{"$ref": "x"}, "", accepted},
		{"$ref inside a const value", map[string]any{"const": map[string]any{"$ref": "x"}}, map[string]any{"$ref": "x"}, "", accepted},

		{"allOf of 40 subschemas", map[string]any{"allOf": copiesOf(40, anyObject)}, map[string]any{}, "", accepted},
		{"33 sibling properties each holding an object", siblingProperties(33, anyObject), map[string]any{}, "", accepted},
		{"33 sibling enum arrays", siblingProperties(33, map[string]any{"enum": []any{1}}), map[string]any{}, "", accepted},
		{"objects and arrays alternating to 32 levels", alternatingSchema(15, map[string]any{"enum": []any{1}}), nil, "", accepted},
		{"objects and arrays alternating to 33 levels", alternatingSchema(16, map[string]any{}), nil, FieldPayloadSchema, tooDeep},

		{"example nested to the limit", anyObject, nestedSchema("a", MaxExampleNesting), "", accepted},
		{"example nested one level over the limit", anyObject, nestedSchema("a", MaxExampleNesting+1), FieldExamplePayload, tooDeep},
		{"example nested as deep as 8192 bytes allow", anyObject, nestedSchema("a", 1300), FieldExamplePayload, tooDeep},
		{"example nested 8000 levels", anyObject, nestedSchema("a", 8000), FieldExamplePayload, exampleBytes},

		{"schema of 256 subschemas", subschemas(MaxSchemaNodes), map[string]any{}, "", accepted},
		{"schema of 257 subschemas", subschemas(MaxSchemaNodes + 1), map[string]any{}, FieldPayloadSchema, tooManyNodes},
		{"schema of 256 subschemas counting boolean subschemas in an array", subschemasWithBooleans(MaxSchemaNodes), map[string]any{}, "", accepted},
		{"schema of 257 subschemas counting boolean subschemas in an array", subschemasWithBooleans(MaxSchemaNodes + 1), map[string]any{}, FieldPayloadSchema, tooManyNodes},
		{"300 required names and a type array are not subschemas", map[string]any{"type": []any{"object", "null"}, "required": namesOf(300)}, map[string]any{}, "", accepted},
		{"300 true values inside enum, const and examples are not subschemas", trueValuesInData(300), map[string]any{}, "", accepted},

		{"pattern of 256 bytes", map[string]any{"properties": map[string]any{"a": map[string]any{"pattern": regexpOfBytes(MaxSchemaPatternBytes)}}}, map[string]any{"a": "x"}, "", accepted},
		{"pattern of 257 bytes", map[string]any{"properties": map[string]any{"a": map[string]any{"pattern": regexpOfBytes(MaxSchemaPatternBytes + 1)}}}, map[string]any{"a": "x"}, FieldPayloadSchema, longPattern},
		{"patternProperties entry of 256 bytes", map[string]any{"patternProperties": map[string]any{regexpOfBytes(MaxSchemaPatternBytes): anyObject}}, map[string]any{}, "", accepted},
		{"patternProperties entry of 257 bytes", map[string]any{"patternProperties": map[string]any{regexpOfBytes(MaxSchemaPatternBytes + 1): anyObject}}, map[string]any{}, FieldPayloadSchema, longPattern},
		{"pattern compiling to 256 steps", patternSchema(exactly256), map[string]any{"a": "x"}, "", accepted},
		{"pattern compiling to 257 steps", patternSchema(exactly257), map[string]any{"a": "x"}, FieldPayloadSchema, tooManySteps},
		{"patternProperties entry compiling to 257 steps", map[string]any{"patternProperties": map[string]any{exactly257: anyObject}}, map[string]any{}, FieldPayloadSchema, tooManySteps},
		{"pattern of 129 two-byte characters", map[string]any{"properties": map[string]any{"a": map[string]any{"pattern": strings.Repeat("é", MaxSchemaPatternBytes/2+1)}}}, map[string]any{"a": "x"}, FieldPayloadSchema, longPattern},

		{"16 heavy repeated alternations against an example at the byte cap", heavyPatternsOverItems(16, "(?:x|xx){0,50}y"), longStrings(8, 1000), "", accepted},
		{"16 heavy counted repeats against an example at the byte cap", heavyPatternsOverItems(16, "x{0,120}y"), longStrings(8, 1000), "", accepted},
		{"16 heavy unicode class repeats against an example at the byte cap", heavyPatternsOverItems(16, "\\p{L}{0,120}\\d"), longStrings(8, 1000), "", accepted},
		{"16 heavy nested counted groups against an example at the byte cap", heavyPatternsOverItems(16, "((x|y|z){5}){5}N"), longStrings(8, 1000), "", accepted},
		{"16 heavy patterns against one string at the byte cap", heavyPatternsOverItems(16, "(?:x|xx){0,50}y"), longStrings(1, 8000), "", accepted},
		{"widest allowed allOf against the largest allowed array", wideArraySchemas(120), integers(4000), "", accepted},
		{"16 heavy patterns at the bottom of the deepest allowed schema and example", deepHeavySchema(24), deepStringArrays(24, 7000), "", accepted},
		{"30,000 enum members against 3,900 array items", enumSchema(30000), integers(3900), "", accepted},
		{"enum of 8,700 distinct numbers against 3,900 zeros", distinctEnumSchema(8700), zeros(3900), "", accepted},
		{"250 failing minimum branches applied to 3,900 array items", failingBranchesPerItem(250, map[string]any{"minimum": 5}), zeros(3900), "", accepted},
		{"250 failing string branches applied to 3,900 array items", failingBranchesPerItem(250, map[string]any{"type": "string"}), zeros(3900), "", accepted},

		{"example that does not match the schema", shipmentSchema, map[string]any{"delayMinutes": "late"}, "", accepted},
		{"draft-07 $schema", map[string]any{"$schema": draft07, "type": "object"}, nil, "", accepted},
		{"2019-09 $schema", map[string]any{"$schema": draft201909, "type": "object"}, nil, "", accepted},
		{"2020-12 $schema with a trailing hash", map[string]any{"$schema": draft202012 + "#", "type": "object"}, nil, "", accepted},
		{"draft-07 $schema below properties and allOf", map[string]any{"properties": map[string]any{"a": map[string]any{"allOf": []any{map[string]any{"$schema": draft07}}}}}, nil, "", accepted},
		{"$schema that is not a string", map[string]any{"$schema": 7, "type": "object"}, nil, FieldPayloadSchema, badSchema},
		{"$schema of a dialect that does not exist", map[string]any{"$schema": "https://example.com/not-a-dialect", "type": "object"}, nil, FieldPayloadSchema, badSchema},
		{"2020-12 $schema", map[string]any{"$schema": draft202012, "type": "object"}, map[string]any{}, "", accepted},
		{"2020-12 $schema below allOf", map[string]any{"allOf": []any{map[string]any{"$schema": draft202012, "type": "object"}}}, map[string]any{}, "", accepted},
		{"no $schema", map[string]any{"type": "object"}, map[string]any{}, "", accepted},
		{"property literally named $schema", map[string]any{"type": "object", "properties": map[string]any{"$schema": map[string]any{"type": "string"}}}, map[string]any{"$schema": draft07}, "", accepted},
		{"draft-07 $schema inside a const value", map[string]any{"const": map[string]any{"$schema": draft07}}, map[string]any{"$schema": draft07}, "", accepted},

		{"16 pattern keywords", patternsAcrossAllOf(MaxSchemaPatterns), map[string]any{"a": "x"}, "", accepted},
		{"17 pattern keywords", patternsAcrossAllOf(MaxSchemaPatterns + 1), map[string]any{"a": "x"}, FieldPayloadSchema, tooManyRegexp},
		{"16 patternProperties entries", patternPropertiesOf(MaxSchemaPatterns), map[string]any{}, "", accepted},
		{"17 patternProperties entries", patternPropertiesOf(MaxSchemaPatterns + 1), map[string]any{}, FieldPayloadSchema, tooManyRegexp},
		{"8 pattern keywords and 8 patternProperties entries", map[string]any{"allOf": []any{patternsAcrossAllOf(8), patternPropertiesOf(8)}}, map[string]any{"a": "x"}, "", accepted},
		{"9 pattern keywords and 8 patternProperties entries", map[string]any{"allOf": []any{patternsAcrossAllOf(9), patternPropertiesOf(8)}}, map[string]any{"a": "x"}, FieldPayloadSchema, tooManyRegexp},
		{"16 pattern keywords beside a property named pattern", map[string]any{"allOf": []any{patternsAcrossAllOf(MaxSchemaPatterns), names}}, map[string]any{"a": "x"}, "", accepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCatalog(t)
			f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
			f.PayloadSchema, f.ExamplePayload = tc.schema, tc.example

			err := createWithinDeadline(t, c, f)

			if tc.wantProblem == accepted {
				if err != nil {
					t.Fatalf("Create refused a valid entry: %v", err)
				}
				return
			}
			want := map[string][]string{tc.wantField: {tc.wantProblem}}
			if diff := cmp.Diff(want, validationDetails(t, err)); diff != "" {
				t.Errorf("Create details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCreateStoresNameAndDescriptionTrimmed(t *testing.T) {
	c := newCatalog(t)
	f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
	f.Name = "  Shipment delayed \t"
	f.Description = "\n A shipment is late.  "

	created, err := c.Create(t.Context(), f, "alice@example.com")
	if err != nil {
		t.Fatalf("Create with a padded name and description: %v", err)
	}

	stored, err := c.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get %s: %v", created.ID, err)
	}
	if stored.Name != "Shipment delayed" || stored.Description != "A shipment is late." {
		t.Errorf("stored name %q and description %q, want both trimmed", stored.Name, stored.Description)
	}
}

func TestCreateStoresAnExampleTheSchemaRejects(t *testing.T) {
	c := newCatalog(t)
	f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
	f.ExamplePayload = map[string]any{"delayMinutes": "late"}

	created, err := c.Create(t.Context(), f, "alice@example.com")
	if err != nil {
		t.Fatalf("Create with an example the schema rejects: %v", err)
	}

	stored, err := c.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get %s after creating it with an example the schema rejects: %v", created.ID, err)
	}
	if diff := cmp.Diff(f.ExamplePayload, stored.ExamplePayload); diff != "" {
		t.Errorf("stored example mismatch (-want +got):\n%s", diff)
	}
}

func TestCreateRefusesOversizedDocuments(t *testing.T) {
	c := newCatalog(t)
	f := shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType))
	f.PayloadSchema = map[string]any{"type": "object", "description": strings.Repeat("x", MaxDocumentBytes)}
	f.ExamplePayload = map[string]any{"note": strings.Repeat("x", MaxDocumentBytes)}

	details := validationDetails(t, func() error { _, err := c.Create(t.Context(), f, "alice@example.com"); return err }())

	if len(details[FieldPayloadSchema]) == 0 || len(details[FieldExamplePayload]) == 0 {
		t.Errorf("details = %v, want both documents refused for size", details)
	}
}

func TestATakenNameOrKeyIsAConflictNamingTheFields(t *testing.T) {
	c := newCatalog(t)
	mesh := c.addEntrypoint(t, eventMeshEntrypointType)
	if _, err := c.Create(t.Context(), shipmentDelayed(mesh), "alice@example.com"); err != nil {
		t.Fatal(err)
	}

	again := shipmentDelayed(mesh)
	again.Name = "SHIPMENT DELAYED"
	_, err := c.Create(t.Context(), again, "alice@example.com")

	taken, ok := errors.AsType[*TakenError](err)
	if !ok {
		t.Fatalf("err = %v, want a TakenError", err)
	}
	if len(taken.Details[FieldName]) == 0 || len(taken.Details[FieldKey]) == 0 {
		t.Errorf("details = %v, want name and key", taken.Details)
	}
	if _, ok := errors.AsType[*apierr.ConflictError](err); !ok {
		t.Error("a TakenError must classify as a conflict")
	}

	again.TopicPattern = "acme/>/bad"
	details := validationDetails(t, func() error { _, err := c.Create(t.Context(), again, "alice@example.com"); return err }())
	if len(details[FieldTopicPattern]) == 0 || len(details[FieldName]) == 0 || len(details[FieldKey]) == 0 {
		t.Errorf("details = %v, want the taken fields listed beside the other problem", details)
	}
}

func TestUpdateChecksTheNewSchemaButNotTheStoredExampleAgainstIt(t *testing.T) {
	c := newCatalog(t)
	created, err := c.Create(t.Context(), shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType)), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := c.Update(t.Context(), created.ID, Change{
		SetPayloadSchema: true,
		PayloadSchema:    map[string]any{"type": "object", "required": []any{"orderId"}},
	}, "bob@example.com")
	if err != nil {
		t.Fatalf("Update to a schema the stored example does not match: %v", err)
	}
	if diff := cmp.Diff(created.ExamplePayload, updated.ExamplePayload); diff != "" {
		t.Errorf("stored example changed (-want +got):\n%s", diff)
	}

	draft07 := map[string]any{"$schema": "http://json-schema.org/draft-07/schema#", "type": "object"}
	updated, err = c.Update(t.Context(), created.ID, Change{SetPayloadSchema: true, PayloadSchema: draft07}, "bob@example.com")
	if err != nil {
		t.Fatalf("Update to a draft-07 schema: %v", err)
	}
	if diff := cmp.Diff(draft07, updated.PayloadSchema); diff != "" {
		t.Errorf("stored schema mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateClearsBothDocumentsAndKeepsTheKeyAndCreator(t *testing.T) {
	c := newCatalog(t)
	created, err := c.Create(t.Context(), shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType)), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	disabled := false
	updated, err := c.Update(t.Context(), created.ID, Change{
		Enabled: &disabled, SetPayloadSchema: true, SetExamplePayload: true,
	}, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.PayloadSchema != nil || updated.ExamplePayload != nil || updated.UpdatedBy != "bob@example.com" {
		t.Errorf("updated = %+v, want disabled with both documents cleared", updated)
	}
	if updated.Key != created.Key || updated.CreatedBy != "alice@example.com" {
		t.Errorf("updated = %+v, want the key and creator kept", updated)
	}
}

func TestUpdateReportsTheCallersProblemsWithoutWriting(t *testing.T) {
	c := newCatalog(t)
	created, err := c.Create(t.Context(), shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType)), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	renamed := "Shipment late"

	_, err = c.Update(t.Context(), created.ID, Change{
		Name: &renamed, Problems: map[string][]string{FieldKey: {"cannot be changed"}},
	}, "bob@example.com")

	if details := validationDetails(t, err); !slices.Equal(details[FieldKey], []string{"cannot be changed"}) {
		t.Errorf("details = %v, want the caller's key refusal", details)
	}
	if got, _ := c.Get(t.Context(), created.ID); got.Name != created.Name {
		t.Errorf("name = %q, want the refused update to write nothing", got.Name)
	}
}

func TestMissingEntriesAreNotFound(t *testing.T) {
	c := newCatalog(t)
	id := idgen.New()
	for name, err := range map[string]error{
		"get":    func() error { _, err := c.Get(t.Context(), id); return err }(),
		"update": func() error { _, err := c.Update(t.Context(), id, Change{}, "bob@example.com"); return err }(),
		"delete": c.Delete(t.Context(), id),
	} {
		if _, ok := errors.AsType[*apierr.NotFoundError](err); !ok {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
}

func TestNothingRefersToAnEntryYet(t *testing.T) {
	used, err := entryInUse(t.Context(), &Entry{ID: idgen.New(), Key: "shipment_delayed"})
	if err != nil || used {
		t.Errorf("entryInUse = %v, %v; want false until subscriptions can name an entry", used, err)
	}
}

func TestDeleteRefusesAnEntryInUse(t *testing.T) {
	c := newCatalog(t)
	created, err := c.Create(t.Context(), shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType)), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var asked *Entry
	c.inUse = func(_ context.Context, e *Entry) (bool, error) { asked = e; return true, nil }

	if _, ok := errors.AsType[*apierr.ConflictError](c.Delete(t.Context(), created.ID)); !ok {
		t.Error("deleting an entry in use must be a conflict")
	}
	if asked == nil || asked.ID != created.ID || asked.Key != created.Key {
		t.Errorf("the use check was asked about %+v, want the entry's id and key", asked)
	}
	if _, err := c.Get(t.Context(), created.ID); err != nil {
		t.Errorf("the refused delete removed the entry: %v", err)
	}

	c.inUse = entryInUse
	if err := c.Delete(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := errors.AsType[*apierr.NotFoundError](func() error { _, err := c.Get(t.Context(), created.ID); return err }()); !ok {
		t.Error("the entry is still there after delete")
	}
}

func TestDeleteStopsWhenTheUseCheckFails(t *testing.T) {
	c := newCatalog(t)
	created, err := c.Create(t.Context(), shipmentDelayed(c.addEntrypoint(t, eventMeshEntrypointType)), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	c.inUse = func(context.Context, *Entry) (bool, error) { return false, errors.New("lookup failed") }

	if err := c.Delete(t.Context(), created.ID); err == nil {
		t.Fatal("a failed use check must fail the delete")
	}
	if _, err := c.Get(t.Context(), created.ID); err != nil {
		t.Errorf("the failed delete removed the entry: %v", err)
	}
}

func orphanedEntry(t *testing.T, c *catalog) *Entry {
	t.Helper()
	mesh := c.addEntrypoint(t, eventMeshEntrypointType)
	created, err := c.Create(t.Context(), shipmentDelayed(mesh), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewGatewayRepo(c.db).Delete(t.Context(), mesh); err != nil {
		t.Fatalf("delete entrypoint: %v", err)
	}
	return created
}

func TestAnEntryWhoseEntrypointIsGoneCanBeDisabledRenamedAndDeleted(t *testing.T) {
	c := newCatalog(t)
	created := orphanedEntry(t, c)
	renamed, disabled := "Shipment late", false

	updated, err := c.Update(t.Context(), created.ID, Change{Name: &renamed, Enabled: &disabled}, "bob@example.com")
	if err != nil {
		t.Fatalf("disabling and renaming an orphaned entry: %v", err)
	}
	if updated.Enabled || updated.Name != renamed {
		t.Errorf("updated = %+v, want disabled and renamed", updated)
	}
	if err := c.Delete(t.Context(), created.ID); err != nil {
		t.Errorf("deleting an orphaned entry: %v", err)
	}
}

func TestAnOrphanedEntryIsStillCheckedWhenEnabledOrRepointed(t *testing.T) {
	c := newCatalog(t)
	created := orphanedEntry(t, c)
	disabled, enabled := false, true
	if _, err := c.Update(t.Context(), created.ID, Change{Enabled: &disabled}, "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	gone := idgen.New()

	_, enableErr := c.Update(t.Context(), created.ID, Change{Enabled: &enabled}, "bob@example.com")
	_, repointErr := c.Update(t.Context(), created.ID, Change{EntrypointID: &gone}, "bob@example.com")

	for name, err := range map[string]error{"enable": enableErr, "repoint": repointErr} {
		if details := validationDetails(t, err); !slices.Equal(details[FieldEntrypointID], []string{problemNotEventMesh}) {
			t.Errorf("%s: details = %v, want entrypointId refused", name, details)
		}
	}

	live := c.addEntrypoint(t, eventMeshEntrypointType)
	if _, err := c.Update(t.Context(), created.ID, Change{EntrypointID: &live, Enabled: &enabled}, "bob@example.com"); err != nil {
		t.Errorf("moving the entry to a live entrypoint and enabling it: %v", err)
	}
}

func TestAnOrphanedEntryTopicIsStillCheckedWhenItChanges(t *testing.T) {
	c := newCatalog(t)
	created := orphanedEntry(t, c)
	bad := "acme//shipments"

	_, err := c.Update(t.Context(), created.ID, Change{TopicPattern: &bad}, "bob@example.com")

	if details := validationDetails(t, err); len(details[FieldTopicPattern]) == 0 || len(details[FieldEntrypointID]) != 0 {
		t.Errorf("details = %v, want only topicPattern refused", details)
	}
}

func TestListFiltersAndPages(t *testing.T) {
	c := newCatalog(t)
	meshA := c.addEntrypoint(t, eventMeshEntrypointType)
	meshB := c.addEntrypoint(t, eventMeshEntrypointType)
	for _, e := range []struct {
		name, key, mesh string
		enabled         bool
	}{
		{"Order placed", "order_placed", meshA, true},
		{"invoice overdue", "invoice_overdue", meshA, false},
		{"Shipment delayed", "shipment_delayed", meshB, true},
	} {
		f := shipmentDelayed(e.mesh)
		f.Name, f.Key, f.Enabled = e.name, e.key, e.enabled
		if _, err := c.Create(t.Context(), f, "alice@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(opts ListOptions) ([]string, int) {
		t.Helper()
		entries, total, err := c.List(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = e.Key
		}
		return out, total
	}
	enabled := true

	if got, total := keys(ListOptions{PageQuery: store.PageQuery{Limit: 10}}); !slices.Equal(got, []string{"invoice_overdue", "order_placed", "shipment_delayed"}) || total != 3 {
		t.Errorf("by name = %v (%d), want name order ignoring case", got, total)
	}
	if got, total := keys(ListOptions{Enabled: &enabled, EntrypointID: meshA, PageQuery: store.PageQuery{Limit: 10}}); !slices.Equal(got, []string{"order_placed"}) || total != 1 {
		t.Errorf("enabled on mesh A = %v (%d)", got, total)
	}
	if got, total := keys(ListOptions{PageQuery: store.PageQuery{Sort: "key", Desc: true, Offset: 1, Limit: 1}}); !slices.Equal(got, []string{"order_placed"}) || total != 3 {
		t.Errorf("second page by key descending = %v (%d)", got, total)
	}
}
