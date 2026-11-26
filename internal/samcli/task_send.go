// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/gateway"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/render"
)

const defaultTaskSendURL = "http://localhost:8800"

func resolveGatewayToken(ctx context.Context, baseURL, explicit string) string {
	if explicit != "" {
		return explicit
	}
	return cachedLoginCredential(ctx, implicitLoginCandidates(target.Target{URL: baseURL})...).Token
}

func newTaskSendCmd() *cobra.Command {
	var (
		url            string
		targetName     string
		manifestPath   string
		insecure       bool
		agent          string
		sessionID      string
		files          []string
		timeout        time.Duration
		outputDir      string
		quiet          bool
		noSTIM         bool
		debug          bool
		data           string
		siInputSchema  string
		siOutputSchema string
		messageFile    string
		hilRespond     string
		hilAnswer      string
		hilApprove     string
		hilDeny        string
		hilAutoApprove bool
		openBrowser    bool
		renderCheck    bool
	)

	cmd := &cobra.Command{
		Use:   "send MESSAGE",
		Short: "Send a task to a running entrypoint",
		Long: `Send a task to the webui entrypoint and stream the response.

MESSAGE is the prompt text to send to the agent.

The entrypoint is selected with the same precedence as ` + "`sam api`" + `:
  --url > --target > --manifest > SAM_WEBUI_URL > single cached login >
  http://localhost:8800.

Authentication mirrors ` + "`sam api`" + `: the bearer token comes from
SAM_AUTH_TOKEN, then SAM_PLATFORM_TOKEN, then the cached SAM access token
written by ` + "`sam auth login`" + ` (auto-refreshed near expiry). A cached
token will refuse to be sent over plain http:// unless --insecure is also
passed.`,
		Example: `  sam task send "What is the weather today?"
  sam task send "Analyze this" --agent data_analyst
  sam task send "Summarize this document" --file ./document.pdf
  sam task send "Continue" --session-id abc-123
  sam task send --target dev "Run the data pipeline"
  sam task send --message-file message.txt -a my_agent`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if hilRespond != "" || hilApprove != "" || hilDeny != "" {
				return handleHILRespond(sendTaskOpts{
					URL:            url,
					Target:         targetName,
					Manifest:       manifestPath,
					Insecure:       insecure,
					Timeout:        timeout,
					OutputDir:      outputDir,
					Quiet:          quiet,
					NoSTIM:         noSTIM,
					Debug:          debug,
					HILRespondFile: hilRespond,
					HILAnswer:      hilAnswer,
					HILApproveFile: hilApprove,
					HILDenyFile:    hilDeny,
					OpenBrowser:    openBrowser,
					Out:            cmd.OutOrStdout(),
					ErrOut:         cmd.ErrOrStderr(),
				})
			}

			message, err := resolveMessage(args, messageFile)
			if err != nil {
				return err
			}
			return sendTask(message, sendTaskOpts{
				URL:            url,
				Target:         targetName,
				Manifest:       manifestPath,
				Insecure:       insecure,
				Agent:          agent,
				SessionID:      sessionID,
				Files:          files,
				Timeout:        timeout,
				OutputDir:      outputDir,
				Quiet:          quiet,
				NoSTIM:         noSTIM,
				Debug:          debug,
				Data:           data,
				SIInputSchema:  siInputSchema,
				SIOutputSchema: siOutputSchema,
				HILAutoApprove: hilAutoApprove,
				OpenBrowser:    openBrowser,
				RenderCheck:    renderCheck,
				Out:            cmd.OutOrStdout(),
				ErrOut:         cmd.ErrOrStderr(),
			})
		},
	}

	cmd.Flags().StringVarP(&url, "url", "u", "",
		"Base URL of the webui entrypoint (overrides --target/--manifest/SAM_WEBUI_URL/cache). With --manifest, the manifest's credentials are sent to this URL")
	cmd.Flags().StringVar(&targetName, "target", "",
		"Cached target name (sam auth login) to address")
	cmd.Flags().StringVarP(&manifestPath, "manifest", "m", "",
		"Manifest file whose target (URL and auth) to use")
	cmd.Flags().BoolVar(&insecure, "insecure", false,
		"Skip TLS verification. Also required to send a bearer token over plain http://")
	cmd.Flags().StringVarP(&agent, "agent", "a", envOrDefault("SAM_AGENT", "orchestrator"),
		"Target agent name")
	cmd.Flags().StringVar(&sessionID, "session-id", "",
		"Session ID for context continuity")
	cmd.Flags().StringArrayVarP(&files, "file", "f", nil,
		"File(s) to attach")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"Timeout for the SSE connection (e.g. 2m, 90s)")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "",
		"Output directory for artifacts and logs")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false,
		"Suppress streaming output, only show final result")
	cmd.Flags().BoolVar(&noSTIM, "no-stim", false,
		"Do not fetch the STIM file on completion")
	cmd.Flags().BoolVar(&debug, "debug", false,
		"Enable debug output")
	cmd.Flags().StringVarP(&data, "data", "d", "",
		"JSON data to send as a DataPart (inline JSON or @filepath)")
	cmd.Flags().StringVar(&siInputSchema, "si-input-schema", "",
		"JSON Schema file for structured invocation input validation")
	cmd.Flags().StringVar(&siOutputSchema, "si-output-schema", "",
		"JSON Schema file for structured invocation output validation")
	cmd.Flags().StringVar(&messageFile, "message-file", "",
		"Read user message from file instead of positional argument")
	cmd.Flags().StringVar(&hilRespond, "hil-respond", "",
		"Load saved HIL request file, post answer, and stream remaining output")
	cmd.Flags().StringVar(&hilAnswer, "hil-answer", "",
		"JSON answer data for --hil-respond (inline JSON or @filepath)")
	cmd.Flags().StringVar(&hilApprove, "hil-approve", "",
		"Approve a tool approval HIL request and stream remaining output")
	cmd.Flags().StringVar(&hilDeny, "hil-deny", "",
		"Deny a tool approval HIL request and stream remaining output")
	cmd.Flags().BoolVar(&hilAutoApprove, "hil-auto-approve", false,
		"Auto-approve tool approval requests inline (no exit)")
	cmd.Flags().BoolVar(&openBrowser, "open-browser", true,
		"Open the default browser when a tool requests user OAuth (pass --open-browser=false to just print the URL)")
	cmd.Flags().BoolVar(&renderCheck, "render-check", false,
		"Rebuild the chat bubbles the web UI would show and fail on rendering problems")
	_ = cmd.Flags().MarkHidden("render-check")

	return cmd
}

