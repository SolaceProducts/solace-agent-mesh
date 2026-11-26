// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

const (
	maxOpenAPIOperationNameLen = 60

	maxMCPToolNameLen = 64
)

var (
	databaseNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_$]+$`)
	hostnamePattern     = regexp.MustCompile(
		`^(` +
			`(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])` +
			`|` +
			`(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])` +
			`)$`,
	)
	oracleServiceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_$.]+$`)
	samPlaceholderPattern    = regexp.MustCompile(`^<<__SAM_.*__>>$`)
)

var (
	nonAlphanumericRe    = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	lowerCamelCaseRe     = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	upperCamelCaseAcrRe  = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	consecutiveUnderscRe = regexp.MustCompile(`_+`)
)

func ValidateConnectorValuesShape(values any) error {
	if _, isObject := values.(map[string]any); values != nil && !isObject {
		return &ValidationError{Message: fmt.Sprintf("values must be a JSON object, got %T", values)}
	}
	return nil
}

func validateConnectorValues(connType, subtype string, values any) error {
	return validateConnectorValuesWith(connType, subtype, values, validateMCPValues)
}

func validateMergedConnectorValues(connType, subtype string, values any) error {
	return validateConnectorValuesWith(connType, subtype, values, validateMergedMCPValues)
}

func validateUpdateRequestValues(connType string, values map[string]any) error {
	if connType == "mcp" {
		if err := validateMCPSSLConfig(values); err != nil {
			return err
		}
		return validateMCPManifestFilterExclusivity(values)
	}
	return nil
}

func validateConnectorValuesWith(connType, subtype string, values any, validateMCP func(map[string]any) error) error {
	if err := ValidateConnectorValuesShape(values); err != nil {
		return err
	}
	m, _ := values.(map[string]any)
	if m == nil {
		return nil
	}

	if err := validateFieldsAgainstSchema(connType, subtype, m); err != nil {
		return err
	}

	if connType != "mcp" {
		if err := validateConnectorHIL(connType, subtype, m); err != nil {
			return err
		}
	}

	switch connType {
	case "sql":
		return validateSQLValues(subtype, m)
	case "search":
		return validateSearchValues(subtype, m)
	case webSearchConnectorType:
		return validateWebSearchValues(m)
	case "api":
		return validateOpenAPIValues(m)
	case "mcp":
		return validateMCP(m)
	case "knowledge_base":
		return validateKnowledgeBaseValues(m)
	case "slack":
		return validateSlackConnectorValues(m)
	case "email":
		return validateEmailConnectorValues(m)
	case "event_mesh":
		return validateEventMeshConnectorValues(m)
	}
	return nil
}

var connectorTypeIndex = sync.OnceValue(loadConnectorTypeIndex)

func loadConnectorTypeIndex() map[string]map[string][]connectorFieldDef {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		return map[string]map[string][]connectorFieldDef{}
	}
	out := make(map[string]map[string][]connectorFieldDef, len(defs))
	for _, def := range defs {
		sub := make(map[string][]connectorFieldDef, len(def.Subtypes))
		for _, st := range def.Subtypes {
			sub[st.Subtype] = st.FieldSchema
		}
		out[def.Type] = sub
	}
	return out
}

var patternCache sync.Map

func compilePattern(p string) (*regexp.Regexp, error) {
	if cached, ok := patternCache.Load(p); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, err
	}
	patternCache.Store(p, re)
	return re, nil
}

func validateFieldsAgainstSchema(connType, subtype string, values map[string]any) error {
	idx := connectorTypeIndex()
	subs, known := idx[connType]
	if !known {
		return nil
	}
	fields, known := subs[subtype]
	if !known {
		return nil
	}

	for _, f := range fields {
		if !fieldVisible(f, values) {
			continue
		}
		raw, present := values[f.Name]
		if !present || raw == nil {
			continue
		}
		if err := validateFieldValue(f, raw); err != nil {
			return err
		}
	}
	return nil
}

func fieldVisible(f connectorFieldDef, values map[string]any) bool {
	for parent, want := range f.VisibleWhen {
		got, ok := values[parent].(string)
		if !ok || got != want {
			return false
		}
	}
	return true
}

func validateFieldValue(f connectorFieldDef, raw any) error {
	switch f.Type {
	case "string", "password":
		s, ok := raw.(string)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("field %q must be a string", f.Name)}
		}
		if s == "" {
			return nil
		}
		if isPlaceholder(s) {
			return nil
		}
		return validateStringRules(f, s)
	case "textarea":
		s, ok := raw.(string)
		if !ok {
			return nil
		}
		if s == "" || isPlaceholder(s) {
			return nil
		}
		return validateStringRules(f, s)
	case "select":
		s, ok := raw.(string)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("field %q must be a string", f.Name)}
		}
		if s == "" {
			return nil
		}
		if len(f.Options) == 0 {
			return nil
		}
		for _, opt := range f.Options {
			if opt.Value == s {
				return nil
			}
		}
		var allowed []string
		for _, opt := range f.Options {
			allowed = append(allowed, opt.Value)
		}
		return &ValidationError{Message: fmt.Sprintf("field %q value %q is not one of [%s]", f.Name, s, strings.Join(allowed, ", "))}
	case "number", "integer":
		n, ok := toFloat64(raw)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("field %q must be a number", f.Name)}
		}
		if f.Validation != nil {
			if f.Validation.Min != nil && n < float64(*f.Validation.Min) {
				return &ValidationError{Message: fmt.Sprintf("field %q must be >= %d", f.Name, *f.Validation.Min)}
			}
			if f.Validation.Max != nil && n > float64(*f.Validation.Max) {
				return &ValidationError{Message: fmt.Sprintf("field %q must be <= %d", f.Name, *f.Validation.Max)}
			}
		}
		return nil
	case "key_value":
		return validateKeyValueField(f.Name, raw)
	}
	return nil
}

