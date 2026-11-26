// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const mcpSSLConfigKey = "ssl_config"

const mcpTLSDirEnvVar = "SAM_MCP_CONNECTOR_TLS_DIR"

type mcpSSLField struct {
	stored string
	wire   string
	ptr    func(*MCPSSLConfig) *string
}

var (
	sslCABundle   = mcpSSLField{"ca_bundle", "caBundle", func(c *MCPSSLConfig) *string { return &c.CABundle }}
	sslClientCert = mcpSSLField{"client_cert", "clientCert", func(c *MCPSSLConfig) *string { return &c.ClientCert }}
	sslClientKey  = mcpSSLField{"client_key", "clientKey", func(c *MCPSSLConfig) *string { return &c.ClientKey }}
	sslServerName = mcpSSLField{"server_name", "serverName", func(c *MCPSSLConfig) *string { return &c.ServerName }}

	mcpSSLFields     = []mcpSSLField{sslCABundle, sslClientCert, sslClientKey, sslServerName}
	mcpSSLFileFields = []mcpSSLField{sslCABundle, sslClientCert, sslClientKey}
)

func acceptedMCPSSLConfig(values map[string]any) map[string]any {
	stored, _ := values[mcpSSLConfigKey].(map[string]any)
	accepted := map[string]any{}
	for _, field := range mcpSSLFields {
		if value, ok := stored[field.stored]; ok {
			accepted[field.stored] = value
		}
	}
	return accepted
}

const msgUndeployableSSLConfig = "mcp connector ssl_config left out of the rendered tool because it fails the platform's checks, so the agent connects without it; save the connector with a valid ssl_config"

func deployableMCPSSLConfig(conn ConnectorConfig) map[string]any {
	accepted := acceptedMCPSSLConfig(conn.Values)
	if len(accepted) == 0 {
		return nil
	}
	if err := validateMCPSSLConfig(map[string]any{mcpSSLConfigKey: accepted}); err != nil {
		slog.Default().Warn(msgUndeployableSSLConfig, "connectorID", conn.ID, "problem", err.Error())
		return nil
	}
	return accepted
}

func keepStoredSSLConfig(merged, stored map[string]any) {
	sent, present := merged[mcpSSLConfigKey]
	if present && sent != nil {
		if block, isMap := sent.(map[string]any); isMap && len(block) == 0 {
			delete(merged, mcpSSLConfigKey)
		}
		return
	}
	if kept, isStored := stored[mcpSSLConfigKey]; isStored && kept != nil {
		merged[mcpSSLConfigKey] = kept
		return
	}
	delete(merged, mcpSSLConfigKey)
}

const (
	mcpSSLVerifyKey = "verify"

	msgMCPSSLVerifyRefused = "is not accepted on a connector: only an operator can turn off certificate verification, by setting " +
		mcpTLSVerifyEnvVar + "=false on the platform"
)

type sslSpelling struct {
	root         string
	field        string
	detailPrefix string
	name         func(mcpSSLField) string
}

var (
	storedSSLSpelling = sslSpelling{
		root: mcpSSLConfigKey, field: "values", detailPrefix: "values.",
		name: func(f mcpSSLField) string { return f.stored },
	}
	wireSSLSpelling = sslSpelling{
		root: "sslConfig", field: "sslConfig",
		name: func(f mcpSSLField) string { return f.wire },
	}
)

func (s sslSpelling) path(f mcpSSLField) string { return s.root + "." + s.name(f) }

type sslProblems struct {
	spelling sslSpelling
	lines    []string
	details  map[string][]string
}

func (p *sslProblems) add(path, problem string) {
	if p.details == nil {
		p.details = map[string][]string{}
	}
	key := p.spelling.detailPrefix + path
	p.details[key] = append(p.details[key], problem)
	p.lines = append(p.lines, path+" "+problem)
}

func (p *sslProblems) err() error {
	if len(p.lines) == 0 {
		return nil
	}
	return &ValidationError{Message: strings.Join(p.lines, "; "), Field: p.spelling.field, Details: p.details}
}

func validateMCPSSLConfig(values map[string]any) error {
	raw, present := values[mcpSSLConfigKey]
	if !present || raw == nil {
		return nil
	}
	problems := &sslProblems{spelling: storedSSLSpelling}
	block, isMap := raw.(map[string]any)
	if !isMap {
		problems.add(mcpSSLConfigKey, "must be a map with the keys "+allowedMCPSSLKeys())
		return problems.err()
	}
	checkMCPSSLSettings(readMCPSSLBlock(block, problems), problems)
	return problems.err()
}