type sendTaskOpts struct {
	URL            string
	Target         string
	Manifest       string
	Insecure       bool
	Agent          string
	SessionID      string
	Files          []string
	Timeout        time.Duration
	OutputDir      string
	Quiet          bool
	NoSTIM         bool
	Debug          bool
	Data           string
	SIInputSchema  string
	SIOutputSchema string
	HILAutoApprove bool
	HILRespondFile string
	HILAnswer      string
	HILApproveFile string
	HILDenyFile    string
	OpenBrowser    bool
	RenderCheck    bool

	Out io.Writer

	ErrOut io.Writer
}

func (o sendTaskOpts) stdoutOr() io.Writer {
	if o.Out != nil {
		return o.Out
	}
	return os.Stdout
}

func (o sendTaskOpts) stderrOr() io.Writer {
	if o.ErrOut != nil {
		return o.ErrOut
	}
	return os.Stderr
}

func sendTask(message string, opts sendTaskOpts) error {
	errOut := opts.stderrOr()
	debugFn := func(msg string) {
		if opts.Debug {
			_, _ = fmt.Fprintf(errOut, "\033[33m[DEBUG] %s\033[0m\n", msg)
		}
	}
	infoFn := func(msg string) {
		if !opts.Quiet {
			_, _ = fmt.Fprintln(errOut, msg)
		}
	}

	ctx := context.Background()
	tg, err := resolveGatewayTarget(ctx, gatewayTargetRef{
		URL:      opts.URL,
		Target:   opts.Target,
		Manifest: opts.Manifest,
	}, defaultTaskSendURL)
	if err != nil {
		return err
	}
	baseURL := tg.URL
	opts.URL = baseURL
	cred, err := resolveGatewayCredential(ctx, tg)
	if err != nil {
		return err
	}
	tok := cred.Token
	if safeErr := assertSafeBearerOverHTTP(baseURL, tok, opts.Insecure); safeErr != nil {
		return safeErr
	}
	recordAddressedGateway(baseURL, defaultTaskSendURL, tok, opts.Insecure, opts.Insecure)

	client := &gateway.Client{
		BaseURL: opts.URL,
		Token:   tok,
		Timeout: 30 * time.Second,
		Debug:   opts.Debug,
	}

	debugFn("Fetching available agents...")
	cards, err := client.FetchAgentCards()
	if err != nil {
		return fmt.Errorf("fetching agents: %w (is the entrypoint running?)", err)
	}
	debugFn(fmt.Sprintf("Found %d agents", len(cards)))

	resolved := gateway.ResolveAgentName(cards, opts.Agent)
	if resolved == "" {
		names := make([]string, len(cards))
		for i, c := range cards {
			names[i] = c.Name
		}
		return fmt.Errorf("agent %q not found; available: %v", opts.Agent, names)
	}
	debugFn(fmt.Sprintf("Resolved agent: %s", resolved))

	return executeTask(message, resolved, opts, client, debugFn, infoFn, "  (use --session-id to continue)")
}

