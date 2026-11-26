// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/gateway"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/runner"
)

func newTaskRunCmd() *cobra.Command {
	var (
		configPaths      []string
		skipFiles        []string
		url              string
		agent            string
		sessionID        string
		files            []string
		timeout          time.Duration
		startupTimeout   time.Duration
		outputDir        string
		quiet            bool
		noSTIM           bool
		systemEnv        bool
		debug            bool
		data             string
		siInputSchema    string
		siOutputSchema   string
		traceFile        string
		traceFilters     []string
		traceAcks        bool
		mockLLM          bool
		mockLLMAddr      string
		mockLLMDelay     time.Duration
		mockLLMScenarios []string
		messageFile      string
		hilAutoApprove   bool
		openBrowser      bool
	)

	cmd := &cobra.Command{
		Use:   "run MESSAGE",
		Short: "Start agents locally, send a task, then stop",
		Long: `Start Agent Mesh locally, send a task to an agent, stream the response, then stop.

This command runs Agent Mesh with the specified configuration, waits for agents
to become ready, sends a task, streams the response, and cleanly shuts down.

MESSAGE is the prompt text to send to the agent.`,
		Example: `  sam task run "What agents are available?"
  sam task run "Hello" -c examples/agents/orchestrator.yaml -c examples/entrypoints/webui_entrypoint_example.yaml
  sam task run "Summarize this document" --file ./document.pdf -c configs/
  sam task run "Analyze data" --agent data_analyst -c configs/
  sam task run --message-file message.txt --mock-llm -c configs/`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			message, err := resolveMessage(args, messageFile)
			if err != nil {
				return err
			}
			return runTask(message, cmd.OutOrStdout(), cmd.ErrOrStderr(), runTaskOpts{
				ConfigPaths:      configPaths,
				SkipFiles:        skipFiles,
				URL:              url,
				Agent:            agent,
				SessionID:        sessionID,
				Files:            files,
				Timeout:          timeout,
				StartupTimeout:   startupTimeout,
				OutputDir:        outputDir,
				Quiet:            quiet,
				NoSTIM:           noSTIM,
				SystemEnv:        systemEnv,
				Debug:            debug,
				Data:             data,
				SIInputSchema:    siInputSchema,
				SIOutputSchema:   siOutputSchema,
				TraceFile:        traceFile,
				TraceFilters:     traceFilters,
				TraceAcks:        traceAcks,
				MockLLM:          mockLLM,
				MockLLMAddr:      mockLLMAddr,
				MockLLMDelay:     mockLLMDelay,
				MockLLMScenarios: mockLLMScenarios,
				HILAutoApprove:   hilAutoApprove,
				OpenBrowser:      openBrowser,
			})
		},
	}

	cmd.Flags().StringArrayVarP(&configPaths, "config", "c", nil,
		"YAML config files or directories (can be used multiple times)")
	cmd.Flags().StringArrayVarP(&skipFiles, "skip", "s", nil,
		"File name(s) to exclude from configs")
	cmd.Flags().StringVarP(&url, "url", "u", envOrDefault("SAM_WEBUI_URL", "http://localhost:8800"),
		"Base URL of the webui entrypoint")
	cmd.Flags().StringVarP(&agent, "agent", "a", envOrDefault("SAM_AGENT", "orchestrator"),
		"Target agent name")
	cmd.Flags().StringVar(&sessionID, "session-id", "",
		"Session ID for context continuity")
	cmd.Flags().StringArrayVarP(&files, "file", "f", nil,
		"File(s) to attach")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute,
		"Timeout for task execution (e.g. 5m, 300s)")
	cmd.Flags().DurationVar(&startupTimeout, "startup-timeout", time.Minute,
		"Timeout for agent readiness (e.g. 60s, 2m)")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "",
		"Output directory for artifacts and logs")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false,
		"Suppress streaming output, only show final result")
	cmd.Flags().BoolVar(&noSTIM, "no-stim", false,
		"Do not fetch the STIM file on completion")
	cmd.Flags().BoolVar(&systemEnv, "system-env", false,
		"Use system environment variables only; do not load .env file")
	cmd.Flags().BoolVar(&debug, "debug", false,
		"Enable debug output")
	cmd.Flags().StringVarP(&data, "data", "d", "",
		"JSON data to send as a DataPart (inline JSON or @filepath)")
	cmd.Flags().StringVar(&siInputSchema, "si-input-schema", "",
		"JSON Schema file for structured invocation input validation")
	cmd.Flags().StringVar(&siOutputSchema, "si-output-schema", "",
		"JSON Schema file for structured invocation output validation")
	cmd.Flags().StringVar(&traceFile, "trace-file", "",
		"Path to trace output file (NDJSON); enables dev broker message tracing")
	cmd.Flags().StringArrayVar(&traceFilters, "trace-filter", nil,
		"Topic filter for tracing (Solace wildcards; repeatable). Default: trace all")
	cmd.Flags().BoolVar(&traceAcks, "trace-acks", false,
		"Also trace message ACK events")
	cmd.Flags().BoolVar(&mockLLM, "mock-llm", false,
		"Start an embedded mock LLM server (OpenAI-compatible)")
	cmd.Flags().StringVar(&mockLLMAddr, "mock-llm-addr", "127.0.0.1:8088",
		"Listen address for the mock LLM server")
	cmd.Flags().DurationVar(&mockLLMDelay, "mock-llm-delay", 0,
		"Simulated TTFT delay for mock LLM responses")
	cmd.Flags().StringArrayVar(&mockLLMScenarios, "mock-llm-scenario", nil,
		"Path to scenario JSON file to pre-load on mock LLM (repeatable)")
	cmd.Flags().StringVar(&messageFile, "message-file", "",
		"Read user message from file instead of positional argument")
	cmd.Flags().BoolVar(&hilAutoApprove, "hil-auto-approve", false,
		"Auto-approve tool approval requests inline (no exit)")
	cmd.Flags().BoolVar(&openBrowser, "open-browser", true,
		"Open the default browser when a tool requests user OAuth (pass --open-browser=false to just print the URL)")

	return cmd
}

