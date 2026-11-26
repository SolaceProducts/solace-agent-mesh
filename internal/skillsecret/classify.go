// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/secretname"
)

var credentialHeaderNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
}

var safeAuthKeys = toSet(
	"type", "in", "name", "location", "username", "user", "client_id",
	"scopes", "scope", "audience", "grant_type", "credential_key",
	"ca_cert_path", "ca_cert", "cert_path", "resource", "display_name",
	"realm", "tenant_id", "tenant", "domain", "issuer", "token_url",
	"authorization_url", "header", "prefix", "scheme",
)

var safeHeaderNames = toSet(
	"content-type", "accept", "user-agent", "x-tenant-id",
	"accept-language", "accept-encoding", "x-request-id",
)

var safeEnvNames = toSet(
	"LOG_LEVEL", "DEBUG", "PATH", "HOME", "PWD", "TZ", "LANG", "LC_ALL",
	"NODE_ENV", "ENV", "ENVIRONMENT", "PORT", "HOST", "REGION", "AWS_REGION",
	"FEATURE_FLAGS",
)

var safeEnvSuffixes = []string{
	"_URL", "_URI", "_HOST", "_PORT", "_REGION", "_LEVEL", "_MODE",
	"_DIR", "_PATH", "_TIMEOUT", "_ENDPOINT", "_ID",
	"_NAME", "_ARN", "_POLICY", "_LIMIT", "_PROFILE", "_CREDENTIALS",
}

var mapSafeSuffixes = []string{
	"_url", "_uri", "_endpoint", "_method", "_name", "_arn", "_policy", "_limit", "_profile",
}

const credentialsSuffix = "_credentials"

var nonSecretKeySuffixes = append([]string{credentialsSuffix}, mapSafeSuffixes...)

var pathShaped = regexp.MustCompile(`^(/|\./|\.\./|~|[A-Za-z]:[\\/])`)

var nonSecretKeyNames = toSet(
	"cache_key", "partition_key", "sort_key", "idempotency_key", "primary_key",
	"foreign_key", "public_key", "credential_key", "hash_key", "range_key",
	"row_key", "lookup_key", "dedupe_key",
)

var passwordLikeSegments = toSet("password", "passwd", "pwd", "pin", "passcode")

var secretQueryParams = toSet(
	"sig", "signature", "token", "access_token", "key", "api_key", "apikey", "code",
)

var tokenUserPrefixes = []string{
	"ghp_", "gho_", "ghu_", "ghs_", "github_pat_", "glpat-", "sk_", "sk-",
	"pk_", "rk_", "xoxb-", "xoxp-", "xoxa-", "AKIA",
}

