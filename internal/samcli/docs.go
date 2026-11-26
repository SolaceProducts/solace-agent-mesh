// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/docsembed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/docsrender"
)

func newDocsCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Open the Agent Mesh documentation in your browser",
		Long: `Starts a local HTTP server to browse the Agent Mesh documentation.

The documentation is rendered natively from its markdown source and served
offline — no internet connection is required (except for diagrams, which use a
CDN-hosted renderer in this prototype). Opens the docs in your default browser.

Examples:
  sam docs
  sam docs -p 9090`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serveDocs(cmd, port)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 8585, "Port to run the documentation server on")
	return cmd
}

func serveDocs(cmd *cobra.Command, port int) error {
	if !docsembed.Available() {
		return fmt.Errorf("documentation is not included in this build. " +
			"Rebuild the CLI with the embeddocs build tag (e.g. `go build -tags embeddocs ./cmd/sam`) " +
			"or download a release binary that bundles the docs")
	}

	srv, err := docsrender.NewServer(docsembed.FS())
	if err != nil {
		return fmt.Errorf("initialize documentation server: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d%s", port, srv.IntroRoute())
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Serving documentation at %s\n", url)
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Press Ctrl+C to stop.")

	openBrowser(url)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case <-ctx.Done():
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\nShutting down documentation server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}