type runTaskOpts struct {
	ConfigPaths      []string
	SkipFiles        []string
	URL              string
	Agent            string
	SessionID        string
	Files            []string
	Timeout          time.Duration
	StartupTimeout   time.Duration
	OutputDir        string
	Quiet            bool
	NoSTIM           bool
	SystemEnv        bool
	Debug            bool
	Data             string
	SIInputSchema    string
	SIOutputSchema   string
	TraceFile        string
	TraceFilters     []string
	TraceAcks        bool
	MockLLM          bool
	MockLLMAddr      string
	MockLLMDelay     time.Duration
	MockLLMScenarios []string
	HILAutoApprove   bool
	OpenBrowser      bool
}

func runTask(message string, out, errOut io.Writer, opts runTaskOpts) error {
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

	configFiles, err := runner.DiscoverConfigFiles(opts.ConfigPaths, opts.SkipFiles)
	if err != nil {
		return fmt.Errorf("discovering config files: %w", err)
	}
	if len(configFiles) == 0 {
		return fmt.Errorf("no configuration files found")
	}

	infoFn(fmt.Sprintf("Starting Agent Mesh with %d config file(s)...", len(configFiles)))
	for _, cf := range configFiles {
		debugFn(fmt.Sprintf("  Config: %s", cf))
	}

	outDir := opts.OutputDir
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), fmt.Sprintf("sam-task-run-%s", idgen.New()))
	}
	_ = os.MkdirAll(outDir, 0o755)
	logFile := filepath.Join(outDir, "sam.log")

	logLevel := slog.LevelInfo
	if opts.Debug {
		logLevel = slog.LevelDebug
	}
	logger := samlog.NewStderrLogger(logLevel)

	runnerOpts := runner.Options{
		ConfigFiles:  configFiles,
		SystemEnv:    opts.SystemEnv,
		Logger:       logger,
		TraceFile:    opts.TraceFile,
		TraceFilters: opts.TraceFilters,
		TraceAcks:    opts.TraceAcks,
	}
	if opts.MockLLM {
		runnerOpts.MockLLMAddr = opts.MockLLMAddr
		runnerOpts.MockLLMDelay = opts.MockLLMDelay
		runnerOpts.MockLLMScenarios = opts.MockLLMScenarios
		infoFn(fmt.Sprintf("Mock LLM server will start on %s", opts.MockLLMAddr))
		infoFn("  Agent config must have: api_base: \"http://" + opts.MockLLMAddr + "\"")
	}
	samRunner := runner.New(runnerOpts)

	ctx := context.Background()
	if startErr := samRunner.Start(ctx); startErr != nil {
		return fmt.Errorf("starting SAM: %w", startErr)
	}

	defer func() {
		infoFn("")
		infoFn("Stopping Agent Mesh...")
		samRunner.Stop()
		infoFn("Done.")
	}()

	infoFn("Agent Mesh started.")

	infoFn(fmt.Sprintf("Waiting for agents (timeout: %s)...", opts.StartupTimeout))

	client := &gateway.Client{
		BaseURL: opts.URL,
		Token:   resolveGatewayBearer(ctx, opts.URL, ""),
		Timeout: 10 * time.Second,
		Debug:   opts.Debug,
	}

	resolvedAgent, err := waitForAgents(client, opts.Agent, opts.StartupTimeout, debugFn)
	if err != nil {
		return fmt.Errorf("waiting for agents (check %s for logs): %w", logFile, err)
	}

	infoFn(fmt.Sprintf("Agent ready: %s", resolvedAgent))
	if resolvedAgent != opts.Agent {
		infoFn(fmt.Sprintf("Using agent: %s", resolvedAgent))
	}
	infoFn("")

	return executeTask(message, resolvedAgent, sendTaskOpts{
		URL:            opts.URL,
		Agent:          resolvedAgent,
		SessionID:      opts.SessionID,
		Files:          opts.Files,
		Timeout:        opts.Timeout,
		OutputDir:      outDir,
		Quiet:          opts.Quiet,
		NoSTIM:         opts.NoSTIM,
		Debug:          opts.Debug,
		Data:           opts.Data,
		SIInputSchema:  opts.SIInputSchema,
		SIOutputSchema: opts.SIOutputSchema,
		HILAutoApprove: opts.HILAutoApprove,
		OpenBrowser:    opts.OpenBrowser,
		Out:            out,
		ErrOut:         errOut,
	}, client, debugFn, infoFn, "")
}

