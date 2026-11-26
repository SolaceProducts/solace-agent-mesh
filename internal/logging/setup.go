// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func SetupFromConfig(cfg config.Config) (*slog.Logger, func(), error) {
	resolved, externalPath, err := resolveLogConfig(cfg)
	if err != nil {
		return nil, noopCleanup, err
	}
	opts := parseLogOptions(resolved)

	stderrHandler := newHandler(os.Stderr, &slog.HandlerOptions{Level: opts.stderrLevel}, opts.format)
	stderrLogger := slog.New(stderrHandler)

	if externalPath != "" {
		fmt.Fprintf(os.Stderr,
			"INFO: logging configured from LOGGING_CONFIG_PATH (path=%s)\n",
			externalPath,
		)
	}

	writer, cleanup, err := openLogWriter(opts.writer)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"WARN: failed to open log file, using stderr only: path=%s error=%v\n",
			opts.writer.logFile, err,
		)
		return stderrLogger, noopCleanup, nil
	}
	if writer == nil {
		return stderrLogger, noopCleanup, nil
	}

	if opts.writer.maxSizeMB <= 0 {
		fmt.Fprintf(os.Stderr,
			"WARN: file logging enabled without rotation; file will grow unbounded — set log.max_size_mb to a positive value to enable rotation (path=%s)\n",
			opts.writer.logFile,
		)
	}

	fileHandler := newHandler(writer, &slog.HandlerOptions{Level: opts.fileLevel}, opts.format)
	return slog.New(NewMultiHandler(stderrHandler, fileHandler)), cleanup, nil
}

func AttachHandlers(base *slog.Logger, extras ...slog.Handler) *slog.Logger {
	if len(extras) == 0 || base == nil {
		return base
	}
	current := base.Handler()
	var merged []slog.Handler
	if mh, ok := current.(*MultiHandler); ok {
		merged = make([]slog.Handler, 0, len(mh.handlers)+len(extras))
		merged = append(merged, mh.handlers...)
	} else {
		merged = make([]slog.Handler, 0, 1+len(extras))
		merged = append(merged, current)
	}
	merged = append(merged, extras...)
	return slog.New(NewMultiHandler(merged...))
}

func SetupFromFlags(logFile string, level string) (*slog.Logger, func(), error) {
	lvl := ParseLogLevel(level)
	format := resolveFormat("")

	if logFile == "" {
		stderrHandler := newHandler(os.Stderr, &slog.HandlerOptions{
			Level: lvl,
		}, format)
		return slog.New(stderrHandler), noopCleanup, nil
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, noopCleanup, fmt.Errorf("open log file %s: %w", logFile, err)
	}

	fileHandler := newHandler(f, &slog.HandlerOptions{
		Level: lvl,
	}, format)
	cleanup := func() { _ = f.Close() }

	return slog.New(fileHandler), cleanup, nil
}

func resolveFormat(cfgFormat string) string {
	if envFormat := os.Getenv("LOG_FORMAT"); envFormat != "" {
		return strings.ToLower(strings.TrimSpace(envFormat))
	}
	if cfgFormat != "" {
		return strings.ToLower(strings.TrimSpace(cfgFormat))
	}
	return "json"
}

var secretAttrKeyPattern = regexp.MustCompile(`(?i:(?:^|_)(?:password|client_secret|api_key|token|credential))$` +
	`|[a-z0-9](?:Password|ClientSecret|ApiKey|APIKey|Token|Credential)$|^(?:apiKey|clientSecret)$`)

func MaskSecretAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		return a
	}
	if secretAttrKeyPattern.MatchString(a.Key) {
		a.Value = slog.StringValue("[REDACTED]")
	}
	return a
}

const (
	datadogMessageKey   = "message"
	datadogTimestampKey = "timestamp"
)

