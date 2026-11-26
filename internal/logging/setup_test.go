// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("LOGGING_CONFIG_PATH")
	_ = os.Unsetenv("LOG_FORMAT")
	os.Exit(m.Run())
}

func TestSetupFromConfig_NilConfig(t *testing.T) {
	logger, cleanup, err := SetupFromConfig(nil)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test message")
}

func TestSetupFromConfig_NoLogSection(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"apps": []any{},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestSetupFromConfig_StdoutOnly(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"stdout_log_level": "WARNING",
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestSetupFromConfig_WithLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"stdout_log_level": "WARNING",
			"log_file_level":   "DEBUG",
			"log_file":         logPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("test file message")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected log file to contain output")
	}
}

func TestSetupFromConfig_InvalidFilePath(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file": "/nonexistent/path/to/log.file",
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error (should fall back gracefully): %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger even on file open failure")
	}
	logger.Info("fallback message")
}

func TestSetupFromConfig_InvalidFilePath_WarningVisibleAtErrorLevel(t *testing.T) {
	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":         "/nonexistent/path/to/log.file",
				"stdout_log_level": "ERROR",
			},
		})
		_, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cleanup()
	})

	if !strings.Contains(out, "failed to open log file") {
		t.Errorf("file-open failure warning must be visible at stdout_log_level=ERROR, got: %q", out)
	}
}

func TestSetupFromFlags_NoFile(t *testing.T) {
	logger, cleanup, err := SetupFromFlags("", "INFO")
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestSetupFromFlags_WithFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "flags.log")

	logger, cleanup, err := SetupFromFlags(logPath, "DEBUG")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Debug("flags test message")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected log file to contain output")
	}
}

func TestSetupFromConfig_JSONFormatViaConfig(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "json.log")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"format":         "json",
			"log_file_level": "DEBUG",
			"log_file":       logPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("json test", "key", "value")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON output (starting with '{'), got: %s", string(data[:min(80, len(data))]))
	}
}

func TestSetupFromConfig_JSONFormatViaEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "env-json.log")

	t.Setenv("LOG_FORMAT", "json")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file_level": "DEBUG",
			"log_file":       logPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("env json test")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON output via env var, got: %s", string(data[:min(80, len(data))]))
	}
}

func TestSetupFromConfig_EnvVarOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "override.log")

	t.Setenv("LOG_FORMAT", "json")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"format":         "text",
			"log_file_level": "DEBUG",
			"log_file":       logPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("override test")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
	if data[0] != '{' {
		t.Errorf("expected env var to override config format to JSON, got: %s", string(data[:min(80, len(data))]))
	}
}

func TestSetupFromConfig_DefaultIsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "default.log")

	t.Setenv("LOG_FORMAT", "")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file_level": "DEBUG",
			"log_file":       logPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("default format test")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON output by default (per resolveFormat fallback), got: %s", string(data[:min(80, len(data))]))
	}
}

func TestSetupFromFlags_JSONViaEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "flags-json.log")

	t.Setenv("LOG_FORMAT", "json")

	logger, cleanup, err := SetupFromFlags(logPath, "DEBUG")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("flags json test")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log file to contain output")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON output via env var in SetupFromFlags, got: %s", string(data[:min(80, len(data))]))
	}
}

func TestSetupFromConfig_RotationViaConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotated.log")

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file":    path,
			"max_size_mb": 1,
			"max_backups": 2,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer cleanup()

	line := string(make([]byte, 1024))
	for range 1200 {
		logger.Info(line)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	backups := 0
	for _, e := range entries {
		if e.Name() != "rotated.log" {
			backups++
		}
	}
	if backups == 0 {
		t.Fatalf("expected rotation to produce at least one backup; dir contents: %v", entries)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	defer func() { os.Stderr = orig }()
	os.Stderr = w

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		b := make([]byte, 1024)
		for {
			n, err := r.Read(b)
			if n > 0 {
				buf = append(buf, b[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- buf
	}()

	fn()

	_ = w.Close()
	return string(<-done)
}

func TestSetupFromConfig_UnboundedGrowthWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nowarn-sentinel.log")

	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":    path,
				"max_size_mb": 0,
			},
		})
		_, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if !strings.Contains(out, "grow unbounded") {
		t.Errorf("expected unbounded-growth warning on stderr, got: %q", out)
	}
	if !strings.Contains(out, "max_size_mb") {
		t.Errorf("expected warning to mention max_size_mb, got: %q", out)
	}
}

