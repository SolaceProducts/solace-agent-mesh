// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "encoding/json"

const redactedSentinel = "<REDACTED>"

var modelAuthSecretKeys = map[string][]string{
	"apikey":                  {"api_key"},
	"oauth2":                  {"client_secret", "oauth_client_secret"},
	"bearer":                  {"token"},
	"basic":                   {"password"},
	"aws_iam":                 {"aws_secret_access_key", "aws_session_token"},
	"gcp_service_account":     {"service_account_json", "vertex_credentials"},
	"none":                    {},
	"azure_workload_identity": {},
	"oauth2_delegated":        {},
}

var anyTypeSecretKeys = func() map[string]struct{} {
	all := make(map[string]struct{})
	for _, keys := range modelAuthSecretKeys {
		for _, k := range keys {
			all[k] = struct{}{}
		}
	}
	return all
}()

func redactModelAuthConfig(stored map[string]any) map[string]any {
	out := make(map[string]any, len(stored))
	authType, _ := stored["type"].(string)
	_, known := modelAuthSecretKeys[authType]
	for k, v := range stored {
		_, isSecretName := anyTypeSecretKeys[k]
		isScalar := isScalarAuthValue(v)
		switch {
		case v == nil && k != "type":
			out[k] = nil
		case k == "type" && isString(v):
			out[k] = v
		case !known || isSecretName || !isScalar:
			out[k] = redactedSentinel
		default:
			out[k] = v
		}
	}
	return out
}

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

func isScalarAuthValue(v any) bool {
	switch v.(type) {
	case string, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	}
	return false
}