func validateStringRules(f connectorFieldDef, s string) error {
	v := f.Validation
	if v == nil {
		return nil
	}
	if v.MinLength != nil && len(s) < *v.MinLength {
		msg := v.ErrorMsg
		if msg == "" {
			msg = fmt.Sprintf("field %q must be at least %d characters", f.Name, *v.MinLength)
		}
		return &ValidationError{Message: msg}
	}
	if v.MaxLength != nil && len(s) > *v.MaxLength {
		msg := v.ErrorMsg
		if msg == "" {
			msg = fmt.Sprintf("field %q must be at most %d characters", f.Name, *v.MaxLength)
		}
		return &ValidationError{Message: msg}
	}
	if v.Pattern != "" {
		re, err := compilePattern(v.Pattern)
		if err != nil {
			return nil
		}
		if !re.MatchString(s) {
			msg := v.ErrorMsg
			if msg == "" {
				msg = fmt.Sprintf("field %q does not match required pattern", f.Name)
			}
			return &ValidationError{Message: msg}
		}
	}
	return nil
}

func validateKeyValueField(name string, raw any) error {
	check := func(s string) error {
		if strings.ContainsAny(s, "\r\n") {
			return &ValidationError{Message: fmt.Sprintf("field %q contains CR/LF — header injection blocked", name)}
		}
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		for k, val := range v {
			if err := check(k); err != nil {
				return err
			}
			if err := check(fmt.Sprint(val)); err != nil {
				return err
			}
		}
	case []any:
		for _, entry := range v {
			pair, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"key", "name"} {
				if s, ok := pair[key].(string); ok {
					if err := check(s); err != nil {
						return err
					}
				}
			}
			if val, ok := pair["value"]; ok {
				if err := check(fmt.Sprint(val)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func toFloat64(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		if v == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

var declaredSubtypes = sync.OnceValue(loadDeclaredSubtypes)

func loadDeclaredSubtypes() map[string]map[string]struct{} {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		return map[string]map[string]struct{}{}
	}
	out := make(map[string]map[string]struct{}, len(defs))
	for _, def := range defs {
		set := make(map[string]struct{}, len(def.Subtypes))
		for _, st := range def.Subtypes {
			set[st.Subtype] = struct{}{}
		}
		out[def.Type] = set
	}
	return out
}

var connectorNameRuleType = sync.OnceValue(func() reflect.Type {
	field, _ := reflect.TypeFor[CreateConnectorRequest]().FieldByName("Name")
	return reflect.StructOf([]reflect.StructField{{Name: field.Name, Type: field.Type, Tag: field.Tag}})
})

func ValidateConnectorName(name string) error {
	holder := reflect.New(connectorNameRuleType())
	holder.Elem().Field(0).SetString(name)
	return validateRequest(holder.Interface())
}

func ValidateConnectorRename(current, requested string) error {
	if requested == "" || requested == current {
		return nil
	}
	return ValidateConnectorName(requested)
}

func ValidateConnectorCreate(req CreateConnectorRequest) error {
	if err := validateConnectorTypeSubtype(req.Type, req.Subtype); err != nil {
		return err
	}
	return validateConnectorValues(req.Type, req.Subtype, req.Values)
}

func validateConnectorTypeSubtype(connType, subtype string) error {
	if connType == "" {
		return &ValidationError{Message: "type is required"}
	}
	declared := declaredSubtypes()
	subs, known := declared[connType]
	if !known {
		return nil
	}
	if subtype == "" {
		return &ValidationError{Message: fmt.Sprintf("subtype is required for type %q (declared subtypes: %s)", connType, sortedKeys(subs))}
	}
	if _, ok := subs[subtype]; !ok {
		return &ValidationError{Message: fmt.Sprintf("unknown subtype %q for type %q (declared subtypes: %s)", subtype, connType, sortedKeys(subs))}
	}
	return nil
}

func sortedKeys(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

func validateSQLValues(subtype string, values map[string]any) error {
	if hostname, ok := values["hostname"].(string); ok && hostname != "" && !isPlaceholder(hostname) {
		if !hostnamePattern.MatchString(hostname) {
			return &ValidationError{Message: "Database Host must be a valid hostname or IPv4 address"}
		}
	}

	switch subtype {
	case "oracle":
		if sn, ok := values["service_name"].(string); ok && sn != "" && !isPlaceholder(sn) {
			if !oracleServiceNamePattern.MatchString(sn) {
				return &ValidationError{Message: "Service Name can only contain letters, numbers, underscores, dollar signs, and dots"}
			}
		}
	default:
		if db, ok := values["database"].(string); ok && db != "" && !isPlaceholder(db) {
			if !databaseNamePattern.MatchString(db) {
				return &ValidationError{Message: "Database Name can only contain letters, numbers, underscores, and dollar signs"}
			}
		}
	}
	return nil
}

func validateSearchValues(subtype string, values map[string]any) error {
	provided := func(key string) bool {
		v, _ := values[key].(string)
		return v != ""
	}
	switch subtype {
	case "elasticsearch":
		if !provided("hostname") && !provided("cloud_id") {
			return &ValidationError{Message: "Elasticsearch connector requires either a Host or a Cloud ID"}
		}
	case "opensearch":
		if !provided("hostname") {
			return &ValidationError{Message: "OpenSearch connector requires a Host"}
		}
	}
	return nil
}

func validateOpenAPIValues(values map[string]any) error {
	if err := validateOpenAPISpecSource(values); err != nil {
		return err
	}
	if err := validateOpenAPIAllowList(values); err != nil {
		return err
	}
	return validateOpenAPIPublicClientPKCE(values)
}

const publicClientWithoutPKCEMsg = "PKCE cannot be disabled when the token endpoint auth method is none: a client without a secret relies on PKCE to bind the authorization code to it"

func validateOpenAPIPublicClientPKCE(values map[string]any) error {
	authType, _ := values["auth_type"].(string)
	method, _ := values["auth_oauth_token_endpoint_auth_method"].(string)
	setting, _ := values[openAPIPKCEField].(string)
	if authType != "oauth" || method != publicClientMethod || setting != openAPIPKCEDisabled {
		return nil
	}
	return &ValidationError{Field: "values." + openAPIPKCEField, Message: publicClientWithoutPKCEMsg}
}

func validateOpenAPISpecSource(values map[string]any) error {
	specFile, _ := values["specification_file"].(string)
	if specFile == "" {
		return nil
	}
	if u, _ := values["specification_url"].(string); u != "" && !IsSpecDisplayPath(u) {
		return &ValidationError{Message: "specification_file and specification_url are mutually exclusive; provide exactly one spec source"}
	}
	if k, _ := values["file_s3_key"].(string); k != "" {
		return &ValidationError{Message: "specification_file and file_s3_key are mutually exclusive; provide exactly one spec source"}
	}
	return nil
}

func IsSpecDisplayPath(u string) bool {
	for _, prefix := range specDisplayPathPrefixes {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	return false
}

func IsSpecResourcePathFor(u, connectorID string) bool {
	for _, prefix := range specDisplayPathPrefixes {
		if u == prefix+connectorID+specResourceSuffix {
			return true
		}
	}
	return false
}

func validateOpenAPIAllowList(values map[string]any) error {
	allowList, ok := values["allow_list"].(string)
	if !ok || allowList == "" {
		return nil
	}

	operations := parseOperationList(allowList)
	if len(operations) == 0 {
		return &ValidationError{Message: "allow_list cannot be empty if provided"}
	}

	return validateOperationUniqueness(operations)
}

func validateAllowListAgainstSpec(operations []string, spec map[string]any) error {
	if err := validateCrossSpecNameCollisions(operations, spec); err != nil {
		return err
	}
	return validateSchemaPropertyUniqueness(operations, spec)
}

func parseOperationList(raw string) []string {
	var ops []string
	for op := range strings.SplitSeq(raw, ",") {
		op = strings.TrimSpace(op)
		if op != "" {
			ops = append(ops, op)
		}
	}
	return ops
}

func validateOperationUniqueness(operations []string) error {
	snakeMap := make(map[string][]string)
	for _, opID := range operations {
		snake := toSnakeCase(opID)
		snakeMap[snake] = append(snakeMap[snake], opID)
	}

	var conflicts []string
	for _, origIDs := range snakeMap {
		if len(origIDs) > 1 {
			conflicts = append(conflicts, fmt.Sprintf("'%s' and '%s'", origIDs[0], origIDs[1]))
		}
	}

	if len(conflicts) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf(
				"%d conflict(s) detected: these operation IDs truncate to the same snake_case tool name. Conflicting operations: %s. "+
					"Unselect one of the conflicting tools or modify operation IDs in your OpenAPI spec.",
				len(conflicts), conflicts[0],
			),
		}
	}
	return nil
}

func validateCrossSpecNameCollisions(operations []string, spec map[string]any) error {
	allowSet := make(map[string]struct{}, len(operations))
	for _, op := range operations {
		allowSet[op] = struct{}{}
	}

	fullSnakeMap := buildFullSnakeMap(spec)

	var conflicts []string
	for _, opID := range operations {
		snake := toSnakeCase(opID)
		siblings := fullSnakeMap[snake]
		for _, sib := range siblings {
			if _, allowed := allowSet[sib]; !allowed {
				conflicts = append(conflicts, fmt.Sprintf(
					"'%s' shares the tool name '%s' with '%s' (not in allow_list). "+
						"A caller cannot address them separately — both will be sent to the LLM.",
					opID, snake, sib,
				))
				break
			}
		}
	}

	if len(conflicts) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf(
				"%d operation(s) share their snake_case tool name with other operations in the spec due to %d-char truncation. "+
					"First conflict — %s "+
					"Add the conflicting operation(s) to the allow_list or unselect the affected operation.",
				len(conflicts), maxOpenAPIOperationNameLen, conflicts[0],
			),
		}
	}
	return nil
}

func validateSchemaPropertyUniqueness(operations []string, spec map[string]any) error {
	allowSet := make(map[string]struct{}, len(operations))
	for _, op := range operations {
		allowSet[op] = struct{}{}
	}

	httpMethods := map[string]struct{}{
		"get": {}, "post": {}, "put": {}, "patch": {},
		"delete": {}, "head": {}, "options": {}, "trace": {},
	}

	var conflicts []string
	paths, _ := spec["paths"].(map[string]any)
	for _, pathItem := range paths {
		pi, ok := pathItem.(map[string]any)
		if !ok {
			continue
		}
		for method, operation := range pi {
			if _, isHTTP := httpMethods[method]; !isHTTP {
				continue
			}
			op, ok := operation.(map[string]any)
			if !ok {
				continue
			}
			opID, _ := op["operationId"].(string)
			if _, allowed := allowSet[opID]; !allowed {
				continue
			}
			if c := checkOperationPropertyConflicts(opID, op, spec); c != "" {
				conflicts = append(conflicts, c)
			}
		}
	}

	if len(conflicts) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf(
				"%d operation(s) have property name conflicts due to snake_case truncation (%d chars). "+
					"First conflict — %s. "+
					"Rename the conflicting properties in your OpenAPI spec or unselect the operation.",
				len(conflicts), maxOpenAPIOperationNameLen, conflicts[0],
			),
		}
	}
	return nil
}