func TestSetupFromConfig_UnboundedGrowthWarning_IgnoresStdoutLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loud.log")

	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":         path,
				"stdout_log_level": "ERROR",
				"max_size_mb":      0,
			},
		})
		_, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if !strings.Contains(out, "grow unbounded") {
		t.Errorf("warning must be visible even at stdout_log_level=ERROR, got: %q", out)
	}
}

func TestSetupFromConfig_NoWarningWhenRotating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot.log")

	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":    path,
				"max_size_mb": 50,
			},
		})
		_, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if strings.Contains(out, "grow unbounded") {
		t.Errorf("did not expect unbounded-growth warning when rotating, got: %q", out)
	}
}

func TestSetupFromConfig_NoWarningWhenNoFile(t *testing.T) {
	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"stdout_log_level": "INFO",
			},
		})
		_, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if strings.Contains(out, "grow unbounded") {
		t.Errorf("did not expect unbounded-growth warning without log_file, got: %q", out)
	}
}

func TestSetupFromConfig_LoggingConfigPathTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	externalPath := filepath.Join(dir, "external.yaml")
	mainLogPath := filepath.Join(dir, "main.log")
	externalLogPath := filepath.Join(dir, "external.log")

	externalYAML := `
log:
  format: json
  log_file: ` + externalLogPath + `
  log_file_level: DEBUG
`
	if err := os.WriteFile(externalPath, []byte(externalYAML), 0644); err != nil {
		t.Fatalf("write external: %v", err)
	}
	t.Setenv("LOGGING_CONFIG_PATH", externalPath)

	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"format":   "text",
			"log_file": mainLogPath,
		},
	})

	logger, cleanup, err := SetupFromConfig(cfg)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	logger.Info("precedence test")
	cleanup()

	data, err := os.ReadFile(externalLogPath)
	if err != nil {
		t.Fatalf("read external log: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("external log file is empty; external config did not take effect")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON (external format), got: %s", string(data[:min(80, len(data))]))
	}
	if _, err := os.Stat(mainLogPath); err == nil {
		data2, _ := os.ReadFile(mainLogPath)
		if len(data2) > 0 {
			t.Errorf("main config's log file should not have been used, contents: %s", string(data2))
		}
	}
}

func TestSetupFromConfig_LoggingConfigPathMissingFails(t *testing.T) {
	t.Setenv("LOGGING_CONFIG_PATH", "/tmp/does-not-exist-xyz-1234.yaml")
	cfg := config.FromMap(map[string]any{
		"log": map[string]any{"format": "text"},
	})
	_, _, err := SetupFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing LOGGING_CONFIG_PATH file")
	}
}

