// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	defaultIMAPPort = 993

	legacyProbeGatewayType = "event_mesh"

	msgProbeRefusedStoredCredentials = "connection test refused stored credentials for changed destination"

	nonScalarPrefix = "\x00"

	unencodableText = "\x01"
)

type probeDestinationField struct {
	key  string
	read func(map[string]any) string
}

func textDestination(key string) probeDestinationField {
	return probeDestinationField{key: key, read: func(values map[string]any) string {
		text, _ := probeScalar(values[key])
		return text
	}}
}

var (
	imapPortDestination = probeDestinationField{key: "imap_port", read: func(values map[string]any) string {
		return strconv.Itoa(probeIMAPPort(values))
	}}
	tlsSkipVerifyDestination = probeDestinationField{key: "tls_skip_verify", read: func(values map[string]any) string {
		return strconv.FormatBool(probeTLSSkipVerify(values))
	}}
)

type probeBinding struct {
	destination      []probeDestinationField
	credentials      []string
	destinationLabel string
}

var (
	connectorEventMeshProbeBinding = probeBinding{
		destination:      []probeDestinationField{textDestination("broker_url")},
		credentials:      []string{"broker_password"},
		destinationLabel: "broker URL",
	}
	entrypointProbeBindings = map[string]probeBinding{
		"event_mesh": {
			destination:      []probeDestinationField{textDestination("broker_url"), tlsSkipVerifyDestination},
			credentials:      []string{"broker_password"},
			destinationLabel: "broker URL or TLS verification setting",
		},
		"email": {
			destination:      []probeDestinationField{textDestination("imap_host"), imapPortDestination},
			credentials:      []string{"imap_password", "oauth2_client_secret"},
			destinationLabel: "mail server",
		},
	}
)

func probeIMAPPort(values map[string]any) int {
	return asIntOrDefault(values, "imap_port", defaultIMAPPort)
}

func probeTLSSkipVerify(values map[string]any) bool {
	switch v := values["tls_skip_verify"].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

type boundProbeValues struct {
	mergeable    map[string]any
	submitted    map[string]any
	refusedField string
}

func bindProbeValues(stored, submitted map[string]any, binding probeBinding) (boundProbeValues, error) {
	bound := boundProbeValues{submitted: withCleanDestination(submitted, binding.destination)}
	if stored == nil {
		return bound, nil
	}
	changed := firstChangedDestination(stored, bound.submitted, binding.destination)
	if changed == "" {
		bound.mergeable = stored
		return bound, nil
	}
	if !holdsCredential(stored, binding.credentials) || holdsCredential(bound.submitted, binding.credentials) {
		return bound, nil
	}
	bound.refusedField = changed
	return bound, &ValidationError{Message: "The saved credentials are only used to test the saved " + binding.destinationLabel + ". To test a different one, enter the credentials again.", Field: fieldValues}
}

func logProbeRefusal(ctx context.Context, logger *slog.Logger, changedField string, attrs ...any) {
	if logger == nil {
		return
	}
	logger.Warn(msgProbeRefusedStoredCredentials, withCallerIdentity(ctx, append(attrs, "changedField", changedField))...)
}

func withCallerIdentity(ctx context.Context, attrs []any) []any {
	identity, ok := auth.SubjectFromContext(ctx)
	switch {
	case !ok:
		return attrs
	case identity.PlatformUserID() != "":
		return append(attrs, "userID", identity.PlatformUserID())
	case identity.UserID() != "":
		return append(attrs, "userFP", strutil.PIIFingerprint(identity.UserID()))
	}
	return attrs
}

func withCleanDestination(submitted map[string]any, fields []probeDestinationField) map[string]any {
	clean := maps.Clone(submitted)
	if clean == nil {
		clean = map[string]any{}
	}
	for _, field := range fields {
		text, supplied := probeScalar(clean[field.key])
		switch {
		case !supplied:
			delete(clean, field.key)
		case isString(clean[field.key]):
			clean[field.key] = text
		}
	}
	return clean
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func firstChangedDestination(stored, submitted map[string]any, fields []probeDestinationField) string {
	for _, field := range fields {
		if _, supplied := probeScalar(submitted[field.key]); supplied && field.read(submitted) != field.read(stored) {
			return field.key
		}
	}
	return ""
}

func holdsCredential(values map[string]any, keys []string) bool {
	for _, key := range keys {
		if text, ok := values[key].(string); ok {
			if text = strings.TrimSpace(text); text != "" && text != redactedSentinel {
				return true
			}
		}
	}
	return false
}

func probeScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", false
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == redactedSentinel {
			return "", false
		}
		return text, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return unencodableText, true
		}
		return nonScalarPrefix + string(encoded), true
	}
}