func executeTask(
	message, agent string,
	opts sendTaskOpts,
	client *gateway.Client,
	debugFn func(string),
	infoFn func(string),
	sessionHint string,
) error {
	out := opts.stdoutOr()
	errOut := opts.stderrOr()
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = idgen.New()
	}

	infoFn(fmt.Sprintf("Sending task to %s...", agent))
	debugFn(fmt.Sprintf("POST %s/api/v1/message:stream", opts.URL))

	taskResp, err := client.SendTask(agent, sessionID, message, opts.Files, opts.Data, opts.SIInputSchema, opts.SIOutputSchema)
	if err != nil {
		return err
	}

	if taskResp.Error != nil {
		return fmt.Errorf("task error: %s", taskResp.Error.Message)
	}

	taskID, _ := taskResp.Result["id"].(string)
	if taskID == "" {
		return fmt.Errorf("no task ID in response")
	}

	if contextID, _ := taskResp.Result["contextId"].(string); contextID != "" {
		sessionID = contextID
	}

	infoFn(fmt.Sprintf("Task ID: %s", taskID))
	if !opts.Quiet {
		_, _ = fmt.Fprintln(errOut)
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(os.TempDir(), "sam-task-"+taskID)
	}
	_ = os.MkdirAll(outputDir, 0o755)

	debugFn(fmt.Sprintf("Subscribing to SSE events for task %s", taskID))

	assembler := gateway.NewMessageAssembler()
	var responseText []string
	var events []render.Event

	stream := gateway.NewSSEStream(client, opts.Timeout)

	err = stream.Subscribe(taskID, func(event gateway.SSEEvent) error {
		if event.Sequence > 0 {
			assembler.SetLastSequence(event.Sequence)
		}
		events = append(events, render.Event{Seq: event.Sequence, Type: event.EventType, Data: json.RawMessage(event.RawData)})

		msg, newText := assembler.ProcessEvent(event.EventType, event.Data)

		if newText != "" {
			responseText = append(responseText, newText)
			if !opts.Quiet {
				_, _ = fmt.Fprint(out, newText)
			}
		}

		applyAuthRequest(msg, assembler, opts.OpenBrowser, opts.Quiet)

		if msg.HILRequest != nil {
			hilReq := msg.HILRequest

			if opts.HILAutoApprove && hilReq.Source == "tool_approval" {
				toolName := extractToolName(hilReq)
				_, _ = fmt.Fprintf(errOut, "\n  Auto-approving tool: %s\n", toolName)
				if hilErr := client.RespondToHIL(gateway.HILRespondRequest{
					TaskID:    hilReq.TaskID,
					AgentName: hilReq.AgentName,
					RequestID: hilReq.RequestID,
					Status:    "answered",
					Data:      map[string]any{"approved": true},
				}); hilErr != nil {
					return fmt.Errorf("auto-approve HIL: %w", hilErr)
				}
				assembler.ClearHILRequest()
				return nil
			}

			hilReq.LastSequence = assembler.Message().LastSequence
			hilReq.SessionID = sessionID
			hilReq.GatewayURL = opts.URL
			hilPath := filepath.Join(outputDir, fmt.Sprintf("hil_request_%s.json", hilReq.RequestID))
			hilJSON, _ := json.MarshalIndent(hilReq, "", "  ")
			_ = os.WriteFile(hilPath, hilJSON, 0o644)
			if !opts.Quiet {
				_, _ = fmt.Fprintln(errOut)
			}
			printHILSummary(hilReq, hilPath)
			return errHILPending
		}

		if msg.IsComplete {
			debugFn("Task is complete")
			return errStreamDone
		}
		return nil
	})

	recordEvents(outputDir, events, debugFn)

	if err == errHILPending {
		return nil
	}
	if err != nil && err != errStreamDone {
		return fmt.Errorf("SSE stream: %w", err)
	}

	if !opts.Quiet && len(responseText) > 0 {
		_, _ = fmt.Fprintln(out)
	}

	finalMsg := assembler.Message()

	if len(responseText) > 0 {
		var fullText string
		for _, t := range responseText {
			fullText += t
		}
		responsePath := filepath.Join(outputDir, "response.txt")
		_ = os.WriteFile(responsePath, []byte(fullText), 0o644)
	}

	writeStructuredResult(outputDir, finalMsg)

	artifacts, err := client.DownloadAllArtifacts(sessionID, outputDir)
	if err == nil && len(artifacts) > 0 && !opts.Quiet {
		_, _ = fmt.Fprintln(errOut)
		_, _ = fmt.Fprintln(errOut, "Downloaded artifacts:")
		for _, a := range artifacts {
			_, _ = fmt.Fprintf(errOut, "  - %s (%d bytes)\n", a.Filename, a.Size)
		}
	}

	if !opts.NoSTIM {
		if stimErr := client.DownloadSTIM(taskID, outputDir); stimErr != nil && opts.Debug {
			debugFn(fmt.Sprintf("STIM download warning: %v", stimErr))
		}
	}

	_, _ = fmt.Fprintln(errOut)
	_, _ = fmt.Fprintln(errOut, "---")
	if finalMsg.IsError {
		_, _ = fmt.Fprintln(errOut, "Task failed.")
	} else {
		_, _ = fmt.Fprintln(errOut, "Task completed successfully.")
	}

	_, _ = fmt.Fprintf(errOut, "Session ID: %s%s\n", sessionID, sessionHint)
	_, _ = fmt.Fprintf(errOut, "Task ID: %s\n", taskID)
	_, _ = fmt.Fprintf(errOut, "Output directory: %s\n", outputDir)

	if checkErr := renderCheckIfAsked(opts, outputDir, errOut); checkErr != nil {
		return checkErr
	}

	if finalMsg.IsError {
		return errTaskFailed
	}
	return nil
}