func checkOperationPropertyConflicts(opID string, operation, spec map[string]any) string {
	snakeMap := make(map[string][]string)

	if params, ok := operation["parameters"].([]any); ok {
		for _, p := range params {
			param, ok := p.(map[string]any)
			if !ok {
				continue
			}
			name, _ := param["name"].(string)
			if name != "" {
				snake := toSnakeCase(name)
				snakeMap[snake] = append(snakeMap[snake], name)
			}
		}
	}

	if reqBody, ok := operation["requestBody"].(map[string]any); ok {
		if content, ok := reqBody["content"].(map[string]any); ok {
			for _, mediaType := range content {
				mt, ok := mediaType.(map[string]any)
				if !ok {
					continue
				}
				if schema, ok := mt["schema"].(map[string]any); ok {
					props := resolveSchemaProperties(schema, spec)
					for propName := range props {
						snake := toSnakeCase(propName)
						snakeMap[snake] = append(snakeMap[snake], propName)
					}
				}
			}
		}
	}

	for snake, names := range snakeMap {
		if len(names) > 1 {
			return fmt.Sprintf("operation '%s': properties '%s' and '%s' both map to '%s'",
				opID, names[0], names[1], snake)
		}
	}
	return ""
}

func buildFullSnakeMap(spec map[string]any) map[string][]string {
	result := make(map[string][]string)
	httpMethods := map[string]struct{}{
		"get": {}, "post": {}, "put": {}, "patch": {},
		"delete": {}, "head": {}, "options": {}, "trace": {},
	}

	paths, _ := spec["paths"].(map[string]any)
	for _, pathItem := range paths {
		pi, ok := pathItem.(map[string]any)
		if !ok {
			continue
		}
		for method, operation := range pi {
			if _, isHTTP := httpMethods[method]; !isHTTP {
				continue
			}
			op, ok := operation.(map[string]any)
			if !ok {
				continue
			}
			if opID, _ := op["operationId"].(string); opID != "" {
				snake := toSnakeCase(opID)
				result[snake] = append(result[snake], opID)
			}
		}
	}
	return result
}

