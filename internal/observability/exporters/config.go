// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

const (
	ProtocolHTTP = "http"
	ProtocolGRPC = "grpc"
)

const (
	CompressionNone    = "none"
	CompressionGzip    = "gzip"
	CompressionDeflate = "deflate"
)

const TypeOTLP = "otlp"

const (
	DefaultLogLevel    = "INFO"
	DefaultTimeoutSec  = 10
	DefaultCompression = CompressionNone
)

var (
	requiredFields = map[string]struct{}{
		"type":     {},
		"endpoint": {},
		"protocol": {},
	}
	optionalFields = map[string]struct{}{
		"metrics":          {},
		"logs":             {},
		"log_level":        {},
		"headers":          {},
		"timeout":          {},
		"compression":      {},
		"insecure":         {},
		"certificate_file": {},
	}
	validProtocols = map[string]struct{}{
		ProtocolHTTP: {},
		ProtocolGRPC: {},
	}
	validCompressions = map[string]struct{}{
		CompressionNone:    {},
		CompressionGzip:    {},
		CompressionDeflate: {},
	}
	validLogLevels = map[string]struct{}{
		"DEBUG":    {},
		"INFO":     {},
		"WARNING":  {},
		"ERROR":    {},
		"CRITICAL": {},
	}
)

type ExporterConfig struct {
	Type            string
	Endpoint        string
	Protocol        string
	Metrics         bool
	Logs            bool
	LogLevel        string
	Headers         map[string]string
	Timeout         float64
	Compression     string
	Insecure        bool
	CertificateFile string
}

func ParseExporters(raw map[string]any, logger *slog.Logger) []ExporterConfig {
	if logger == nil {
		logger = slog.Default()
	}
	mgmt, ok := raw["management_server"].(map[string]any)
	if !ok {
		return nil
	}
	rawExporters, ok := mgmt["exporters"]
	if !ok {
		return nil
	}
	list, ok := rawExporters.([]any)
	if !ok {
		logger.Error("management_server.exporters must be a list",
			"got", fmt.Sprintf("%T", rawExporters))
		return nil
	}

	out := make([]ExporterConfig, 0, len(list))
	for idx, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			logger.Error("exporter entry must be a map",
				"index", idx, "got", fmt.Sprintf("%T", entry))
			continue
		}
		cfg, err := ValidateExporterConfig(m, logger)
		if err != nil {
			logger.Error("invalid exporter config",
				"index", idx,
				"endpoint", stringOr(m["endpoint"], "unknown"),
				"error", err)
			continue
		}
		out = append(out, cfg)
	}
	return out
}

func ValidateExporterConfig(raw map[string]any, logger *slog.Logger) (ExporterConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var cfg ExporterConfig

	if missing := missingRequired(raw); len(missing) > 0 {
		return cfg, fmt.Errorf("missing required fields: %v", missing)
	}

	t, ok := raw["type"].(string)
	if !ok || t != TypeOTLP {
		return cfg, fmt.Errorf("unsupported exporter type: %v. Only %q is supported", raw["type"], TypeOTLP)
	}
	cfg.Type = t

	endpoint, ok := raw["endpoint"].(string)
	if !ok || strings.TrimSpace(endpoint) == "" {
		return cfg, fmt.Errorf("'endpoint' must be a non-empty string")
	}
	cfg.Endpoint = endpoint

	protocol, ok := raw["protocol"].(string)
	if !ok {
		return cfg, fmt.Errorf("'protocol' must be a string")
	}
	if _, ok := validProtocols[protocol]; !ok {
		return cfg, fmt.Errorf("'protocol' must be one of %v, got: %s", sortedKeys(validProtocols), protocol)
	}
	cfg.Protocol = protocol

	if v, present := raw["metrics"]; present {
		b, ok := v.(bool)
		if !ok {
			return cfg, fmt.Errorf("'metrics' must be a boolean, got: %T", v)
		}
		cfg.Metrics = b
	}
	if v, present := raw["logs"]; present {
		b, ok := v.(bool)
		if !ok {
			return cfg, fmt.Errorf("'logs' must be a boolean, got: %T", v)
		}
		cfg.Logs = b
	}
	if !cfg.Metrics && !cfg.Logs {
		logger.Warn("exporter configured with both metrics and logs disabled — will be a no-op",
			"endpoint", cfg.Endpoint)
	}

	cfg.LogLevel = DefaultLogLevel
	if v, present := raw["log_level"]; present {
		s, ok := v.(string)
		if !ok {
			return cfg, fmt.Errorf("'log_level' must be a string, got: %T", v)
		}
		upper := strings.ToUpper(s)
		if _, ok := validLogLevels[upper]; !ok {
			return cfg, fmt.Errorf("'log_level' must be one of %v, got: %s", sortedKeys(validLogLevels), s)
		}
		cfg.LogLevel = upper
	}

	if v, present := raw["headers"]; present {
		m, ok := v.(map[string]any)
		if !ok {
			return cfg, fmt.Errorf("'headers' must be a map, got: %T", v)
		}
		cfg.Headers = make(map[string]string, len(m))
		for k, val := range m {
			vs, ok := val.(string)
			if !ok {
				return cfg, fmt.Errorf("header value for %q must be string, got: %T", k, val)
			}
			cfg.Headers[k] = vs
		}
	}

	cfg.Timeout = DefaultTimeoutSec
	if v, present := raw["timeout"]; present {
		f, ok := toFloat(v)
		if !ok || f <= 0 {
			return cfg, fmt.Errorf("'timeout' must be a positive number, got: %v", v)
		}
		cfg.Timeout = f
	}

	cfg.Compression = DefaultCompression
	if v, present := raw["compression"]; present {
		s, ok := v.(string)
		if !ok {
			return cfg, fmt.Errorf("'compression' must be a string, got: %T", v)
		}
		if _, ok := validCompressions[s]; !ok {
			return cfg, fmt.Errorf("'compression' must be one of %v, got: %s", sortedKeys(validCompressions), s)
		}
		cfg.Compression = s
	}

	if v, present := raw["insecure"]; present {
		b, ok := v.(bool)
		if !ok {
			return cfg, fmt.Errorf("'insecure' must be a boolean, got: %T", v)
		}
		cfg.Insecure = b
	}

	if v, present := raw["certificate_file"]; present {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return cfg, fmt.Errorf("'certificate_file' must be a non-empty string")
		}
		cfg.CertificateFile = s
	}

	if cfg.Insecure && cfg.CertificateFile != "" {
		return cfg, fmt.Errorf("'insecure: true' and 'certificate_file' are mutually exclusive")
	}

	if unknown := unknownFields(raw); len(unknown) > 0 {
		logger.Warn("unknown exporter config fields will be ignored", "fields", unknown)
	}

	return cfg, nil
}

func missingRequired(raw map[string]any) []string {
	var missing []string
	for k := range requiredFields {
		if _, ok := raw[k]; !ok {
			missing = append(missing, k)
		}
	}
	slices.Sort(missing)
	return missing
}

func unknownFields(raw map[string]any) []string {
	var unknown []string
	for k := range raw {
		if _, ok := requiredFields[k]; ok {
			continue
		}
		if _, ok := optionalFields[k]; ok {
			continue
		}
		unknown = append(unknown, k)
	}
	slices.Sort(unknown)
	return unknown
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}