var (
	referenceSpan  = regexp.MustCompile(`\$\{([^}:\s]+)(?:(\s*,\s*|:-|:\+)((?:[^$}]|\$[^{}]|\$\{[^}]*\}|\$)*))?\}`)
	wholeReference = regexp.MustCompile(`^` + referenceSpan.String() + `$`)

	countLikeKey  = regexp.MustCompile(`(?:^|_)(?:max|min|num|count|limit|size|timeout|port|retries|retry|ttl|expir[a-z]*|seconds|version|interval|days|lifetime|duration)(?:_|$)|_tokens$`)
	envAssignName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerName    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
	pemPrivateKey = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	jwtShape      = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)
	digitsOnly    = regexp.MustCompile(`^[0-9]+$`)
	jsonString    = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"\s*:\s*"((?:[^"\\]|\\.)+)"`)

	authSchemeValue   = regexp.MustCompile(`^\s*(?i:bearer|basic)\s+([A-Za-z0-9\-._~+/]{16,})=*\s*$`)
	connStringSecrets = regexp.MustCompile(`(?i)(?:password|pwd|secret)=([^;&\s]+)`)
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}

func hasNonSecretSuffix(lowerKey string) bool { return hasSuffixIn(lowerKey, nonSecretKeySuffixes) }

func hasSuffixIn(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func redactionFor(r scalarRef) (string, bool) {
	if !r.inConfig {
		return "", false
	}
	v := r.value()
	tag := r.shortTag()
	if v == "" || v == RedactedSentinel || tag == "!!null" || tag == "!!bool" {
		return "", false
	}
	if r.argSecret && r.argFlag != "" && digitsOnly.MatchString(strings.TrimPrefix(v, r.argPrefix)) && isCountLikeKey(r.argFlag) {
		return "", false
	}
	if r.argPrefix != "" {
		return redactArgValue(r.argPrefix, v)
	}
	if strings.HasPrefix(v, "vault://") {
		return "", false
	}
	if m := wholeReference.FindStringSubmatch(v); m != nil {
		return RedactedSentinel, literalRedacts(r, m[3], "!!str", false)
	}
	if tag == "!!int" || tag == "!!float" {
		return RedactedSentinel, numberIsCredential(r)
	}
	return RedactedSentinel, literalRedacts(r, v, tag, true)
}

const refToken = "REFX"

func isRefOnly(s string) bool { return s != "" && strings.ReplaceAll(s, refToken, "") == "" }

func literalRedacts(r scalarRef, text, tag string, mapRule bool) bool {
	if text == "" {
		return false
	}
	literal := referenceSpan.ReplaceAllString(text, refToken)
	if literal == text {
		if keyNameIsCredential(r) || credentialsKeyHoldsSecret(r.key, text) || (mapRule && keyDefaultRedacts(r)) {
			return true
		}
	}
	return tag == "!!str" && valueLooksSecret(r.key, literal)
}

func redactArgValue(prefix, v string) (string, bool) {
	value := strings.TrimPrefix(v, prefix)
	if value == "" || value == RedactedSentinel || strings.HasPrefix(value, "vault://") {
		return "", false
	}
	if m := wholeReference.FindStringSubmatch(value); m != nil {
		return prefix + RedactedSentinel, strings.TrimSpace(referenceSpan.ReplaceAllString(m[3], "")) != ""
	}
	if referenceSpan.MatchString(value) {
		return "", false
	}
	return prefix + RedactedSentinel, true
}

func keyNameIsCredential(r scalarRef) bool {
	return r.argSecret || isSecretKeyName(r.key) || credentialHeaderNames[strings.ToLower(r.key)]
}

func isCredentialsKey(key string) bool {
	return strings.HasSuffix(normalizedName(key), credentialsSuffix)
}

func credentialsKeyHoldsSecret(key, text string) bool {
	return isCredentialsKey(key) && !pathShaped.MatchString(text)
}

func keyDefaultRedacts(r scalarRef) bool { return r.mapKind != "" && !isSafeMapKey(r) }

func numberIsCredential(r scalarRef) bool {
	switch {
	case r.argSecret:
		return true
	case isCountLikeKey(r.key):
		return false
	case isPasswordLikeKey(r.key), isSecretKeyName(r.key), isCredentialsKey(r.key):
		return true
	case r.mapKind == authKey || r.mapKind == headersKey:
		return !isSafeMapKey(r)
	}
	return false
}

func isCountLikeKey(key string) bool { return countLikeKey.MatchString(normalizedName(key)) }

func isSafeMapKey(r scalarRef) bool {
	lower := strings.ToLower(r.key)
	if hasSuffixIn(lower, mapSafeSuffixes) {
		return true
	}
	switch r.mapKind {
	case headersKey:
		if r.headerName != "" {
			if lower == "value" {
				return safeHeaderNames[strings.ToLower(r.headerName)]
			}
			return lower == "name"
		}
		return safeHeaderNames[lower]
	case envKey:
		return isSafeEnvName(strings.ToUpper(r.key))
	}
	return safeAuthKeys[lower]
}

func isSafeEnvName(upper string) bool {
	if safeEnvNames[upper] {
		return true
	}
	return hasSuffixIn(upper, safeEnvSuffixes)
}

func nameSegments(name string) []string {
	var segs []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}
	prevLower := false
	for _, r := range name {
		switch {
		case r == '_' || r == '-':
			flush()
			prevLower = false
			continue
		case unicode.IsUpper(r) && prevLower:
			flush()
		}
		cur.WriteRune(r)
		prevLower = unicode.IsLower(r)
	}
	flush()
	return segs
}

func normalizedName(key string) string {
	return strings.ToLower(strings.Join(nameSegments(key), "_"))
}

func isSecretKeyName(key string) bool {
	norm := normalizedName(key)
	if hasNonSecretSuffix(norm) || norm == "credential_key" || key == "PWD" {
		return false
	}
	if secretname.IsSecretFieldName(key) {
		return true
	}
	upper := strings.ToUpper(norm)
	for _, suffix := range []string{"PASSWORD", "PASSWD", "PWD"} {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	if strings.Contains(upper, "PRIVATE") && containsAny(upper, "KEY", "TOKEN", "SECRET") {
		return true
	}
	return endsWithKey(key, norm)
}

func endsWithKey(key, norm string) bool {
	for name := range nonSecretKeyNames {
		if norm == name || strings.HasSuffix(norm, "_"+name) {
			return false
		}
	}
	segs := nameSegments(key)
	return len(segs) > 0 && strings.EqualFold(segs[len(segs)-1], "key")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isPasswordLikeKey(key string) bool {
	if key == "PWD" || hasNonSecretSuffix(strings.ToLower(key)) {
		return false
	}
	norm := normalizedName(key)
	for _, seg := range strings.Split(norm, "_") {
		if passwordLikeSegments[seg] {
			return true
		}
	}
	for _, suffix := range []string{"password", "passwd", "pwd"} {
		if strings.HasSuffix(norm, suffix) {
			return true
		}
	}
	return false
}

func valueLooksSecret(key, v string) bool {
	if hasLiteralCapture(authSchemeValue, v) || hasLiteralCapture(connStringSecrets, v) || hasSecretShape(v) {
		return true
	}
	u := parseHTTPURL(v)
	if u == nil {
		return false
	}
	if strings.Contains(strings.ToLower(key), "webhook") {
		return true
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if (password != "" && !isRefOnly(password)) || looksLikeTokenUsername(u.User.Username()) {
			return true
		}
	}
	for name, values := range u.Query() {
		if (secretQueryParams[strings.ToLower(name)] || secretname.IsSecretFieldName(name)) &&
			slices.ContainsFunc(values, func(v string) bool { return v != "" && !isRefOnly(v) }) {
			return true
		}
	}
	return false
}

func hasLiteralCapture(re *regexp.Regexp, v string) bool {
	for _, m := range re.FindAllStringSubmatch(v, -1) {
		if !isRefOnly(m[1]) {
			return true
		}
	}
	return false
}

func hasSecretShape(v string) bool {
	if pemPrivateKey.MatchString(v) {
		return true
	}
	trimmed := strings.TrimSpace(v)
	return jwtShape.MatchString(trimmed) || (strings.HasPrefix(trimmed, "{") && jsonHoldsSecretString(trimmed))
}

func jsonHoldsSecretString(doc string) bool {
	for _, m := range jsonString.FindAllStringSubmatch(doc, -1) {
		if isSecretKeyName(m[1]) && !isCountLikeKey(m[1]) && !isRefOnly(m[2]) {
			return true
		}
	}
	return false
}

func looksLikeTokenUsername(user string) bool {
	for _, prefix := range tokenUserPrefixes {
		if strings.HasPrefix(user, prefix) {
			return true
		}
	}
	if len(user) < 20 {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range user {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		case r != '_' && r != '-':
			return false
		}
	}
	return hasLetter && hasDigit
}

func parseHTTPURL(v string) *url.URL {
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}

func argRedaction(args []*yaml.Node, i int) (secret bool, prefix, flag string) {
	arg := args[i].Value
	if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
		if embedded, ok := embeddedSecretPrefix(arg[2:]); ok {
			return true, arg[:2] + embedded, ""
		}
	}
	if isFlag(arg) {
		name, value, inline := strings.Cut(arg, "=")
		if !inline || value == "" {
			return false, "", ""
		}
		if isSecretFlag(name) {
			return true, name + "=", flagName(name)
		}
		if embedded, ok := embeddedSecretPrefix(value); ok {
			return true, name + "=" + embedded, ""
		}
		return false, "", ""
	}
	if embedded, ok := embeddedSecretPrefix(arg); ok {
		return true, embedded, ""
	}
	if i > 0 {
		prev := args[i-1].Value
		if isFlag(prev) && !strings.Contains(prev, "=") && isSecretFlag(prev) {
			return true, "", flagName(prev)
		}
	}
	return false, "", ""
}

func flagName(flag string) string { return strings.TrimLeft(flag, "-") }

func embeddedSecretPrefix(s string) (string, bool) {
	if name, value, ok := strings.Cut(s, "="); ok && value != "" && envAssignName.MatchString(name) && isSecretKeyName(name) {
		return name + "=", true
	}
	name, value, ok := strings.Cut(s, ":")
	if !ok || !headerName.MatchString(name) || (!credentialHeaderNames[strings.ToLower(name)] && !isSecretKeyName(name)) {
		return "", false
	}
	prefix := name + ":"
	if rest, spaced := strings.CutPrefix(value, " "); spaced {
		prefix, value = prefix+" ", rest
	}
	return prefix, value != ""
}

func isFlag(s string) bool { return len(s) > 1 && s[0] == '-' }

func isSecretFlag(flag string) bool {
	return isSecretKeyName(strings.TrimLeft(flag, "-"))
}

func sentinelOf(r scalarRef) (string, bool) {
	v := r.value()
	if v == RedactedSentinel {
		return "", true
	}
	if !r.inArgs || !r.inConfig {
		return "", false
	}
	prefix, ok := strings.CutSuffix(v, RedactedSentinel)
	if ok && (strings.HasSuffix(prefix, "=") || strings.HasSuffix(prefix, ":") || strings.HasSuffix(prefix, ": ")) {
		return prefix, true
	}
	return "", false
}

func prefixName(prefix string) string {
	name := strings.TrimLeft(prefix, "-")
	name = strings.TrimSuffix(name, ": ")
	name = strings.TrimSuffix(name, ":")
	return strings.TrimSuffix(name, "=")
}