func resolveRef(ref string, spec map[string]any) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	parts := strings.Split(ref[2:], "/")
	var node any = spec
	for _, part := range parts {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[part]
	}
	if m, ok := node.(map[string]any); ok {
		return m
	}
	return nil
}

func resolveSchemaProperties(schema, spec map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		schema = resolveRef(ref, spec)
		if schema == nil {
			return nil
		}
	}
	props, _ := schema["properties"].(map[string]any)
	return props
}

func parseOpenAPISpec(body []byte) map[string]any {
	var result map[string]any
	if json.Unmarshal(body, &result) == nil {
		return result
	}
	if yaml.Unmarshal(body, &result) == nil {
		return result
	}
	return nil
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func validateEmailConnectorValues(values map[string]any) error {
	if host, ok := values["smtp_host"].(string); ok && host != "" && !isPlaceholder(host) {
		if !hostnamePattern.MatchString(host) {
			return &ValidationError{Message: "SMTP Server Host must be a valid hostname or IPv4 address"}
		}
	}

	if from, ok := values["envelope_from"].(string); ok && from != "" && !isPlaceholder(from) {
		if !emailPattern.MatchString(from) {
			return &ValidationError{Message: "Sender Email Address must be a valid email address"}
		}
	}

	if allowlist, ok := values["recipient_allowlist"].(string); ok {
		if allowlist == "" {
			return &ValidationError{Message: "Recipient Allowlist is required and must contain at least one domain"}
		}
		for domain := range strings.SplitSeq(allowlist, "\n") {
			domain = strings.TrimSpace(domain)
			if domain == "" {
				continue
			}
			if strings.Contains(domain, "@") ||
				strings.Contains(domain, " ") ||
				strings.HasPrefix(domain, ".") ||
				strings.HasSuffix(domain, ".") ||
				(domain != "localhost" && !strings.Contains(domain, ".")) {
				return &ValidationError{Message: fmt.Sprintf("Invalid domain in allowlist: %q. Enter domain names only (e.g., example.com), not email addresses", domain)}
			}
		}
	}

	return nil
}

var eventMeshToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var eventMeshTopicTemplatePattern = regexp.MustCompile(`\{\{(.*?)\}\}|\{\s*([^{}]+?)\s*\}`)

func eventMeshTopicPlaceholders(topic string) []string {
	var names []string
	for _, m := range eventMeshTopicTemplatePattern.FindAllStringSubmatch(topic, -1) {
		var name string
		if strings.HasPrefix(m[0], "{{") {
			name = strings.TrimSpace(m[1])
			if strings.Contains(name, "://") {
				name = strings.TrimSpace(name[strings.Index(name, "//")+2:])
			}
		} else {
			name = strings.TrimSpace(m[2])
		}
		names = append(names, name)
	}
	return names
}

var eventMeshValidParamTypes = map[string]struct{}{
	"string":  {},
	"integer": {},
	"number":  {},
	"boolean": {},
}

func validateEventMeshConnectorValues(values map[string]any) error {
	if err := validateEventMeshBrokerConnection(values); err != nil {
		return err
	}
	if _, present := values["operations"]; !present {
		return validateEventMeshLegacyFields(values)
	}
	operations, err := eventMeshOperations(values)
	if err != nil {
		return err
	}
	if len(operations) == 0 {
		return &ValidationError{Message: "At least one Event Mesh operation is required"}
	}
	seenNames := make(map[string]struct{}, len(operations))
	for index, operation := range operations {
		opValues := eventMeshOperationValues(operation)
		name, _ := opValues["tool_name"].(string)
		if name == "" {
			return &ValidationError{Message: fmt.Sprintf("Operation %d requires tool_name", index+1)}
		}
		if _, exists := seenNames[name]; exists {
			return &ValidationError{Message: fmt.Sprintf("Duplicate Event Mesh operation tool name '%s'", name)}
		}
		seenNames[name] = struct{}{}
		if err := validateEventMeshOperation(opValues, index); err != nil {
			return err
		}
	}
	return nil
}

func validateEventMeshLegacyFields(values map[string]any) error {
	if err := validateEventMeshToolName(values); err != nil {
		return err
	}
	topic, _ := values["topic"].(string)
	if topic != "" && !isPlaceholder(topic) {
		if err := validateEventMeshTopic(topic); err != nil {
			return err
		}
	}
	parsedParams, err := validateEventMeshParamsJSON(values)
	if err != nil {
		return err
	}
	if topic != "" && !isPlaceholder(topic) {
		if err := validateEventMeshTopicParamConsistency(topic, parsedParams); err != nil {
			return err
		}
	}
	if err := validateEventMeshPayloadFormat(values, parsedParams); err != nil {
		return err
	}
	return validateEventMeshRequestExpiry(values)
}

func validateEventMeshOperation(values map[string]any, index int) error {
	if err := validateEventMeshToolName(values); err != nil {
		return err
	}
	description, _ := values["tool_description"].(string)
	if description == "" {
		return &ValidationError{Message: fmt.Sprintf("Operation %d requires tool_description", index+1)}
	}
	topic, _ := values["topic"].(string)
	if topic == "" {
		return &ValidationError{Message: fmt.Sprintf("Operation %d requires topic", index+1)}
	}
	if !isPlaceholder(topic) {
		if err := validateEventMeshTopic(topic); err != nil {
			return err
		}
	}
	if _, exists := values["input_schema"]; !exists {
		return &ValidationError{Message: fmt.Sprintf("Operation %d requires input_schema", index+1)}
	}
	if _, err := eventMeshParametersFromInputSchema(values["input_schema"]); err != nil {
		return err
	}
	parsedParams, err := validateEventMeshParamsJSON(values)
	if err != nil {
		return err
	}
	if !isPlaceholder(topic) {
		if err := validateEventMeshTopicParamConsistency(topic, parsedParams); err != nil {
			return err
		}
	}
	if err := validateEventMeshPayloadFormat(values, parsedParams); err != nil {
		return err
	}
	return validateEventMeshRequestExpiry(values)
}

func validateEventMeshBrokerConnection(values map[string]any) error {
	var missing []string
	for _, field := range []string{"broker_url", "broker_vpn", "broker_username", "broker_password"} {
		v, _ := values[field].(string)
		if v == "" && !isPlaceholder(v) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("broker connection requires fields: %s", strings.Join(missing, ", ")),
		}
	}

	if url, ok := values["broker_url"].(string); ok && url != "" && !isPlaceholder(url) {
		if !strings.HasPrefix(url, "tcp://") && !strings.HasPrefix(url, "tcps://") {
			return &ValidationError{Message: "Broker URL must start with tcp:// or tcps://"}
		}
		if err := validateEventMeshBrokerURL(url); err != nil {
			return err
		}
	}
	return nil
}