func TestSetupFromConfig_LoggingConfigPathLogFormatEnvStillWins(t *testing.T) {
	dir := t.TempDir()
	externalPath := filepath.Join(dir, "fmt.yaml")
	logPath := filepath.Join(dir, "fmtwin.log")

	if err := os.WriteFile(externalPath, []byte(`
log:
  format: text
  log_file: `+logPath+`
  log_file_level: DEBUG
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGGING_CONFIG_PATH", externalPath)
	t.Setenv("LOG_FORMAT", "json")

	logger, cleanup, err := SetupFromConfig(config.FromMap(nil))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	logger.Info("env wins test")
	cleanup()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty; LOG_FORMAT did not take effect")
	}
	if data[0] != '{' {
		t.Errorf("LOG_FORMAT=json should override external format:text, got: %s",
			string(data[:min(80, len(data))]))
	}
}

func TestSetupFromConfig_LoggingConfigPathLogsInfo(t *testing.T) {
	dir := t.TempDir()
	externalPath := filepath.Join(dir, "info.yaml")
	if err := os.WriteFile(externalPath, []byte("log:\n  format: text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGGING_CONFIG_PATH", externalPath)

	out := captureStderr(t, func() {
		_, cleanup, err := SetupFromConfig(config.FromMap(nil))
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if !strings.Contains(out, "logging configured from LOGGING_CONFIG_PATH") {
		t.Errorf("expected INFO line mentioning LOGGING_CONFIG_PATH on stderr, got: %q", out)
	}
}

func TestSetupFromConfig_LoggingConfigPathLogsInfo_IgnoresStdoutLevel(t *testing.T) {
	dir := t.TempDir()
	externalPath := filepath.Join(dir, "warn.yaml")
	if err := os.WriteFile(externalPath, []byte("log:\n  format: text\n  stdout_log_level: ERROR\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGGING_CONFIG_PATH", externalPath)

	out := captureStderr(t, func() {
		_, cleanup, err := SetupFromConfig(config.FromMap(nil))
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		cleanup()
	})

	if !strings.Contains(out, "logging configured from LOGGING_CONFIG_PATH") {
		t.Errorf("confirmation must be visible even at stdout_log_level=ERROR, got: %q", out)
	}
}

func TestParseLogOptions_MaxBackupsZeroIsUnlimited(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file":    "/tmp/unused.log",
			"max_size_mb": 50,
			"max_backups": 0,
		},
	})
	opts := parseLogOptions(cfg)
	if opts.writer.maxBackups != 0 {
		t.Errorf("explicit max_backups: 0 should pass through (lumberjack 'unlimited'), got %d", opts.writer.maxBackups)
	}
}

func TestParseLogOptions_MaxBackupsAbsentGetsDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"log": map[string]any{
			"log_file":    "/tmp/unused.log",
			"max_size_mb": 50,
		},
	})
	opts := parseLogOptions(cfg)
	if opts.writer.maxBackups != 10 {
		t.Errorf("absent max_backups should default to 10, got %d", opts.writer.maxBackups)
	}
}

func TestParseLogOptions_MaxBackupsBogusValuesGetDefault(t *testing.T) {
	cases := map[string]any{
		"null":           nil,
		"negative int":   -5,
		"negative int64": int64(-1),
		"non-numeric":    "abc",
		"float":          3.14,
		"empty string":   "",
		"list":           []any{1, 2, 3},
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.FromMap(map[string]any{
				"log": map[string]any{
					"log_file":    "/tmp/unused.log",
					"max_size_mb": 50,
					"max_backups": val,
				},
			})
			opts := parseLogOptions(cfg)
			if opts.writer.maxBackups != 10 {
				t.Errorf("bogus max_backups (%v) should fall back to default 10, got %d",
					val, opts.writer.maxBackups)
			}
		})
	}
}

func TestParseLogOptions_MaxBackupsExplicitPositive(t *testing.T) {
	for _, want := range []int{1, 5, 20, 100} {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":    "/tmp/unused.log",
				"max_size_mb": 50,
				"max_backups": want,
			},
		})
		opts := parseLogOptions(cfg)
		if opts.writer.maxBackups != want {
			t.Errorf("max_backups: %d should pass through, got %d", want, opts.writer.maxBackups)
		}
	}
}

func TestSetupFromConfig_LogKeyNullDoesNotPanic(t *testing.T) {
	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": nil,
		})
		logger, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if logger == nil {
			t.Fatal("expected fallback logger, got nil")
		}
		logger.Info("still works")
		cleanup()
	})

	if !strings.Contains(out, "not a YAML mapping") {
		t.Errorf("expected type-mismatch warning on stderr, got: %q", out)
	}
}

func TestSetupFromConfig_LogKeyWrongTypeDoesNotPanic(t *testing.T) {
	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": []any{"oops"},
		})
		logger, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if logger == nil {
			t.Fatal("expected fallback logger, got nil")
		}
		cleanup()
	})

	if !strings.Contains(out, "not a YAML mapping") {
		t.Errorf("expected type-mismatch warning on stderr, got: %q", out)
	}
}

func TestSetupFromConfig_NegativeMaxSizeMBIsPlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neg.log")

	out := captureStderr(t, func() {
		cfg := config.FromMap(map[string]any{
			"log": map[string]any{
				"log_file":    path,
				"max_size_mb": -1,
			},
		})
		logger, cleanup, err := SetupFromConfig(cfg)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		logger.Info("should land in file")
		cleanup()
	})

	if !strings.Contains(out, "grow unbounded") {
		t.Errorf("expected unbounded-growth warning for max_size_mb=-1, got: %q", out)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected log output in file, got empty file (lumberjack path took the record?)")
	}
}

func TestResolveFormat(t *testing.T) {
	tests := []struct {
		name      string
		cfgFormat string
		envFormat string
		want      string
	}{
		{"default", "", "", "json"},
		{"config json", "json", "", "json"},
		{"config text", "text", "", "text"},
		{"env overrides config", "text", "json", "json"},
		{"env alone", "", "json", "json"},
		{"env uppercase JSON is not text", "", "JSON", "json"},
		{"env mixed case", "", "Json", "json"},
		{"config uppercase", "TEXT", "", "text"},
		{"config mixed case", "Json", "", "json"},
		{"env trailing space", "", "json ", "json"},
		{"config leading space", " text", "", "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_FORMAT", tt.envFormat)
			got := resolveFormat(tt.cfgFormat)
			if got != tt.want {
				t.Errorf("resolveFormat(%q) with LOG_FORMAT=%q = %q, want %q",
					tt.cfgFormat, tt.envFormat, got, tt.want)
			}
		})
	}
}

func TestNewHandler_JSONDatadogKeys(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "json")
	r := slog.NewRecord(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), slog.LevelInfo, "hello world", 0)
	if err := h.Handle(t.Context(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("json.Unmarshal: %v (raw: %s)", err, buf.String())
	}

	if _, ok := m["message"]; !ok {
		t.Errorf("expected top-level \"message\" key, got: %v", m)
	}
	if _, ok := m["timestamp"]; !ok {
		t.Errorf("expected top-level \"timestamp\" key, got: %v", m)
	}
	if _, ok := m["msg"]; ok {
		t.Errorf("unexpected top-level \"msg\" key (should have been renamed to \"message\")")
	}
	if _, ok := m["time"]; ok {
		t.Errorf("unexpected top-level \"time\" key (should have been renamed to \"timestamp\")")
	}
	if m["message"] != "hello world" {
		t.Errorf("message value: got %q, want %q", m["message"], "hello world")
	}
}

func TestNewHandler_TextDoesNotRenameKeys(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "text")
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello text", 0)
	if err := h.Handle(t.Context(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "msg=") {
		t.Errorf("expected \"msg=\" in text output, got: %s", out)
	}
	if !strings.Contains(out, "time=") {
		t.Errorf("expected \"time=\" in text output, got: %s", out)
	}
}

func TestNewHandler_JSONNestedGroupMsgPreserved(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "json")
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "outer", 0)
	r.AddAttrs(slog.Group("req", slog.String("msg", "inner value")))
	if err := h.Handle(t.Context(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("json.Unmarshal: %v (raw: %s)", err, buf.String())
	}

	req, ok := m["req"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested \"req\" group, got: %v", m)
	}
	if req["msg"] != "inner value" {
		t.Errorf("nested msg key modified or missing: req=%v", req)
	}
}

func TestParseLogOptions_EmptyLogBlockMatchesAbsentBlock(t *testing.T) {
	absent := parseLogOptions(config.FromMap(map[string]any{}))
	empty := parseLogOptions(config.FromMap(map[string]any{
		"log": map[string]any{"log_file": "/tmp/unused.log"},
	}))

	if absent.stderrLevel != empty.stderrLevel {
		t.Errorf("stderr level: absent block = %v, empty block = %v — the two must agree",
			absent.stderrLevel, empty.stderrLevel)
	}
	if absent.fileLevel != empty.fileLevel {
		t.Errorf("file level: absent block = %v, empty block = %v — the two must agree",
			absent.fileLevel, empty.fileLevel)
	}
	if empty.fileLevel == slog.LevelDebug {
		t.Error("an unconfigured file sink resolved to DEBUG — content-bearing records would be written to disk without anyone asking for them")
	}
	if empty.stderrLevel != slog.LevelInfo || empty.fileLevel != slog.LevelInfo {
		t.Errorf("unconfigured levels = stderr %v / file %v, want INFO for both",
			empty.stderrLevel, empty.fileLevel)
	}
}

func TestParseLogOptions_ExplicitDebugStillHonoured(t *testing.T) {
	opts := parseLogOptions(config.FromMap(map[string]any{
		"log": map[string]any{
			"stdout_log_level": "DEBUG",
			"log_file_level":   "DEBUG",
			"log_file":         "/tmp/unused.log",
		},
	}))
	if opts.stderrLevel != slog.LevelDebug || opts.fileLevel != slog.LevelDebug {
		t.Errorf("explicit DEBUG not honoured: stderr %v / file %v", opts.stderrLevel, opts.fileLevel)
	}
}

func TestNewStderrLogger_MasksAndDefaultsToText(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = saved })

	NewStderrLogger(slog.LevelDebug).Info("startup", "api_key", "sk-live-BOOTSTRAP-CANARY", "plain", "kept")
	_ = w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(out)

	if strings.Contains(got, "sk-live-BOOTSTRAP-CANARY") {
		t.Errorf("the bootstrap logger emitted a secret-keyed value verbatim: %q", got)
	}
	if !strings.Contains(got, "api_key=[REDACTED]") {
		t.Errorf("api_key was not masked: %q", got)
	}
	if !strings.Contains(got, "plain=kept") {
		t.Errorf("a non-secret attribute was lost: %q", got)
	}
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Errorf("bootstrap output is JSON with no LOG_FORMAT set: %q", got)
	}
}

func TestBootstrapFormat(t *testing.T) {
	cases := []struct {
		name string
		env  string
		set  bool
		want string
	}{
		{"unset defaults to text", "", false, "text"},
		{"explicit json", "JSON", true, "json"},
		{"padded and mixed case", "  Text  ", true, "text"},
		{"empty value is not an override", "", true, "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("LOG_FORMAT", tc.env)
			} else {
				t.Setenv("LOG_FORMAT", "")
			}
			if got := bootstrapFormat(); got != tc.want {
				t.Errorf("bootstrapFormat() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewHandler_CallerReplaceAttrCannotDisableMasking(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			callerRan := false
			h := NewHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					callerRan = true
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			}, format)

			slog.New(h).Info("hello", "api_key", "sk-live-CALLER-CANARY")

			if !callerRan {
				t.Fatal("the caller's ReplaceAttr never ran, so this asserts nothing about chaining")
			}
			if strings.Contains(buf.String(), "sk-live-CALLER-CANARY") {
				t.Errorf("a caller-supplied ReplaceAttr suppressed masking: %q", buf.String())
			}
			if !strings.Contains(buf.String(), "[REDACTED]") {
				t.Errorf("nothing was masked: %q", buf.String())
			}
		})
	}
}

func TestParseLogOptions_WarnsOnUnrecognizedLogKeys(t *testing.T) {
	out := captureStderr(t, func() {
		parseLogOptions(config.FromMap(map[string]any{
			"log": map[string]any{
				"level":            "debug",
				"stdout_log_level": "INFO",
			},
		}))
	})

	if !strings.Contains(out, "under 'log:': level;") {
		t.Errorf("warning must name level as the unrecognized key, got: %q", out)
	}
	if !strings.Contains(out, "expected one of: compress, format") {
		t.Errorf("warning must list the recognized keys so the fix is obvious, got: %q", out)
	}
}

func TestParseLogOptions_SilentWhenEveryKeyIsRecognized(t *testing.T) {
	out := captureStderr(t, func() {
		parseLogOptions(config.FromMap(map[string]any{
			"log": map[string]any{
				"format":           "json",
				"stdout_log_level": "DEBUG",
				"log_file":         "/tmp/unused.log",
				"max_size_mb":      10,
				"max_backups":      3,
				"max_age_days":     7,
				"compress":         true,
			},
		}))
	})

	if out != "" {
		t.Errorf("a fully recognized log block must warn about nothing, got: %q", out)
	}
}

func TestParseLogOptions_WarnsOnUnrecognizedKeysFromExternalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logging.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600); err != nil {
		t.Fatalf("write external config: %v", err)
	}
	t.Setenv("LOGGING_CONFIG_PATH", path)

	out := captureStderr(t, func() {
		resolved, _, err := resolveLogConfig(config.FromMap(nil))
		if err != nil {
			t.Fatalf("resolveLogConfig: %v", err)
		}
		parseLogOptions(resolved)
	})

	if !strings.Contains(out, "under 'log:': level;") {
		t.Errorf("an invented key in LOGGING_CONFIG_PATH must warn too, got: %q", out)
	}
}

func TestMaskSecretAttr_KeyNames(t *testing.T) {
	masked := []string{
		"password", "client_secret", "api_key", "token", "credential",
		"db_password", "API_KEY", "Token",
		"accessToken", "refreshToken", "authToken", "apiKey", "clientSecret",
		"realClientSecret", "openaiAPIKey", "dbPassword", "awsCredential",
	}
	kept := []string{
		"token_url", "credential_key", "tokenCount", "maxTokens", "promptTokens",
		"tokenizer", "apiKeyID", "passwordSet", "userID", "url",
	}
	for _, k := range masked {
		if got := MaskSecretAttr(nil, slog.String(k, "v")).Value.String(); got != "[REDACTED]" {
			t.Errorf("key %q: got %q, want [REDACTED]", k, got)
		}
	}
	for _, k := range kept {
		if got := MaskSecretAttr(nil, slog.String(k, "v")).Value.String(); got != "v" {
			t.Errorf("key %q: got %q, want it unmasked", k, got)
		}
	}
}
