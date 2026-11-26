// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type MCPServerHarness struct {
	sseProcess  *exec.Cmd
	httpProcess *exec.Cmd
	ssePort     int
	httpPort    int
	serverPath  string
	pythonPath  string
}

func NewMCPServerHarness() (*MCPServerHarness, error) {
	pythonPath, err := findPython()
	if err != nil {
		return nil, fmt.Errorf("mcp harness: %w", err)
	}

	serverPath, err := findMCPServerScript()
	if err != nil {
		return nil, fmt.Errorf("mcp harness: %w", err)
	}

	return &MCPServerHarness{
		pythonPath: pythonPath,
		serverPath: serverPath,
	}, nil
}

func (h *MCPServerHarness) Start() error {
	ssePort, err := findFreePort()
	if err != nil {
		return fmt.Errorf("find free port for SSE: %w", err)
	}
	h.ssePort = ssePort

	h.sseProcess = exec.Command(h.pythonPath, h.serverPath, "--transport", "sse", "--port", fmt.Sprintf("%d", ssePort))
	runInOwnProcessGroup(h.sseProcess)
	h.sseProcess.Stdout = os.Stdout
	h.sseProcess.Stderr = os.Stderr
	if startErr := h.sseProcess.Start(); startErr != nil {
		return fmt.Errorf("start SSE server: %w", startErr)
	}

	httpPort, err := findFreePort()
	if err != nil {
		h.Stop()
		return fmt.Errorf("find free port for HTTP: %w", err)
	}
	h.httpPort = httpPort

	h.httpProcess = exec.Command(h.pythonPath, h.serverPath, "--transport", "http", "--port", fmt.Sprintf("%d", httpPort))
	runInOwnProcessGroup(h.httpProcess)
	h.httpProcess.Stdout = os.Stdout
	h.httpProcess.Stderr = os.Stderr
	if err := h.httpProcess.Start(); err != nil {
		h.Stop()
		return fmt.Errorf("start HTTP server: %w", err)
	}

	if err := waitForHealth(fmt.Sprintf("http://127.0.0.1:%d/health", httpPort), 10*time.Second); err != nil {
		h.Stop()
		return fmt.Errorf("HTTP server not ready: %w", err)
	}

	if err := waitForHealth(fmt.Sprintf("http://127.0.0.1:%d/health", ssePort), 10*time.Second); err != nil {
		h.Stop()
		return fmt.Errorf("SSE server not ready: %w", err)
	}

	return nil
}

func (h *MCPServerHarness) Stop() {
	killProcessGroup(h.sseProcess)
	killProcessGroup(h.httpProcess)
}

func (h *MCPServerHarness) StdioConnectionParams() map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": h.pythonPath,
		"args":    []any{h.serverPath, "--transport", "stdio"},
	}
}

func (h *MCPServerHarness) SSEConnectionParams() map[string]any {
	return map[string]any{
		"type": "sse",
		"url":  fmt.Sprintf("http://127.0.0.1:%d/sse", h.ssePort),
	}
}

func (h *MCPServerHarness) StreamableHTTPConnectionParams() map[string]any {
	return map[string]any{
		"type": "streamable-http",
		"url":  fmt.Sprintf("http://127.0.0.1:%d/mcp", h.httpPort),
	}
}

func (h *MCPServerHarness) AllConnectionParams() map[string]map[string]any {
	return map[string]map[string]any{
		"stdio":           h.StdioConnectionParams(),
		"sse":             h.SSEConnectionParams(),
		"streamable_http": h.StreamableHTTPConnectionParams(),
	}
}

func findPython() (string, error) {
	if venvPython := findVenvPython(); venvPython != "" {
		return venvPython, nil
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python3 not found in PATH")
}

func findVenvPython() string {
	candidates := []string{findModuleRoot(), findRepoRoot()}
	for _, root := range candidates {
		if root == "" {
			continue
		}
		venvPython := filepath.Join(root, ".venv", "bin", "python3")
		if runtime.GOOS == "windows" {
			venvPython = filepath.Join(root, ".venv", "Scripts", "python.exe")
		}
		if _, err := os.Stat(venvPython); err == nil {
			return venvPython
		}
	}
	return ""
}

func findMCPServerScript() (string, error) {
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		return "", fmt.Errorf("could not find repo root")
	}
	serverPath := filepath.Join(repoRoot, "tests", "sam-test-infrastructure", "src", "sam_test_infrastructure", "mcp_server", "server.py")
	if _, err := os.Stat(serverPath); err != nil {
		return "", fmt.Errorf("MCP server script not found at %s", serverPath)
	}
	return serverPath, nil
}

func findRepoRoot() string {
	if dir := os.Getenv("SAM_PYTHON_REPO_DIR"); dir != "" {
		return dir
	}

	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	cur := dir
	for {
		if _, err := os.Stat(filepath.Join(cur, "tests", "sam-test-infrastructure")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	modRoot := findModuleRoot()
	sibling := filepath.Join(filepath.Dir(modRoot), "solace-agent-mesh")
	if _, err := os.Stat(filepath.Join(sibling, "tests", "sam-test-infrastructure")); err == nil {
		return sibling
	}

	return ""
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func waitForHealth(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("health check at %s timed out after %v", url, timeout)
}