func validateEventMeshToolName(values map[string]any) error {
	name, ok := values["tool_name"].(string)
	if !ok || name == "" || isPlaceholder(name) {
		return nil
	}
	if !eventMeshToolNamePattern.MatchString(name) {
		return &ValidationError{Message: "Tool name must start with lowercase letter and contain only lowercase letters, numbers, and underscores"}
	}
	if len(name) > maxMCPToolNameLen {
		return &ValidationError{Message: fmt.Sprintf("Tool name must not exceed %d characters", maxMCPToolNameLen)}
	}
	return nil
}

func validateEventMeshParamsJSON(values map[string]any) ([]map[string]any, error) {
	params, ok := values["parameters"].(string)
	if !ok || params == "" || isPlaceholder(params) {
		return nil, nil
	}
	return validateEventMeshParameters(params)
}

func validateEventMeshPayloadFormat(values map[string]any, parsedParams []map[string]any) error {
	payloadFormat, _ := values["payload_format"].(string)
	if payloadFormat != "text" {
		return nil
	}
	if raw, _ := values["parameters"].(string); isPlaceholder(raw) {
		return nil
	}
	payloadPathCount := 0
	for _, p := range parsedParams {
		if path, ok := p["payload_path"].(string); ok && path != "" {
			payloadPathCount++
		}
	}
	if payloadPathCount != 1 {
		return &ValidationError{Message: "payload_format=text requires exactly one parameter with a payload_path"}
	}
	return nil
}

func validateEventMeshRequestExpiry(values map[string]any) error {
	expiry, ok := values["request_expiry_ms"]
	if !ok || expiry == nil {
		return nil
	}
	n, ok := toFloat64(expiry)
	if !ok {
		return nil
	}
	if n < 1000 {
		return &ValidationError{Message: "Request timeout must be at least 1000ms (1 second)"}
	}
	if n > 300000 {
		return &ValidationError{Message: "Request timeout must not exceed 300000ms (5 minutes)"}
	}
	return nil
}

