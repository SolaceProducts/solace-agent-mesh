// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvVars_HTTPSProxyAndSSLCertFile_InLinuxContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("container e2e test; skipped under -short")
	}

	runtimePath, err := detectContainerRuntime(t)
	if err != nil {
		t.Skipf("no usable container runtime: %v", err)
	}

	var connectHits atomic.Int32
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen on all interfaces: %v", err)
	}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connectHits.Add(1)
		}
		http.Error(w, "no upstream", http.StatusBadGateway)
	}))
	proxy.Listener = ln
	proxy.StartTLS()
	defer proxy.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	proxyURLForContainer := fmt.Sprintf("https://example.com:%d", port)

	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "proxy-ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw})
	if err := os.WriteFile(certPath, pemBytes, 0o644); err != nil {
		t.Fatalf("write PEM: %v", err)
	}

	binPath := filepath.Join(tmp, "tlscheck")
	root := repoRoot(t)
	build := exec.Command("go", "build", "-o", binPath, "./pkg/harnesstools/tlscheck")
	build.Dir = root
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cross-compile tlscheck:\n%s", out)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	args := []string{
		"run", "--rm",
		"--add-host=example.com:host-gateway",
		"-v", binPath + ":/usr/local/bin/tlscheck:ro",
		"-v", certPath + ":/etc/ssl/certs/proxy-ca.pem:ro",
		"-e", "HTTPS_PROXY=" + proxyURLForContainer,
		"-e", "SSL_CERT_FILE=/etc/ssl/certs/proxy-ca.pem",
		"alpine:3.20",
		"/usr/local/bin/tlscheck",
	}
	cmd := exec.CommandContext(ctx, runtimePath, args...)
	out, runErr := cmd.CombinedOutput()
	t.Logf("container output:\n%s", out)

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
			t.Fatalf("SSL_CERT_FILE was not honored — TLS handshake to the proxy failed inside the container:\n%s", out)
		}
		t.Fatalf("container run failed: %v\n%s", runErr, out)
	}

	if got := connectHits.Load(); got == 0 {
		t.Fatalf("proxy received zero CONNECT requests — HTTPS_PROXY was not honored.\nContainer output:\n%s", out)
	}
}

func detectContainerRuntime(t *testing.T) (string, error) {
	t.Helper()
	for _, name := range []string{"docker", "podman"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		probe := exec.Command(p, "ps")
		if err := probe.Run(); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("neither docker nor podman is available and running")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