func writeStructuredResult(outputDir string, finalMsg gateway.AssembledMessage) {
	path := filepath.Join(outputDir, "response.json")
	if len(finalMsg.DataParts) == 0 {
		_ = os.Remove(path)
		return
	}
	resultJSON, _ := json.MarshalIndent(finalMsg.DataParts, "", "  ")
	_ = os.WriteFile(path, resultJSON, 0o644)
}

var (
	errStreamDone = fmt.Errorf("stream done")
	errHILPending = fmt.Errorf("HIL pending")
	errTaskFailed = fmt.Errorf("task failed")
)

func IsAlreadyReported(err error) bool {
	return errors.Is(err, errTaskFailed) || errors.Is(err, errChangesPending)
}

func applyAuthRequest(msg gateway.AssembledMessage, assembler *gateway.MessageAssembler, openBrowser, quiet bool) bool {
	if msg.AuthRequest == nil {
		return false
	}
	handleAuthRequest(msg.AuthRequest, openBrowser, quiet)
	assembler.ClearAuthRequest()
	return true
}

func handleAuthRequest(req *gateway.AuthRequest, openBrowser, quiet bool) {
	if req == nil || req.AuthURI == "" {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "\033[36mAuthentication required for tool on agent %q.\033[0m\n", req.TargetAgent)
	fmt.Fprintf(os.Stderr, "  Open this URL in a browser to authorize:\n  %s\n", req.AuthURI)
	if openBrowser {
		if err := oauth.OpenBrowser(req.AuthURI); err != nil {
			fmt.Fprintf(os.Stderr, "  Could not launch browser automatically (%v). Open the URL above manually.\n", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "  (--open-browser=false: not launching automatically)")
	}
	if !quiet {
		fmt.Fprintln(os.Stderr, "Waiting for authorization to complete…")
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func handleHILRespond(opts sendTaskOpts) error {
	var hilFile string
	var answerData map[string]any
	var status string

	switch {
	case opts.HILApproveFile != "":
		hilFile = opts.HILApproveFile
		answerData = map[string]any{"approved": true}
		status = "answered"
	case opts.HILDenyFile != "":
		hilFile = opts.HILDenyFile
		answerData = map[string]any{}
		status = "cancelled"
	case opts.HILRespondFile != "":
		hilFile = opts.HILRespondFile
		status = "answered"
		parsed, err := parseHILAnswer(opts.HILAnswer)
		if err != nil {
			return err
		}
		answerData = parsed
	default:
		return fmt.Errorf("no HIL request file specified")
	}

	hilReq, err := loadHILRequest(hilFile)
	if err != nil {
		return err
	}

	out := opts.stdoutOr()
	errOut := opts.stderrOr()
	debugFn := func(msg string) {
		if opts.Debug {
			_, _ = fmt.Fprintf(errOut, "\033[33m[DEBUG] %s\033[0m\n", msg)
		}
	}

	ref := gatewayTargetRef{URL: opts.URL, Target: opts.Target, Manifest: opts.Manifest}
	if ref.URL == "" && ref.Target == "" && ref.Manifest == "" && hilReq.GatewayURL != "" {
		ref.URL = hilReq.GatewayURL
	}
	ctx := context.Background()
	tg, err := resolveGatewayTarget(ctx, ref, defaultTaskSendURL)
	if err != nil {
		return err
	}
	gwURL := tg.URL
	cred, err := resolveGatewayCredential(ctx, tg)
	if err != nil {
		return err
	}
	tok := cred.Token
	if safeErr := assertSafeBearerOverHTTP(gwURL, tok, opts.Insecure); safeErr != nil {
		return safeErr
	}
	recordAddressedGateway(gwURL, defaultTaskSendURL, tok, opts.Insecure, opts.Insecure)

	client := &gateway.Client{
		BaseURL: gwURL,
		Token:   tok,
		Timeout: 30 * time.Second,
		Debug:   opts.Debug,
	}

	_, _ = fmt.Fprintf(errOut, "Resuming task %s...\n", hilReq.TaskID)

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(os.TempDir(), "sam-task-"+hilReq.TaskID)
	}
	_ = os.MkdirAll(outputDir, 0o755)

	stream := gateway.NewSSEStream(client, opts.Timeout)
	assembler := gateway.NewMessageAssembler()

	sseDone := make(chan error, 1)
	var streamed bool
	go func() {
		sseDone <- stream.SubscribeFrom(hilReq.TaskID, hilReq.LastSequence, func(event gateway.SSEEvent) error {
			if event.Sequence > 0 {
				assembler.SetLastSequence(event.Sequence)
			}
			msg, newText := assembler.ProcessEvent(event.EventType, event.Data)
			if newText != "" && !opts.Quiet {
				streamed = true
				_, _ = fmt.Fprint(out, newText)
			}
			applyAuthRequest(msg, assembler, opts.OpenBrowser, opts.Quiet)
			if msg.HILRequest != nil {
				hil2 := msg.HILRequest
				hil2.LastSequence = assembler.Message().LastSequence
				hil2.SessionID = hilReq.SessionID
				hil2.GatewayURL = gwURL
				path := filepath.Join(outputDir, fmt.Sprintf("hil_request_%s.json", hil2.RequestID))
				data, _ := json.MarshalIndent(hil2, "", "  ")
				_ = os.WriteFile(path, data, 0o644)
				_, _ = fmt.Fprintln(errOut)
				printHILSummary(hil2, path)
				return errHILPending
			}
			if msg.IsComplete {
				debugFn("Task is complete")
				return errStreamDone
			}
			return nil
		})
	}()

	debugFn(fmt.Sprintf("Posting HIL response: status=%s requestID=%s", status, hilReq.RequestID))
	if respondErr := client.RespondToHIL(gateway.HILRespondRequest{
		TaskID:    hilReq.TaskID,
		AgentName: hilReq.AgentName,
		RequestID: hilReq.RequestID,
		Status:    status,
		Data:      answerData,
	}); respondErr != nil {
		return fmt.Errorf("posting HIL response: %w", respondErr)
	}

	err = <-sseDone
	if err == errHILPending {
		return nil
	}
	if err != nil && err != errStreamDone {
		return fmt.Errorf("SSE stream: %w", err)
	}

	if streamed {
		_, _ = fmt.Fprintln(out)
	}

	finalMsg := assembler.Message()

	writeStructuredResult(outputDir, finalMsg)

	artifacts, dlErr := client.DownloadAllArtifacts(hilReq.SessionID, outputDir)
	if dlErr == nil && len(artifacts) > 0 && !opts.Quiet {
		_, _ = fmt.Fprintln(errOut)
		_, _ = fmt.Fprintln(errOut, "Downloaded artifacts:")
		for _, a := range artifacts {
			_, _ = fmt.Fprintf(errOut, "  - %s (%d bytes)\n", a.Filename, a.Size)
		}
	}

	if !opts.NoSTIM {
		if stimErr := client.DownloadSTIM(hilReq.TaskID, outputDir); stimErr != nil && opts.Debug {
			debugFn(fmt.Sprintf("STIM download warning: %v", stimErr))
		}
	}

	_, _ = fmt.Fprintln(errOut)
	_, _ = fmt.Fprintln(errOut, "---")
	if finalMsg.IsError {
		_, _ = fmt.Fprintln(errOut, "Task failed.")
	} else {
		_, _ = fmt.Fprintln(errOut, "Task completed successfully.")
	}
	_, _ = fmt.Fprintf(errOut, "Session ID: %s\n", hilReq.SessionID)
	_, _ = fmt.Fprintf(errOut, "Task ID: %s\n", hilReq.TaskID)
	_, _ = fmt.Fprintf(errOut, "Output directory: %s\n", outputDir)

	if finalMsg.IsError {
		return errTaskFailed
	}
	return nil
}

func loadHILRequest(path string) (*gateway.HILRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading HIL request file: %w", err)
	}
	var req gateway.HILRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing HIL request file: %w", err)
	}
	return &req, nil
}

