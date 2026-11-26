// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"encoding/json"
)

const codeVerifierKey = "code_verifier"

func StripAuthCodeVerifier(raw []byte) []byte {
	if !bytes.Contains(raw, []byte(`"`+codeVerifierKey+`"`)) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return raw
	}
	if !deleteCodeVerifier(tree) {
		return raw
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tree); err != nil {
		return raw
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

func StripAuthCodeVerifierFromParts(parts []Part) {
	for i := range parts {
		if parts[i].Kind != "data" {
			continue
		}
		if data, ok := parts[i].Data.(map[string]any); ok {
			deleteCodeVerifier(data)
		}
	}
}

func deleteCodeVerifier(node any) bool {
	removed := false
	switch typed := node.(type) {
	case map[string]any:
		if typed["type"] == string(SignalAuthenticationRequired) {
			if _, ok := typed[codeVerifierKey]; ok {
				delete(typed, codeVerifierKey)
				removed = true
			}
		}
		for _, val := range typed {
			removed = deleteCodeVerifier(val) || removed
		}
	case []any:
		for _, item := range typed {
			removed = deleteCodeVerifier(item) || removed
		}
	}
	return removed
}

const RedactedAuthValue = "<REDACTED>"

var authSecretKeys = map[string]struct{}{
	codeVerifierKey: {}, "auth_uri": {}, "auth_response_uri": {},
}

var authSignalSecretKeys = map[SignalType]map[string]struct{}{
	SignalAuthResponse: {"code": {}, "state": {}},
}

var authProbeTokens = buildAuthProbeTokens()

func buildAuthProbeTokens() [][]byte {
	tokens := make([][]byte, 0, len(authSecretKeys)+len(authSignalSecretKeys))
	for key := range authSecretKeys {
		tokens = append(tokens, []byte(`"`+key+`"`))
	}
	for signalType := range authSignalSecretKeys {
		tokens = append(tokens, []byte(`"`+string(signalType)+`"`))
	}
	return tokens
}

func PayloadHasAuthMaterial(raw []byte) bool {
	for _, token := range authProbeTokens {
		if bytes.Contains(raw, token) {
			return true
		}
	}
	return false
}

func RedactAuthSecrets(node any) {
	switch typed := node.(type) {
	case map[string]any:
		signalType, _ := typed["type"].(string)
		signalKeys := authSignalSecretKeys[SignalType(signalType)]
		for key, val := range typed {
			if _, isString := val.(string); isString {
				if _, isSecret := authSecretKeys[key]; isSecret {
					typed[key] = RedactedAuthValue
					continue
				}
				if _, isSecret := signalKeys[key]; isSecret {
					typed[key] = RedactedAuthValue
				}
				continue
			}
			RedactAuthSecrets(val)
		}
	case []any:
		for _, item := range typed {
			RedactAuthSecrets(item)
		}
	}
}