func validateEventMeshBrokerURL(url string) error {
	url = strings.TrimPrefix(url, "tcps://")
	url = strings.TrimPrefix(url, "tcp://")
	if url == "" {
		return &ValidationError{Message: "Broker URL must include a host after the scheme"}
	}

	parts := strings.SplitN(url, "/", 2)
	hostPort := parts[0]
	if hostPort == "" {
		return &ValidationError{Message: "Broker URL must include a host"}
	}

	colonIdx := strings.LastIndex(hostPort, ":")
	if colonIdx == -1 {
		if !hostnamePattern.MatchString(hostPort) {
			return &ValidationError{Message: "Broker URL host must be a valid hostname or IPv4 address"}
		}
	} else {
		host := hostPort[:colonIdx]
		portStr := hostPort[colonIdx+1:]

		if host == "" {
			return &ValidationError{Message: "Broker URL must include a host before the port"}
		}
		if !hostnamePattern.MatchString(host) {
			return &ValidationError{Message: "Broker URL host must be a valid hostname or IPv4 address"}
		}

		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return &ValidationError{Message: "Broker URL port must be a valid port number (1-65535)"}
		}
	}

	return nil
}

func validateEventMeshTopic(topic string) error {
	skeleton := eventMeshTopicTemplatePattern.ReplaceAllString(topic, "_")
	if strings.Contains(skeleton, "//") {
		return &ValidationError{Message: "Topic address contains empty levels (consecutive slashes)"}
	}
	if strings.HasPrefix(skeleton, "/") {
		return &ValidationError{Message: "Topic address should not start with a slash"}
	}
	if strings.Count(topic, "{") != strings.Count(topic, "}") {
		return &ValidationError{Message: "Topic address has unbalanced { } dynamic levels"}
	}
	emptyPlaceholderPattern := regexp.MustCompile(`\{\s*\}`)
	if emptyPlaceholderPattern.MatchString(topic) {
		return &ValidationError{Message: "Topic address contains an empty placeholder"}
	}

	for _, paramName := range eventMeshTopicPlaceholders(topic) {
		if !isValidIdentifier(paramName) {
			return &ValidationError{Message: fmt.Sprintf("Topic address dynamic level '%s' is not a valid identifier", paramName)}
		}
	}

	return nil
}

func validateEventMeshParameters(paramsJSON string) ([]map[string]any, error) {
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &parsed); err != nil {
		return nil, &ValidationError{Message: "Parameters must be a valid JSON array"}
	}

	seenNames := make(map[string]struct{})
	for i, p := range parsed {
		name, ok := p["name"].(string)
		if !ok || name == "" {
			return nil, &ValidationError{Message: fmt.Sprintf("Parameter %d missing required 'name' field", i)}
		}
		if !isValidIdentifier(name) {
			return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' name must be a valid identifier (alphanumeric and underscores, not starting with a digit)", name)}
		}
		if _, exists := seenNames[name]; exists {
			return nil, &ValidationError{Message: fmt.Sprintf("Duplicate parameter name '%s'", name)}
		}
		seenNames[name] = struct{}{}

		if pType, ok := p["type"].(string); ok && pType != "" {
			if _, valid := eventMeshValidParamTypes[pType]; !valid {
				return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' has invalid type '%s'. Valid types: string, integer, number, boolean", name, pType)}
			}
		}

		if payloadPath, ok := p["payload_path"].(string); ok && payloadPath != "" {
			if strings.HasPrefix(payloadPath, ".") || strings.HasSuffix(payloadPath, ".") {
				return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' payload_path should not start or end with a dot", name)}
			}
			if strings.Contains(payloadPath, "..") {
				return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' payload_path contains consecutive dots", name)}
			}
		}

		if rawEnum, exists := p["enum"]; exists {
			enum, ok := rawEnum.([]any)
			if !ok {
				return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' enum must be an array", name)}
			}
			if len(enum) == 0 {
				return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' enum array must not be empty", name)}
			}
			paramType, _ := p["type"].(string)
			if paramType == "" {
				paramType = "string"
			}
			for _, value := range enum {
				if !eventMeshEnumValueMatchesType(value, paramType) {
					return nil, &ValidationError{Message: fmt.Sprintf("Parameter '%s' enum values must match type '%s'", name, paramType)}
				}
			}
		}
	}

	return parsed, nil
}

func eventMeshEnumValueMatchesType(value any, paramType string) bool {
	switch paramType {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "number":
		_, ok := value.(float64)
		return ok
	default:
		return false
	}
}

func validateEventMeshTopicParamConsistency(topic string, params []map[string]any) error {
	definedParams := make(map[string]struct{})
	for _, p := range params {
		if name, ok := p["name"].(string); ok {
			definedParams[name] = struct{}{}
		}
	}

	var undefinedParams []string
	for _, paramName := range eventMeshTopicPlaceholders(topic) {
		if _, defined := definedParams[paramName]; !defined {
			undefinedParams = append(undefinedParams, paramName)
		}
	}

	if len(undefinedParams) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("Topic address uses undefined parameters: %s. Add them to the input schema.", strings.Join(undefinedParams, ", ")),
		}
	}

	return nil
}

func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' {
				return false
			}
		} else {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
				return false
			}
		}
	}
	return true
}

func validateKnowledgeBaseValues(values map[string]any) error {
	var missing []string
	for _, field := range []string{"kb_id", "region", "auth_type"} {
		v, _ := values[field].(string)
		if v == "" && !isPlaceholder(v) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("knowledge base connector requires fields: %s", strings.Join(missing, ", ")),
		}
	}
	return nil
}

func validateSlackConnectorValues(values map[string]any) error {
	token, _ := values["slack_bot_token"].(string)
	if token == "" || isPlaceholder(token) {
		return nil
	}
	if !strings.HasPrefix(token, "xoxb-") {
		return &ValidationError{Message: "Bot Token must start with 'xoxb-'"}
	}
	return nil
}

func validateMCPValues(values map[string]any) error {
	if err := validateMCPSelectedToolsShape(values); err != nil {
		return err
	}
	if err := validateMCPSSLConfig(values); err != nil {
		return err
	}
	if err := validateMCPToolFilterExclusivity(values); err != nil {
		return err
	}
	return validateMCPManifestContent(values)
}