func waitForAgents(client *gateway.Client, target string, timeout time.Duration, debugFn func(string)) (string, error) {
	deadline := time.Now().Add(timeout)
	firstSeen := false

	for time.Now().Before(deadline) {
		cards, err := client.FetchAgentCards()
		if err != nil {
			debugFn(fmt.Sprintf("Entrypoint not yet responding: %v", err))
			time.Sleep(time.Second)
			continue
		}

		if len(cards) > 0 {
			if !firstSeen {
				firstSeen = true
				names := make([]string, len(cards))
				for i, c := range cards {
					names[i] = c.Name
				}
				debugFn(fmt.Sprintf("First agents detected: %v", names))
				debugFn("Waiting 2s for stabilization...")
				time.Sleep(2 * time.Second)

				cards, err = client.FetchAgentCards()
				if err != nil {
					debugFn(fmt.Sprintf("Re-fetch failed: %v", err))
					continue
				}
			}

			resolved := gateway.ResolveAgentName(cards, target)
			if resolved != "" {
				return resolved, nil
			}
			debugFn(fmt.Sprintf("Target agent %q not yet available", target))
		}

		time.Sleep(time.Second)
	}

	return "", fmt.Errorf("timeout waiting for agent %q after %s", target, timeout)
}

func resolveMessage(args []string, messageFile string) (string, error) {
	if messageFile != "" {
		if len(args) > 0 {
			return "", fmt.Errorf("cannot use both MESSAGE argument and --message-file")
		}
		data, err := os.ReadFile(messageFile)
		if err != nil {
			return "", fmt.Errorf("reading message file: %w", err)
		}
		return string(data), nil
	}
	if len(args) == 0 {
		return "", fmt.Errorf("MESSAGE argument or --message-file is required")
	}
	return args[0], nil
}