func NewHandler(w io.Writer, opts *slog.HandlerOptions, format string) slog.Handler {
	merged := &slog.HandlerOptions{}
	if opts != nil {
		*merged = *opts
	}
	existing := merged.ReplaceAttr
	if format == "json" {
		merged.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if existing != nil {
				a = existing(groups, a)
			}
			a = MaskSecretAttr(groups, a)
			if len(groups) == 0 {
				switch a.Key {
				case slog.MessageKey:
					a.Key = datadogMessageKey
				case slog.TimeKey:
					a.Key = datadogTimestampKey
				}
			}
			return a
		}
		return slog.NewJSONHandler(w, merged)
	}
	merged.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if existing != nil {
			a = existing(groups, a)
		}
		return MaskSecretAttr(groups, a)
	}
	return slog.NewTextHandler(w, merged)
}

func newHandler(w io.Writer, opts *slog.HandlerOptions, format string) slog.Handler {
	return NewHandler(w, opts, format)
}

func NewStderrLogger(level slog.Level) *slog.Logger {
	return slog.New(NewHandler(os.Stderr, &slog.HandlerOptions{Level: level}, bootstrapFormat()))
}

func bootstrapFormat() string {
	if envFormat := os.Getenv("LOG_FORMAT"); envFormat != "" {
		return strings.ToLower(strings.TrimSpace(envFormat))
	}
	return "text"
}

type logOptions struct {
	format      string
	stderrLevel slog.Level
	fileLevel   slog.Level
	writer      writerOptions
}

func resolveLogConfig(cfg config.Config) (config.Config, string, error) {
	path := os.Getenv("LOGGING_CONFIG_PATH")
	if path == "" {
		return cfg, "", nil
	}
	resolved, err := LoadExternalLogConfig(path)
	if err != nil {
		return nil, "", err
	}
	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		abs = path
	}
	return resolved, abs, nil
}

func parseLogOptions(cfg config.Config) logOptions {
	defaults := logOptions{
		format:      resolveFormat(""),
		stderrLevel: slog.LevelInfo,
		fileLevel:   slog.LevelInfo,
	}
	if cfg == nil || !cfg.Has("log") {
		return defaults
	}
	logCfg := cfg.GetSection("log")
	if logCfg == nil {
		fmt.Fprintf(os.Stderr,
			"WARN: 'log:' key present but not a YAML mapping; using default logging settings (expected a map of keys like format, stdout_log_level, log_file, max_size_mb)\n",
		)
		return defaults
	}

	if logSection, ok := cfg.GetRaw("log").(map[string]any); ok {
		warnUnrecognizedLogKeys(logSection)
	}

	opts := logOptions{
		format:      resolveFormat(logCfg.GetString("format")),
		stderrLevel: ParseLogLevel(logCfg.GetString("stdout_log_level")),
		fileLevel:   ParseLogLevel(logCfg.GetString("log_file_level")),
		writer: writerOptions{
			logFile:   logCfg.GetString("log_file"),
			maxSizeMB: logCfg.GetInt("max_size_mb"),
		},
	}

	if opts.writer.maxSizeMB > 0 {
		opts.writer.maxBackups = resolveMaxBackups(logCfg)
		opts.writer.maxAgeDays = logCfg.GetInt("max_age_days")
		opts.writer.compress = logCfg.GetBool("compress")
	}

	return opts
}

var recognizedLogKeys = []string{
	"compress",
	"format",
	"log_file",
	"log_file_level",
	"max_age_days",
	"max_backups",
	"max_size_mb",
	"stdout_log_level",
}

func warnUnrecognizedLogKeys(logSection map[string]any) {
	var unrecognized []string
	for key := range logSection {
		if !slices.Contains(recognizedLogKeys, key) {
			unrecognized = append(unrecognized, key)
		}
	}
	if len(unrecognized) == 0 {
		return
	}
	slices.Sort(unrecognized)
	fmt.Fprintf(os.Stderr,
		"WARN: unrecognized key(s) under 'log:': %s; ignored (expected one of: %s)\n",
		strings.Join(unrecognized, ", "), strings.Join(recognizedLogKeys, ", "))
}

func resolveMaxBackups(logCfg config.Config) int {
	const defaultBackups = 10
	raw := logCfg.GetRaw("max_backups")
	switch n := raw.(type) {
	case int:
		if n >= 0 {
			return n
		}
	case int64:
		if n >= 0 {
			return int(n)
		}
	}
	return defaultBackups
}