func parseHILAnswer(answer string) (map[string]any, error) {
	if answer == "" {
		return map[string]any{}, nil
	}

	var raw []byte
	if strings.HasPrefix(answer, "@") {
		var err error
		raw, err = os.ReadFile(answer[1:])
		if err != nil {
			return nil, fmt.Errorf("reading HIL answer file: %w", err)
		}
	} else {
		raw = []byte(answer)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing HIL answer JSON: %w", err)
	}
	return result, nil
}

func extractToolName(req *gateway.HILRequest) string {
	surface, ok := req.Surface.(map[string]any)
	if !ok {
		return "unknown"
	}
	dm, _ := surface["dataModel"].(map[string]any)
	if dm != nil {
		if name, ok := dm["tool_name"].(string); ok {
			return name
		}
	}
	components, _ := surface["components"].([]any)
	for _, compRaw := range components {
		comp, _ := compRaw.(map[string]any)
		if comp == nil {
			continue
		}
		if id, _ := comp["id"].(string); id == "info" {
			textObj, _ := comp["text"].(map[string]any)
			if textObj != nil {
				text, _ := textObj["literalString"].(string)
				if idx := strings.LastIndex(text, "tool: "); idx >= 0 {
					return text[idx+6:]
				}
			}
		}
	}
	return "unknown"
}

