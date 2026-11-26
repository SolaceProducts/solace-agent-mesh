// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

var ErrConnectionConflict = errors.New("cannot share the connection or credential slot of an earlier entry")

type connectionIdentity struct {
	Type             string            `conn:"type"`
	URL              string            `conn:"url"`
	Command          string            `conn:"command"`
	Args             []string          `conn:"args"`
	Env              map[string]string `conn:"env"`
	SSEReadTimeout   time.Duration     `conn:"sse_read_timeout"`
	MaxResponseBytes int64             `conn:"max_response_bytes"`
	TerminateOnClose bool              `conn:"terminate_on_close"`
	SSLVerify        bool              `conn:"ssl_config.verify"`
	SSLCABundle      string            `conn:"ssl_config.ca_bundle"`
	SSLClientCert    string            `conn:"ssl_config.client_cert"`
	SSLClientKey     string            `conn:"ssl_config.client_key"`
	SSLServerName    string            `conn:"ssl_config.server_name"`
	Headers          map[string]string `conn:"headers"`
	Auth             *authIdentity     `conn:"auth"`
	Manifest         bool              `conn:"manifest"`
}

type authIdentity struct {
	EntryType            string                   `conn:"auth.entry_type"`
	Type                 string                   `conn:"auth.type"`
	Scheme               *toolauth.OAuthScheme    `conn:"auth.scheme"`
	Credential           *toolauth.AuthCredential `conn:"auth.credential"`
	CredentialKey        string                   `conn:"auth.credential_key"`
	ResourceURL          string                   `conn:"auth.resource_url"`
	CACertPath           string                   `conn:"auth.ca_cert_path"`
	InsecureSkipVerify   bool                     `conn:"auth.insecure_skip_verify"`
	DisablePKCE          bool                     `conn:"auth.disable_pkce"`
	DisableConsentPrompt bool                     `conn:"auth.disable_consent_prompt"`
}

func (cfg *ToolsetConfig) connectionIdentity() connectionIdentity {
	return connectionIdentity{
		Type:             cfg.ConnectionType,
		URL:              cfg.URL,
		Command:          cfg.Command,
		Args:             nilIfEmpty(cfg.CommandArgs),
		Env:              nilIfEmptyMap(cfg.EnvVars),
		SSEReadTimeout:   cfg.EffectiveSSEReadTimeout(),
		MaxResponseBytes: cfg.EffectiveMaxResponseBytes(),
		TerminateOnClose: cfg.TerminateOnClose == nil || *cfg.TerminateOnClose,
		SSLVerify:        cfg.EffectiveSSLVerify(),
		SSLCABundle:      cfg.SSLCABundle,
		SSLClientCert:    cfg.SSLClientCert,
		SSLClientKey:     cfg.SSLClientKey,
		SSLServerName:    cfg.SSLServerName,
		Headers:          lowerCaseKeys(cfg.Headers),
		Auth:             cfg.authIdentity(),
		Manifest:         cfg.Manifest != nil,
	}
}

func (cfg *ToolsetConfig) authIdentity() *authIdentity {
	ac := cfg.AuthConfig
	if ac == nil && cfg.AuthType == "" {
		return nil
	}
	id := &authIdentity{EntryType: cfg.AuthType}
	if ac == nil {
		return id
	}
	id.Type = ac.Type
	id.CredentialKey = ac.CredentialKey
	id.ResourceURL = ac.ResourceURL
	id.CACertPath = ac.CACertPath
	id.InsecureSkipVerify = ac.InsecureSkipVerify
	id.DisablePKCE = ac.DisablePKCE
	id.DisableConsentPrompt = ac.DisableConsentPrompt
	if ac.Scheme != nil {
		scheme := *ac.Scheme
		scheme.Scopes = nilIfEmpty(scheme.Scopes)
		if !reflect.DeepEqual(scheme, toolauth.OAuthScheme{}) {
			id.Scheme = &scheme
		}
	}
	if ac.Credential != nil && !reflect.DeepEqual(*ac.Credential, toolauth.AuthCredential{}) {
		id.Credential = ac.Credential
	}
	return id
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return maps.Clone(m)
}

