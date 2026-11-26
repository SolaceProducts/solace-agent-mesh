// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const secretsDirName = "secrets"

func loadSecretsDir(logf func(format string, args ...any)) (loadedKeys []string, warnings []string) {
	home, err := settings.ResolveHomeDir()
	if err != nil {
		return nil, nil
	}
	dir := filepath.Join(home, secretsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".env") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)

	for _, name := range files {
		path := filepath.Join(dir, name)
		keys, warns := loadEnvFile(path)
		loadedKeys = append(loadedKeys, keys...)
		warnings = append(warnings, warns...)
	}

	if logf != nil && len(loadedKeys) > 0 {
		s := ""
		if len(loadedKeys) != 1 {
			s = "s"
		}
		logf("loaded %d secret%s from %s", len(loadedKeys), s, dir)
	}
	for _, w := range warnings {
		if logf != nil {
			logf("%s", w)
		}
	}
	return loadedKeys, warnings
}

func loadEnvFile(path string) (loadedKeys []string, warnings []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("warning: could not read %s: %s", path, err)}
	}
	defer func() { _ = f.Close() }()

	keys, warns := parseEnvFile(f, path)
	for _, kv := range keys {
		if _, present := os.LookupEnv(kv.key); present {
			continue
		}
		if err := os.Setenv(kv.key, kv.value); err != nil {
			warns = append(warns, fmt.Sprintf("warning: setenv %s: %s", kv.key, err))
			continue
		}
		loadedKeys = append(loadedKeys, kv.key)
	}
	return loadedKeys, warns
}

type envKV struct {
	key   string
	value string
}

func parseEnvFile(r io.Reader, path string) ([]envKV, []string) {
	var entries []envKV
	var warnings []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			warnings = append(warnings, fmt.Sprintf("warning: %s:%d: malformed line (no '='): %s", path, lineNum, line))
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := line[eq+1:]
		if !validEnvKey(key) {
			warnings = append(warnings, fmt.Sprintf("warning: %s:%d: invalid env key %q", path, lineNum, key))
			continue
		}
		entries = append(entries, envKV{key: key, value: value})
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("warning: %s: scan error: %s", path, err))
	}
	return entries, warnings
}

func validEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