func printHILSummary(req *gateway.HILRequest, filePath string) {
	fmt.Fprintf(os.Stderr, "Agent is requesting input (%s)\n", req.Source)
	fmt.Fprintf(os.Stderr, "Request saved to: %s\n\n", filePath)

	if surface, ok := req.Surface.(map[string]any); ok {
		printSurfaceDetails(surface)
	}

	switch req.Source {
	case "tool_approval":
		fmt.Fprintf(os.Stderr, "Approve:\n  sam task send --hil-approve %s\n\n", filePath)
		fmt.Fprintf(os.Stderr, "Deny:\n  sam task send --hil-deny %s\n", filePath)
	default:
		fmt.Fprintf(os.Stderr, "Respond:\n  sam task send --hil-respond %s \\\n    --hil-answer '{\"answers\": {\"q0\": \"your answer\"}}'\n", filePath)
	}
}

func printSurfaceDetails(surface map[string]any) {
	components, _ := surface["components"].([]any)
	printed := false
	for _, compRaw := range components {
		comp, _ := compRaw.(map[string]any)
		if comp == nil {
			continue
		}
		compType, _ := comp["component"].(string)
		if compType == "" {
			compType, _ = comp["type"].(string)
		}
		switch compType {
		case "Text":
			text := extractTextContent(comp)
			if text != "" {
				variant, _ := comp["variant"].(string)
				if variant == "body" || variant == "caption" {
					fmt.Fprintf(os.Stderr, "  %s\n", text)
					printed = true
				}
			}
		case "ChoicePicker":
			options, _ := comp["options"].([]any)
			for i, optRaw := range options {
				opt, _ := optRaw.(map[string]any)
				if opt == nil {
					continue
				}
				label := extractLiteralString(opt["label"])
				if label == "" || label == "Other" {
					continue
				}
				desc := extractLiteralString(opt["description"])
				if desc != "" {
					fmt.Fprintf(os.Stderr, "    %d. %s — %s\n", i+1, label, desc)
				} else {
					fmt.Fprintf(os.Stderr, "    %d. %s\n", i+1, label)
				}
				printed = true
			}
		}
	}
	if printed {
		fmt.Fprintln(os.Stderr)
	}
}

func extractTextContent(comp map[string]any) string {
	return extractLiteralString(comp["text"])
}

func extractLiteralString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if s, ok := m["literalString"].(string); ok {
			return s
		}
	}
	return ""
}