func validateMergedMCPValues(values map[string]any) error {
	if err := validateMCPSelectedToolsShape(values); err != nil {
		return err
	}
	if err := validateMCPRuntimeFilterExclusivity(values); err != nil {
		return err
	}
	return validateMCPManifestContent(values)
}

func validateMCPSelectedToolsShape(values map[string]any) error {
	if raw, ok := values["selected_tools"].(string); ok && raw == "" {
		return &ValidationError{Message: "selected_tools cannot be an empty string; omit the field entirely to select all tools"}
	}
	return nil
}

func validateMCPManifestContent(values map[string]any) error {
	manifest := parseMCPManifest(values)
	selectedTools := parseMCPSelectedTools(values)

	if err := validateMCPToolSelection(selectedTools, manifest); err != nil {
		return err
	}
	if err := validateMCPToolNameLength(manifest, selectedTools); err != nil {
		return err
	}
	if err := validateMCPManifestAnnotations(manifest); err != nil {
		return err
	}
	return validateConnectorHILShape(values["hil"])
}

func validateMCPManifestAnnotations(manifest []map[string]any) error {
	for _, entry := range manifest {
		if err := mcp.ValidateManifestAnnotations(entry); err != nil {
			name, _ := entry["name"].(string)
			return &ValidationError{Message: fmt.Sprintf("manifest tool %q: %v", name, err)}
		}
	}
	return nil
}

func validateConnectorHILShape(raw any) error {
	if raw == nil {
		return nil
	}
	parsed, err := parseConnectorHIL(raw)
	if err != nil {
		return &ValidationError{Message: "hil: must be a map, or a YAML or JSON string that parses to one"}
	}
	if parsed == nil {
		return nil
	}
	if err := validateHILEntry(parsed, "hil"); err != nil {
		return err
	}
	toolsRaw, ok := parsed["tools"]
	if !ok {
		return nil
	}
	toolsMap, ok := toolsRaw.(map[string]any)
	if !ok {
		return &ValidationError{Message: "hil.tools must be a map of tool-name to HIL config"}
	}
	for name, entryRaw := range toolsMap {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("hil.tools.%s must be a map", name)}
		}
		if err := validateHILEntry(entry, "hil.tools."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateHILEntry(entry map[string]any, path string) error {
	for k, v := range entry {
		switch k {
		case "tools":
			if path != "hil" {
				return &ValidationError{Message: fmt.Sprintf("%s.tools is only valid at the top level", path)}
			}
		case "require_approval", "show_args":
			if _, ok := v.(bool); !ok {
				return &ValidationError{Message: fmt.Sprintf("%s.%s must be a bool", path, k)}
			}
		case "require_approval_when":
			if err := validateApprovalWhenRules(v, path); err != nil {
				return err
			}
		case "approvers":
			if _, err := tool.ParseHILApprovers(v, strings.TrimPrefix(path, "hil.tools.")); err != nil {
				return &ValidationError{Message: err.Error()}
			}
		case "approval_message":
			if _, ok := v.(string); !ok {
				return &ValidationError{Message: fmt.Sprintf("%s.%s must be a string", path, k)}
			}
		case "timeout":
			if !isDuration(v) {
				return &ValidationError{Message: fmt.Sprintf("%s.timeout must be a duration string (e.g. \"30m\")", path)}
			}
		default:
			return &ValidationError{Message: fmt.Sprintf("%s.%s: unknown HIL field", path, k)}
		}
	}
	return nil
}

func isDuration(v any) bool {
	s, ok := v.(string)
	if !ok || s == "" {
		return false
	}
	_, err := time.ParseDuration(s)
	return err == nil
}

var hilRuleOperators = map[string]struct{}{
	"eq": {}, "in": {}, "not_in": {}, "exists": {}, "not_exists": {},
	"gt": {}, "lt": {}, "gte": {}, "lte": {},
}

func validateApprovalWhenRules(v any, path string) error {
	rules, ok := v.([]any)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when must be a list", path)}
	}
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] must be a map", path, i)}
		}
		if arg, _ := rule["arg"].(string); arg == "" {
			return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] missing required field 'arg' (non-empty string)", path, i)}
		}
		var op string
		ops := 0
		for k := range rule {
			if k == "arg" {
				continue
			}
			if _, ok := hilRuleOperators[k]; !ok {
				return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] unknown operator %q (valid: eq, in, not_in, exists, not_exists, gt, lt, gte, lte)", path, i, k)}
			}
			op = k
			ops++
		}
		switch {
		case ops == 0:
			return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] missing operator (one of: eq, in, not_in, exists, not_exists, gt, lt, gte, lte)", path, i)}
		case ops > 1:
			return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] specifies multiple operators — only one operator per rule is supported", path, i)}
		}
		if msg := approvalOperandError(op, rule[op]); msg != "" {
			return &ValidationError{Message: fmt.Sprintf("%s.require_approval_when[%d] operator %q %s", path, i, op, msg)}
		}
	}
	return nil
}

func approvalOperandError(op string, val any) string {
	switch op {
	case "exists", "not_exists":
		return ""
	case "in", "not_in":
		list, ok := val.([]any)
		if !ok {
			return "value must be a list"
		}
		if len(list) == 0 {
			return "list must be non-empty"
		}
		for j, item := range list {
			if !isHILScalar(item) {
				return fmt.Sprintf("list element [%d] must be a string, number, or bool", j)
			}
		}
		return ""
	case "gt", "lt", "gte", "lte":
		if !isHILNumeric(val) {
			return "value must be a number"
		}
		return ""
	case "eq":
		if !isHILScalar(val) {
			return "value must be a string, number, or bool"
		}
		return ""
	}
	return ""
}

