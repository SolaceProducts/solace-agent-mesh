// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const RedactedMarker = "<REDACTED>"

const unserializableMarker = "<unserializable value>"

const maxRedactedValueBytes = 16384

const inheritedCredentialLeafMinBytes = 8

type credentialScope int

const (
	notCredential credentialScope = iota
	namedCredential
	inheritedCredential
)

const credentialNameAlternation = `secret|password|passwd|passphrase|pwd|token|credential` +
	`|authorization|auth|cookie|signature|sig|hmac|jwt` +
	`|api[_-]?key|access[_-]?key|private[_-]?key|signing[_-]?key|master[_-]?key` +
	`|encryption[_-]?key|account[_-]?key|license[_-]?key|deploy[_-]?key` +
	`|subscription[_-]?key|connection[_-]?string` +
	`|passcode|dsn|app[_-]?key|consumer[_-]?key|client[_-]?key` +
	`|session[_-]?key|shared[_-]?key`

const credentialNamePattern = `[a-z0-9_.-]*(?:` + credentialNameAlternation + `)[0-9]*(?:[_.-][a-z0-9_.-]*)?`

var (
	doubleQuotedCredentialAssignment = regexp.MustCompile(
		`(?i)("?\b` + credentialNamePattern + `"?[^\S\n]*[:=][^\S\n]*")([^"\n]{8,})(")`)
	singleQuotedCredentialAssignment = regexp.MustCompile(
		`(?i)('?\b` + credentialNamePattern + `'?[^\S\n]*[:=][^\S\n]*')([^'\n]{8,})(')`)
	bareCredentialAssignment = regexp.MustCompile(
		`(?i)(\b` + credentialNamePattern + `[^\S\n]*[:=][^\S\n]*)([^\s"',;&})\]><]{8,})`)
)

var placeholderValuePattern = regexp.MustCompile(
	`^(?:\$\{[^}]*\}?|\{\{[^}]*\}{0,2}|%\([^)]*\)?s?|\$[A-Z_][A-Z0-9_]*)$`)

var authSchemeValuePattern = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)([A-Za-z0-9._~+/=-]{8,})`)

var authorizationHeaderSpanPattern = regexp.MustCompile(
	`(?i)(\b(?:proxy-)?authorization[^\S\n]*[:=][^\S\n]*)([^\n]*)`)

var authorizationSchemePrefixPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*[^\S\n]+`)

var authorizationParamListPattern = regexp.MustCompile(
	`(?i)^[A-Za-z][A-Za-z0-9_-]*[^\S\n]*=[^\S\n]*"|,[^\S\n]*[A-Za-z][A-Za-z0-9_-]*[^\S\n]*=`)

var authorizationParamPattern = regexp.MustCompile(
	`(?i)^([A-Za-z][A-Za-z0-9_-]*)([^\S\n]*=[^\S\n]*)("?)([^"]*)("?)$`)

var authorizationParamCredentials = []string{"response", "cnonce", "opaque"}

func isAuthorizationHeaderName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "authorization" || lower == "proxy-authorization"
}

var nonSecretEnumValues = []string{
	"none", "null", "nil", "n/a", "na", "unset", "empty", "default",
	"basic", "bearer", "digest", "oauth", "oauth2", "apikey",
	"true", "false", "yes", "no", "on", "off", "0", "1",
	"required", "optional", "enabled", "disabled", "accepted",
}

func isNonSecretEnumValue(value string) bool {
	return slices.Contains(nonSecretEnumValues, strings.ToLower(strings.TrimSpace(value)))
}

func isLowerAlphaWord(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return s != ""
}

var opaqueSecretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.+?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}`),
	regexp.MustCompile(`\b(?:xox[abopsr]-[A-Za-z0-9-]{8,}|xapp-[A-Za-z0-9-]{8,}` +
		`|sk-[A-Za-z0-9_-]{8,}|sk_(?:live|test)_[A-Za-z0-9]{8,}` +
		`|github_pat_[A-Za-z0-9_]{16,}|gh[pousr]_[A-Za-z0-9]{16,}|glpat-[A-Za-z0-9_-]{16,}` +
		`|AKIA[0-9A-Z]{12,}|AIza[A-Za-z0-9_-]{20,})`),
}

var webhookURLPattern = regexp.MustCompile(
	`(?i)(https://(?:hooks\.slack\.com/services|discord(?:app)?\.com/api/webhooks)/)[A-Za-z0-9/_-]{8,}`)

var embeddedURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>\\]+`)

var credentialKeyNeedles = []string{
	"apikey", "accesskey", "secretkey", "privatekey", "clientsecret",
	"accesstoken", "refreshtoken", "idtoken", "sessiontoken", "bearertoken", "authtoken",
	"subscriptionkey", "licensekey", "deploykey", "signingkey", "masterkey", "encryptionkey",
	"accountkey", "connectionstring", "apisecret", "secretaccesskey",
	"appkey", "consumerkey", "clientkey", "sessionkey", "sharedkey", "bindpw",
}

var credentialQueryKeyWords = []string{"key", "sig", "session", "sessionid", "sid", "passcode"}

var credentialKeyWords = []string{
	"secret", "secrets", "password", "passwords", "passwd", "passphrase", "passphrases", "pwd",
	"token", "auth", "authorization", "credential", "credentials",
	"cookie", "cookies", "signature", "signatures", "sig", "hmac", "jwt", "pat",
	"passcode", "pin", "dsn",
}

var credentialMetadataSuffixes = []string{
	"url", "uri", "endpoint", "type", "kind", "name", "id", "scheme",
	"required", "enabled", "present", "count", "length", "expiry", "expires", "ttl",
	"method", "provider", "mode", "status", "algorithm", "alg", "version",
	"scope", "scopes", "policy", "domain", "path", "format", "at", "secure",
}

func describesCredential(key string) bool {
	return describesCredentialWords(splitKeyWords(key))
}

func describesCredentialWords(words []string) bool {
	return len(words) > 0 && slices.Contains(credentialMetadataSuffixes, words[len(words)-1])
}

func isCredentialKey(key string) bool {
	return isCredentialKeyWords(splitKeyWords(key))
}

func isCredentialKeyWords(words []string) bool {
	if describesCredentialWords(words) {
		return false
	}
	normalized := strings.Join(words, "")
	for _, needle := range credentialKeyNeedles {
		if strings.Contains(normalized, needle) {
			return true
		}
	}
	return slices.ContainsFunc(words, func(word string) bool {
		return slices.Contains(credentialKeyWords, word)
	})
}

func isCredentialQueryKey(key string) bool {
	words := splitKeyWords(key)
	if isCredentialKeyWords(words) {
		return true
	}
	return len(words) == 1 && slices.Contains(credentialQueryKeyWords, words[0])
}

func splitKeyWords(key string) []string {
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	runes := []rune(key)
	isLowerAt := func(i int) bool {
		return i >= 0 && i < len(runes) && runes[i] >= 'a' && runes[i] <= 'z'
	}
	for i, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			if isLowerAt(i-1) || (i > 0 && isLowerAt(i+1)) {
				flush()
			}
			current.WriteRune(r)
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return words
}

type redaction struct {
	cut bool
}

func (r *redaction) str(s string) string {
	s, cutBefore := tool.CapText(s, maxRedactedValueBytes)
	s = embeddedURLPattern.ReplaceAllStringFunc(s, func(match string) string {
		trimmed := strings.TrimRight(match, `.,;:!?)]}'"`)
		return redactURLCredentials(trimmed) + match[len(trimmed):]
	})
	s = redactAuthorizationHeaders(s)
	s = redactAssignedValues(s, doubleQuotedCredentialAssignment, `${1}`+RedactedMarker+`${3}`, nil)
	s = redactAssignedValues(s, singleQuotedCredentialAssignment, `${1}`+RedactedMarker+`${3}`, nil)
	s = redactAssignedValues(s, bareCredentialAssignment, `${1}`+RedactedMarker, isAuthorizationHeaderName)
	s = redactAuthSchemeValues(s)
	for _, pattern := range opaqueSecretValuePatterns {
		s = pattern.ReplaceAllString(s, RedactedMarker)
	}
	s = webhookURLPattern.ReplaceAllString(s, `${1}`+RedactedMarker)
	s, cutAfter := tool.CapText(s, maxRedactedValueBytes)
	r.cut = r.cut || cutBefore || cutAfter
	return s
}

func redactAuthorizationHeaders(s string) string {
	return authorizationHeaderSpanPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := authorizationHeaderSpanPattern.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		return groups[1] + redactAuthorizationValue(groups[2])
	})
}

func redactAuthorizationValue(value string) string {
	scheme := authorizationSchemePrefixPattern.FindString(value)
	rest := value[len(scheme):]
	if authorizationParamListPattern.MatchString(rest) {
		return scheme + redactAuthorizationParams(rest)
	}
	end := strings.IndexAny(rest, "\"',;")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return value
	}
	return scheme + RedactedMarker + rest[end:]
}

func redactAuthorizationParams(list string) string {
	parts := strings.Split(list, ",")
	for i, part := range parts {
		groups := authorizationParamPattern.FindStringSubmatch(strings.TrimSpace(part))
		if groups == nil {
			continue
		}
		name := groups[1]
		if !slices.Contains(authorizationParamCredentials, strings.ToLower(name)) && !isCredentialKey(name) {
			continue
		}
		lead := part[:len(part)-len(strings.TrimLeft(part, " \t"))]
		parts[i] = lead + name + groups[2] + groups[3] + RedactedMarker + groups[5]
	}
	return strings.Join(parts, ",")
}

func redactAuthSchemeValues(s string) string {
	return authSchemeValuePattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := authSchemeValuePattern.FindStringSubmatch(match)
		if len(groups) < 4 || isLowerAlphaWord(strings.TrimRight(groups[3], ".")) {
			return match
		}
		return groups[1] + groups[2] + RedactedMarker
	})
}