func readMCPSSLBlock(block map[string]any, problems *sslProblems) *MCPSSLConfig {
	ssl := &MCPSSLConfig{}
	for _, key := range slices.Sorted(maps.Keys(block)) {
		if key == mcpSSLVerifyKey {
			problems.add(problems.spelling.root+"."+key, msgMCPSSLVerifyRefused)
			continue
		}
		i := slices.IndexFunc(mcpSSLFields, func(f mcpSSLField) bool { return f.stored == key })
		if i < 0 {
			problems.add(problems.spelling.root+"."+truncateKey(key), "is not a key of ssl_config; use "+allowedMCPSSLKeys())
			continue
		}
		field := mcpSSLFields[i]
		text, isString := block[key].(string)
		if !isString {
			problems.add(problems.spelling.path(field), "must be a string")
			continue
		}
		*field.ptr(ssl) = text
	}
	return ssl
}

func allowedMCPSSLKeys() string {
	keys := make([]string, len(mcpSSLFields))
	for i, f := range mcpSSLFields {
		keys[i] = f.stored
	}
	return strings.Join(keys, ", ")
}

func validateMCPSSLRequest(ssl *MCPSSLConfig) error {
	if ssl == nil {
		return nil
	}
	problems := &sslProblems{spelling: wireSSLSpelling}
	checkMCPSSLSettings(ssl, problems)
	return problems.err()
}

func refuseSSLConfigBeforeSpendingASession(ssl *MCPSSLConfig) error {
	if err := validateMCPSSLRequest(ssl); err != nil {
		return err
	}
	if _, err := buildMCPDiscoveryTLSConfig(ssl); err != nil {
		return &ValidationError{Message: err.Error(), Field: "sslConfig"}
	}
	return nil
}

func checkMCPSSLSettings(ssl *MCPSSLConfig, problems *sslProblems) {
	s := problems.spelling
	switch {
	case ssl.ClientCert != "" && ssl.ClientKey == "":
		problems.add(s.path(sslClientKey), fmt.Sprintf("must be set when %s is set", s.path(sslClientCert)))
	case ssl.ClientKey != "" && ssl.ClientCert == "":
		problems.add(s.path(sslClientCert), fmt.Sprintf("must be set when %s is set", s.path(sslClientKey)))
	}
	checkMCPSSLFiles(ssl, problems)
}

func checkMCPSSLFiles(ssl *MCPSSLConfig, problems *sslProblems) {
	s := problems.spelling
	var named []mcpSSLField
	for _, f := range mcpSSLFileFields {
		if *f.ptr(ssl) != "" {
			named = append(named, f)
		}
	}
	if len(named) == 0 {
		return
	}
	dir, err := mcpConnectorTLSDir()
	if err != nil {
		for _, f := range named {
			problems.add(s.path(f), "names a file, but "+err.Error())
		}
		return
	}
	for _, f := range named {
		rel, err := tlsPathInDir(dir, *f.ptr(ssl))
		if err == nil && isServerTLSFile(dir, rel) {
			err = errTLSPathServerTLSFile
		}
		if err != nil {
			problems.add(s.path(f), tlsPathProblem(dir, err))
		}
	}
}

var serverTLSFileEnvVars = []string{"SSL_CERTFILE", "SSL_KEYFILE"}

func isServerTLSFile(dir, rel string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	named, err := root.Stat(rel)
	if err != nil {
		return false
	}
	for _, envVar := range serverTLSFileEnvVars {
		served, err := os.Stat(os.Getenv(envVar))
		if err == nil && os.SameFile(named, served) {
			return true
		}
	}
	return false
}

var (
	errTLSDirUnset       = errors.New(mcpTLSDirEnvVar + " is not set on the platform")
	errTLSDirNotAbsolute = errors.New(mcpTLSDirEnvVar + " must be an absolute path")

	errTLSPathNotAbsolute   = errors.New("must be an absolute path")
	errTLSPathNotClean      = errors.New(`must be a clean path: no "." or ".." element, repeated slash or trailing slash`)
	errTLSPathOutside       = errors.New("must be a file inside the directory named by " + mcpTLSDirEnvVar)
	errTLSPathServerTLSFile = errors.New("must not be the platform's own TLS certificate or key")
)

func mcpConnectorTLSDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv(mcpTLSDirEnvVar))
	switch {
	case dir == "":
		return "", errTLSDirUnset
	case !filepath.IsAbs(dir):
		return "", errTLSDirNotAbsolute
	}
	return filepath.Clean(dir), nil
}

func tlsPathInDir(dir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errTLSPathNotAbsolute
	}
	if filepath.Clean(path) != path {
		return "", errTLSPathNotClean
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errTLSPathOutside
	}
	return rel, nil
}

func tlsPathProblem(dir string, err error) string {
	if errors.Is(err, errTLSPathOutside) {
		return "must be a file inside " + dir + " (" + mcpTLSDirEnvVar + ")"
	}
	return err.Error()
}

func readMCPTLSFile(path string) ([]byte, error) {
	dir, err := mcpConnectorTLSDir()
	if err != nil {
		return nil, err
	}
	rel, err := tlsPathInDir(dir, path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", mcpTLSDirEnvVar, err)
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(rel)
}