func isHILScalar(v any) bool {
	switch v.(type) {
	case string, bool,
		float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func isHILNumeric(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func parseConnectorHIL(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		if len(v) == 0 {
			return nil, nil
		}
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
		if len(parsed) == 0 {
			return nil, nil
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("must be a map or YAML/JSON string, got %T", raw)
	}
}

func validateMCPToolFilterExclusivity(values map[string]any) error {
	if err := validateMCPRuntimeFilterExclusivity(values); err != nil {
		return err
	}
	return validateMCPManifestFilterExclusivity(values)
}

func validateMCPRuntimeFilterExclusivity(values map[string]any) error {
	set := mcpRuntimeFiltersSet(values)
	if len(set) > 1 {
		return &ValidationError{
			Message: fmt.Sprintf("tool_name, allow_list, and deny_list are mutually exclusive — got %s", strings.Join(set, " + ")),
		}
	}
	return nil
}

func validateMCPManifestFilterExclusivity(values map[string]any) error {
	set := mcpRuntimeFiltersSet(values)
	if len(set) > 0 && len(parseMCPManifest(values)) > 0 {
		return &ValidationError{
			Message: fmt.Sprintf("manifest is mutually exclusive with %s — the manifest already declares the tool set", set[0]),
		}
	}
	return nil
}

func mcpRuntimeFiltersSet(values map[string]any) []string {
	var set []string
	if strings.TrimSpace(stringValue(values["tool_name"])) != "" {
		set = append(set, "tool_name")
	}
	if len(splitMCPNameList(values["allow_list"])) > 0 {
		set = append(set, "allow_list")
	}
	if len(splitMCPNameList(values["deny_list"])) > 0 {
		set = append(set, "deny_list")
	}
	return set
}

func stringValue(raw any) string {
	if s, ok := raw.(string); ok {
		return s
	}
	return ""
}

func parseMCPManifest(values map[string]any) []map[string]any {
	raw, ok := values["manifest"]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		var manifest []map[string]any
		for _, entry := range v {
			if m, ok := entry.(map[string]any); ok {
				manifest = append(manifest, m)
			}
		}
		return manifest
	case string:
		if v == "" {
			return nil
		}
		var parsed []map[string]any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			return parsed
		}
		return nil
	default:
		return nil
	}
}

func parseMCPSelectedTools(values map[string]any) []string {
	raw, ok := values["selected_tools"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		var result []string
		for t := range strings.SplitSeq(v, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				result = append(result, t)
			}
		}
		return result
	case []any:
		var result []string
		for _, entry := range v {
			if s, ok := entry.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func validateMCPToolSelection(selectedTools []string, manifest []map[string]any) error {
	if len(selectedTools) == 0 {
		return nil
	}
	if len(manifest) == 0 {
		return &ValidationError{
			Message: "selected_tools cannot be used without a manifest. " +
				"Either provide a manifest with tool definitions, or omit selected_tools for runtime discovery.",
		}
	}

	toolMap := make(map[string]struct{}, len(manifest))
	for _, t := range manifest {
		if name, _ := t["name"].(string); name != "" {
			toolMap[name] = struct{}{}
		}
	}

	var invalid []string
	for _, name := range selectedTools {
		if _, ok := toolMap[name]; !ok {
			invalid = append(invalid, name)
		}
	}

	if len(invalid) > 0 {
		var available []string
		for _, t := range manifest {
			if name, _ := t["name"].(string); name != "" {
				available = append(available, name)
			}
		}
		return &ValidationError{
			Message: fmt.Sprintf(
				"Selected tools not found in manifest: %s. Available tools: %s",
				strings.Join(invalid, ", "), strings.Join(available, ", "),
			),
		}
	}
	return nil
}

func validateMCPToolNameLength(manifest []map[string]any, selectedTools []string) error {
	if len(manifest) == 0 {
		return nil
	}

	toolsToValidate := manifest
	if len(selectedTools) > 0 {
		toolMap := make(map[string]map[string]any, len(manifest))
		for _, t := range manifest {
			if name, _ := t["name"].(string); name != "" {
				toolMap[name] = t
			}
		}
		toolsToValidate = nil
		for _, name := range selectedTools {
			if t, ok := toolMap[name]; ok {
				toolsToValidate = append(toolsToValidate, t)
			}
		}
	}

	var longTools []string
	for _, tool := range toolsToValidate {
		name, _ := tool["name"].(string)
		if name != "" && len(name) > maxMCPToolNameLen {
			longTools = append(longTools, name)
		}
	}

	if len(longTools) > 0 {
		remedy := "Remove the offending entries from the manifest."
		if len(selectedTools) > 0 {
			remedy = "Unselect the offending tool(s)."
		}
		return &ValidationError{
			Message: fmt.Sprintf(
				"%d tool(s) detected with names exceeding the %d character limit. "+
					"Tool '%s' has %d characters. %s",
				len(longTools), maxMCPToolNameLen, longTools[0], len(longTools[0]), remedy,
			),
		}
	}
	return nil
}

func toSnakeCase(text string) string {
	text = nonAlphanumericRe.ReplaceAllString(text, "_")
	text = lowerCamelCaseRe.ReplaceAllString(text, "${1}_${2}")
	text = upperCamelCaseAcrRe.ReplaceAllString(text, "${1}_${2}")
	text = strings.ToLower(text)
	text = consecutiveUnderscRe.ReplaceAllString(text, "_")
	text = strings.Trim(text, "_")
	if len(text) > maxOpenAPIOperationNameLen {
		text = text[:maxOpenAPIOperationNameLen]
	}
	return text
}

func isPlaceholder(v string) bool {
	return samPlaceholderPattern.MatchString(v)
}