func redactAssignedValues(s string, pattern *regexp.Regexp, template string, ownedElsewhere func(name string) bool) string {
	return pattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := pattern.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		if placeholderValuePattern.MatchString(groups[2]) {
			return match
		}
		name := assignedName(groups[1])
		if describesCredential(name) {
			return match
		}
		if ownedElsewhere != nil && ownedElsewhere(name) {
			return match
		}
		if !endsWithQuote(groups[1]) && looksLikeExpression(groups[2]) {
			return match
		}
		return pattern.ReplaceAllString(match, template)
	})
}

func assignedName(prefix string) string {
	return strings.Trim(strings.TrimRight(prefix, " \t:=\"'"), "\"'")
}

func endsWithQuote(prefix string) bool {
	return strings.HasSuffix(prefix, `"`) || strings.HasSuffix(prefix, `'`)
}

func looksLikeExpression(value string) bool {
	return strings.HasSuffix(value, "(") || strings.HasSuffix(value, "[")
}

func redactURLCredentials(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}

	rest := raw[scheme+3:]
	authorityEnd := strings.IndexAny(rest, "/?#")
	authority := rest
	if authorityEnd >= 0 {
		authority = rest[:authorityEnd]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		userinfo := authority[:at]
		if colon := strings.IndexByte(userinfo, ':'); colon >= 0 {
			userinfo = userinfo[:colon+1] + RedactedMarker
		}
		rest = userinfo + "@" + rest[at+1:]
	}

	out := raw[:scheme+3] + rest
	query := strings.IndexByte(out, '?')
	fragment := strings.IndexByte(out, '#')
	if query >= 0 && (fragment < 0 || query < fragment) {
		if fragment < 0 {
			return out[:query+1] + redactPairList(out[query+1:])
		}
		return out[:query+1] + redactPairList(out[query+1:fragment]) +
			out[fragment:fragment+1] + redactPairList(out[fragment+1:])
	}
	if fragment >= 0 && strings.ContainsRune(out[fragment:], '=') {
		return out[:fragment+1] + redactPairList(out[fragment+1:])
	}
	return out
}

func redactPairList(list string) string {
	if list == "" {
		return list
	}
	var out strings.Builder
	start := 0
	for i := 0; i <= len(list); i++ {
		if i < len(list) && list[i] != '&' && list[i] != ';' && list[i] != '?' {
			continue
		}
		segment := list[start:i]
		if eq := strings.IndexByte(segment, '='); eq > 0 && eq < len(segment)-1 &&
			isCredentialQueryKey(segment[:eq]) && !placeholderValuePattern.MatchString(segment[eq+1:]) &&
			!isNonSecretEnumValue(segment[eq+1:]) {
			out.WriteString(segment[:eq+1])
			out.WriteString(RedactedMarker)
		} else {
			out.WriteString(segment)
		}
		if i < len(list) {
			out.WriteByte(list[i])
		}
		start = i + 1
	}
	return out.String()
}

func RedactToolArgs(args map[string]any) map[string]any {
	return (&redaction{}).mapOf(args, notCredential)
}

func (r *redaction) mapOf(args map[string]any, scope credentialScope) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = r.node(value, childScope(scope, key))
	}
	return out
}

func childScope(parent credentialScope, key string) credentialScope {
	switch {
	case isCredentialKey(key):
		return namedCredential
	case parent != notCredential && !describesCredential(key):
		return inheritedCredential
	}
	return notCredential
}

func (r *redaction) node(value any, scope credentialScope) any {
	switch typed := value.(type) {
	case nil:
		return value
	case bool, float64, float32, int, int64, json.Number:
		if scope == namedCredential {
			return RedactedMarker
		}
		return value
	case string:
		if redactsLeaf(scope, typed) {
			return RedactedMarker
		}
		return r.str(typed)
	case map[string]any:
		return r.mapOf(typed, scope)
	case map[string]string:
		if typed == nil {
			return value
		}
		out := make(map[string]string, len(typed))
		for key, inner := range typed {
			if redactsLeaf(childScope(scope, key), inner) {
				out[key] = RedactedMarker
				continue
			}
			out[key] = r.str(inner)
		}
		return out
	case []string:
		if typed == nil {
			return value
		}
		out := make([]string, len(typed))
		for i, inner := range typed {
			if redactsLeaf(scope, inner) {
				out[i] = RedactedMarker
				continue
			}
			out[i] = r.str(inner)
		}
		return out
	case []any:
		if typed == nil {
			return value
		}
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = r.node(inner, scope)
		}
		return out
	default:
		return r.unknown(value, scope)
	}
}

func redactsLeaf(scope credentialScope, value string) bool {
	switch scope {
	case namedCredential:
		return !isNonSecretEnumValue(value)
	case inheritedCredential:
		return len(value) >= inheritedCredentialLeafMinBytes
	default:
		return false
	}
}

func RedactValue(value any) any {
	redacted, _ := RedactValueReportingCut(value)
	return redacted
}

func RedactValueReportingCut(value any) (any, bool) {
	r := &redaction{}
	redacted := r.node(value, notCredential)
	return redacted, r.cut
}

func (r *redaction) unknown(value any, scope credentialScope) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return unserializableMarker
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return unserializableMarker
	}
	return r.node(normalized, scope)
}