func lowerCaseKeys(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for _, name := range slices.Sorted(maps.Keys(m)) {
		out[strings.ToLower(name)] = m[name]
	}
	return out
}

const clientIDDigestLen = 16

func (cfg *ToolsetConfig) deriveClientID() (string, error) {
	digest, err := cfg.connectionIdentity().digest()
	if err != nil {
		return "", err
	}
	return sanitizeName("mcp_"+clientIDLabel(cfg)) + "_" + digest, nil
}

const unnamedServerLabel = "server"

func clientIDLabel(cfg *ToolsetConfig) string {
	if cfg.ConnectionType == connTypeStdio {
		parts := strings.Split(cfg.Command, "/")
		return parts[len(parts)-1]
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Hostname() == "" || (u.User == nil && strings.Contains(cfg.URL, "@")) {
		return unnamedServerLabel
	}
	if port := u.Port(); port != "" {
		return u.Hostname() + "_" + port
	}
	return u.Hostname()
}

func (id connectionIdentity) digest() (string, error) {
	h := sha256.New()
	if err := writeCanonical(h, reflect.ValueOf(id)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:clientIDDigestLen], nil
}

func writeCanonical(w io.Writer, v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		_, _ = fmt.Fprintf(w, "s%d:%s;", v.Len(), v.String())
	case reflect.Bool:
		_, _ = fmt.Fprintf(w, "b%t;", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		_, _ = fmt.Fprintf(w, "i%d;", v.Int())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			_, _ = io.WriteString(w, "nil;")
			return nil
		}
		_, _ = io.WriteString(w, "ref;")
		return writeCanonical(w, v.Elem())
	case reflect.Slice, reflect.Array:
		_, _ = fmt.Fprintf(w, "list%d[", v.Len())
		for i := range v.Len() {
			if err := writeCanonical(w, v.Index(i)); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(w, "];")
	case reflect.Map:
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		_, _ = fmt.Fprintf(w, "map%d{", v.Len())
		for _, k := range keys {
			if err := writeCanonical(w, k); err != nil {
				return err
			}
			if err := writeCanonical(w, v.MapIndex(k)); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(w, "};")
	case reflect.Struct:
		_, _ = fmt.Fprintf(w, "struct%d{", v.NumField())
		for i := range v.NumField() {
			if err := writeCanonical(w, reflect.ValueOf(v.Type().Field(i).Name)); err != nil {
				return err
			}
			if err := writeCanonical(w, v.Field(i)); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(w, "};")
	default:
		return fmt.Errorf("mcp connection identity: no canonical encoding for kind %s", v.Kind())
	}
	return nil
}

func connectionDifferences(a, b *ToolsetConfig) []string {
	ia, ib := reflect.ValueOf(a.connectionIdentity()), reflect.ValueOf(b.connectionIdentity())
	fields := ia.Type()
	var diffs []string
	for i := range fields.NumField() {
		if !reflect.DeepEqual(ia.Field(i).Interface(), ib.Field(i).Interface()) {
			diffs = append(diffs, fields.Field(i).Tag.Get("conn"))
		}
	}
	return diffs
}

func connectionConflict(cfg *ToolsetConfig, diffs []string) error {
	return fmt.Errorf("add MCP client for server %s: this entry %w (client id %q is already open and this entry differs in %s)",
		serverForLog(cfg), ErrConnectionConflict, cfg.ClientID, strings.Join(diffs, ", "))
}

func (cfg *ToolsetConfig) SharedStateKey() string {
	if slot, ok := authSlotOf(cfg); ok {
		return "auth:" + slot.credentialKey + "\x00" + slot.server
	}
	return "client:" + cfg.ClientID
}

func authDifferences(a, b authIdentity) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var diffs []string
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			diffs = append(diffs, va.Type().Field(i).Tag.Get("conn"))
		}
	}
	return diffs
}

func credentialSlotConflict(cfg *ToolsetConfig, diffs []string) error {
	return fmt.Errorf("add MCP client for server %s: this entry %w (it uses the same credential key as an earlier entry for this server but differs in %s; set a distinct credential_key on each entry)",
		serverForLog(cfg), ErrConnectionConflict, strings.Join(diffs, ", "))
}
