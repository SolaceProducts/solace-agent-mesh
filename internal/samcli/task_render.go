// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/gateway"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/render"
)

const renderEventsFile = "events.jsonl"

var errRenderProblems = errors.New("rendering problems found")

const artifactsDir = "artifacts"

func newTaskRenderCmd() *cobra.Command {
	var (
		url          string
		targetName   string
		manifestPath string
		insecure     bool
		sessionID    string
		asJSON       bool
		check        bool
	)
	cmd := &cobra.Command{
		Use:   "render [OUTPUT_DIR | EVENTS_FILE]",
		Short: "Show the chat bubbles the web UI draws for a task and check them for rendering problems",
		Long: `Rebuild the chat bubbles the web UI shows for a task and check them for
rendering problems: a table shown twice, SAM's internal notes or raw embed and
template markup reaching the user, tables that did not parse, unclosed code
fences, failed artifact cards and empty answers.

The input is the output directory (or its events.jsonl) that
` + "`sam task send`" + ` writes, or with --session-id the bubbles the web UI saved
for a session someone ran in the browser.`,
		Example: `  sam task render /tmp/sam-task-<task id>
  sam task render --session-id <session id> --check
  sam task send "Show the WH-EAST stock" --render-check`,
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if sessionID != "" {
				if len(args) > 0 {
					return errors.New("pass either an output directory or --session-id, not both")
				}
				bubbles, artifacts, err := fetchSessionBubbles(cmd.Context(), gatewayTargetRef{URL: url, Target: targetName, Manifest: manifestPath}, insecure, sessionID)
				if err != nil {
					return err
				}
				return writeRender(out, bubbles, artifacts, asJSON, check)
			}
			if len(args) == 0 {
				return errors.New("pass the output directory of `sam task send`, an events.jsonl file, or --session-id")
			}
			bubbles, artifacts, err := loadTaskOutput(args[0])
			if err != nil {
				return err
			}
			return writeRender(out, bubbles, artifacts, asJSON, check)
		},
	}
	cmd.Flags().StringVarP(&url, "url", "u", "", "Base URL of the webui entrypoint (with --session-id)")
	cmd.Flags().StringVar(&targetName, "target", "", "Cached target name (sam auth login) to address (with --session-id)")
	cmd.Flags().StringVarP(&manifestPath, "manifest", "m", "", "Manifest file whose target (URL and auth) to use (with --session-id)")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification. Also required to send a bearer token over plain http://")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Check the bubbles the web UI saved for this session")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the bubbles and findings as JSON")
	cmd.Flags().BoolVar(&check, "check", false, "Exit non-zero when an error-level problem is found")
	return cmd
}

func writeRender(w io.Writer, bubbles []render.Bubble, artifacts []string, asJSON, check bool) error {
	report := render.NewReport(bubbles, render.Options{Artifacts: artifacts})
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		report.WriteText(w)
	}
	if check && render.HasErrors(report.Findings) {
		return errRenderProblems
	}
	return nil
}

func recordEvents(outputDir string, events []render.Event, debugFn func(string)) {
	if err := writeEvents(filepath.Join(outputDir, renderEventsFile), events); err != nil {
		debugFn(fmt.Sprintf("writing SSE events: %v", err))
	}
}

func renderCheckIfAsked(opts sendTaskOpts, outputDir string, w io.Writer) error {
	if !opts.RenderCheck {
		return nil
	}
	return renderCheckDir(outputDir, w, false)
}

func renderCheckDir(dir string, w io.Writer, asJSON bool) error {
	bubbles, artifacts, err := loadTaskOutput(dir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "--- render check")
	return writeRender(w, bubbles, artifacts, asJSON, true)
}

func loadTaskOutput(path string) ([]render.Bubble, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	eventsPath := path
	var artifacts []string
	if info.IsDir() {
		eventsPath = filepath.Join(path, renderEventsFile)
		artifacts, err = artifactNamesIn(path)
		if err != nil {
			return nil, nil, err
		}
	}
	events, err := readEvents(eventsPath)
	if err != nil {
		return nil, nil, err
	}
	return render.Fold(events), artifacts, nil
}

func artifactNamesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, artifactsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func writeEvents(path string, events []render.Event) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

func readEvents(path string) ([]render.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var events []render.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev render.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

func fetchSessionBubbles(ctx context.Context, ref gatewayTargetRef, insecure bool, sessionID string) ([]render.Bubble, []string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tg, err := resolveGatewayTarget(ctx, ref, defaultTaskSendURL)
	if err != nil {
		return nil, nil, err
	}
	cred, err := resolveGatewayCredential(ctx, tg)
	if err != nil {
		return nil, nil, err
	}
	err = assertSafeBearerOverHTTP(tg.URL, cred.Token, insecure)
	if err != nil {
		return nil, nil, err
	}
	client := &gateway.Client{BaseURL: tg.URL, Token: cred.Token, Timeout: 30 * time.Second}
	body, err := client.FetchChatTasks(sessionID)
	if err != nil {
		return nil, nil, err
	}
	bubbles, err := render.FromChatTasks(body)
	if err != nil {
		return nil, nil, err
	}
	list, err := client.ListArtifacts(sessionID)
	if err != nil {
		return nil, nil, err
	}
	artifacts := []string{}
	for _, a := range list {
		if name, _ := a["filename"].(string); name != "" {
			artifacts = append(artifacts, name)
		}
	}
	return bubbles, artifacts, nil
}
