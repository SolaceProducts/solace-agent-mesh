// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

//go:embed Dockerfile.str
var dockerfileContent string

const LabelComponent = "sam.component"

const LabelVersion = "sam.version"

type BuildOptions struct {
	STRBinary string

	Tag string

	Version string

	Progress io.Writer
}

func BuildSTRImage(ctx context.Context, rt Runtime, opts BuildOptions) error {
	if opts.Progress == nil {
		opts.Progress = os.Stdout
	}

	tag := opts.Tag
	if tag == "" {
		tag = DefaultImageTag(opts.Version)
	}

	localPath, cleanup, err := ResolveSTRBinary(opts.STRBinary)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	expectedArch := HostContainerArch()
	if valErr := ValidateBinary(localPath, expectedArch); valErr != nil {
		return valErr
	}

	contextDir, err := os.MkdirTemp("", "sam-str-build-*")
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	defer func() { _ = os.RemoveAll(contextDir) }()

	dfPath := filepath.Join(contextDir, "Dockerfile")
	if writeErr := os.WriteFile(dfPath, []byte(dockerfileContent), 0o644); writeErr != nil {
		return fmt.Errorf("write Dockerfile: %w", writeErr)
	}

	binaryData, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read sam-str binary: %w", err)
	}
	binaryDest := filepath.Join(contextDir, "sam-str")
	if err := os.WriteFile(binaryDest, binaryData, 0o755); err != nil {
		return fmt.Errorf("write sam-str to build context: %w", err)
	}

	_, _ = fmt.Fprintf(opts.Progress, "Building STR image %s using %s...\n", tag, rt.Type())
	if err := rt.Build(ctx, contextDir, tag, opts.Progress); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(opts.Progress, "\nSTR image built: %s\n", tag)
	return nil
}

func ResolveSTRBinary(strBinary string) (localPath string, cleanup func(), err error) {
	if strBinary != "" {
		return resolvePathOrURL(strBinary)
	}

	if cfgBinary := readConfigSTRBinary(); cfgBinary != "" {
		return resolvePathOrURL(cfgBinary)
	}

	return "", nil, fmt.Errorf("no sam-str binary specified; set it in <SAM home>/config.yaml under container.str_binary")
}

func DefaultImageTag(version string) string {
	if version == "" || version == "dev" {
		return "sam-str:dev"
	}
	return "sam-str:" + version
}

func resolvePathOrURL(source string) (string, func(), error) {
	if isURL(source) {
		return downloadBinary(source)
	}
	if _, err := os.Stat(source); err != nil {
		return "", nil, fmt.Errorf("sam-str binary not found: %w", err)
	}
	return source, nil, nil
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func downloadBinary(url string) (string, func(), error) {
	resp, err := http.Get(url) //nolint:gosec // URL is user-provided
	if err != nil {
		return "", nil, fmt.Errorf("download sam-str binary from %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download sam-str binary from %s: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "sam-str-download-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file for download: %w", err)
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("download sam-str binary: %w", err)
	}
	_ = tmp.Close()

	cleanup := func() { _ = os.Remove(tmp.Name()) }
	return tmp.Name(), cleanup, nil
}

type samConfig struct {
	Container struct {
		STRBinary string `yaml:"str_binary"`
	} `yaml:"container"`
}

func readConfigSTRBinary() string {
	home, err := settings.ResolveHomeDir()
	if err != nil {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return ""
	}

	var cfg samConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}

	return cfg.Container.STRBinary
}
