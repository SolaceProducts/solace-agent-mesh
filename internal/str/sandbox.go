// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox/egressproxy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str/seccomp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

const (
	bwrapFlagBind   = "--bind"
	bwrapFlagROBind = "--ro-bind"
	bwrapFlagTmpfs  = "--tmpfs"
)

var errBwrapMountPointMissing = errors.New("bwrap bind destination has no mount point in the read-only sandbox root")

const seccompChildFD = 3

func newSeccompFilterFile() (*os.File, error) {
	prog, err := seccomp.HostProgram()
	if err != nil {
		return nil, err
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("seccomp pipe: %w", err)
	}
	if _, err := wr.Write(prog); err != nil {
		_ = wr.Close()
		_ = rd.Close()
		return nil, fmt.Errorf("write seccomp program: %w", err)
	}
	if err := wr.Close(); err != nil {
		_ = rd.Close()
		return nil, fmt.Errorf("close seccomp pipe write end: %w", err)
	}
	return rd, nil
}

type SandboxConfig struct {
	Mode                    string
	BwrapBin                string
	PrlimitBin              string
	WorkBaseDir             string
	DefaultProfile          string
	DefaultTimeoutSeconds   int
	MaxTimeoutSeconds       int
	MaxConcurrentExecutions int
	MaxInFlight             int

	MemorySoftPercent   int
	MemoryHardPercent   int
	MemoryGuardDisabled bool

	ToolOutputLLMReturnMaxBytes int

	SeccompBindDeny bool

	FreshProc string

	Isolated IsolatedConfig
}

type ResourceLimits struct {
	MaxCPUSeconds  int `yaml:"max_cpu_seconds"`
	MaxFileSizeMB  int `yaml:"max_file_size_mb"`
	MaxOpenFiles   int `yaml:"max_open_files"`
	MaxProcesses   int `yaml:"max_processes"`
	MaxStackSizeMB int `yaml:"max_stack_size_mb"`
}

const (
	defaultPerToolRSSBytes int64 = 256 << 20

	fallbackMaxInFlight = 16

	strSelfReserveBytes int64 = 512 << 20

	computeOversubscription = 2

	minComputeBudget = 2
)

func defaultComputeBudget(gomaxprocs int) int {
	return max(gomaxprocs*computeOversubscription, minComputeBudget)
}

func resolveInFlight(memLimitBytes, perToolRSSBytes int64) int {
	if perToolRSSBytes <= 0 {
		perToolRSSBytes = defaultPerToolRSSBytes
	}
	if memLimitBytes > 0 {
		reserve := min(strSelfReserveBytes, memLimitBytes/5)
		return max(int((memLimitBytes-reserve)/perToolRSSBytes), minComputeBudget)
	}
	return max(fallbackMaxInFlight, defaultComputeBudget(runtime.GOMAXPROCS(0)))
}

func (c *SandboxConfig) setDefaults() {
	if c.Mode == "" {
		c.Mode = "direct"
	}
	if c.BwrapBin == "" {
		c.BwrapBin = "/usr/bin/bwrap"
	}
	if c.PrlimitBin == "" {
		c.PrlimitBin = "/usr/bin/prlimit"
	}
	if c.WorkBaseDir == "" {
		c.WorkBaseDir = "/tmp/sam-str-work"
	}
	if c.DefaultProfile == "" {
		c.DefaultProfile = "standard"
	}
	if c.DefaultTimeoutSeconds <= 0 {
		c.DefaultTimeoutSeconds = DefaultRemoteToolTimeoutSeconds
	}
	if c.MaxTimeoutSeconds <= 0 {
		c.MaxTimeoutSeconds = DefaultMaxToolTimeoutSeconds
	}
	if c.MaxConcurrentExecutions <= 0 {
		c.MaxConcurrentExecutions = defaultComputeBudget(runtime.GOMAXPROCS(0))
	}
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = resolveInFlight(cgroupMemoryLimitBytes(), defaultPerToolRSSBytes)
	}
	if c.MemorySoftPercent <= 0 {
		c.MemorySoftPercent = defaultMemorySoftPercent
	}
	if c.MemoryHardPercent <= 0 {
		c.MemoryHardPercent = defaultMemoryHardPercent
	}
	c.MaxConcurrentExecutions = min(c.MaxConcurrentExecutions, c.MaxInFlight)
	if c.FreshProc == "" {
		c.FreshProc = freshProcAuto
	}
}

type toolLLMCallFunc func(ctx context.Context, modelAlias, componentName string, inv *remotetool.InvokeParams, systemPrompt, userPrompt string, temperature float64) (string, error)

type SandboxRunner struct {
	cfg             SandboxConfig
	logger          *slog.Logger
	compute         *computeBudget
	cpuWatch        *cpuIdleWatcher
	memGuard        *memoryGuard
	inflightSem     chan struct{}
	cleanupCh       chan string
	cleanupDone     chan struct{}
	llmCall         toolLLMCallFunc
	llmChat         toolLLMChatFunc
	chatModelChoice bool
	toolModelAlias  string
	volumeSvc       volume.Service
	metrics         *strMetrics
	cpuThrottle     func() (cpuThrottleSample, bool)

	isolated    *isolatedBackend
	isolatedErr error

	freshProc bool

	dispatchNameWarned sync.Map

	instanceMu   sync.Mutex
	instanceDir  string
	instanceLock *os.File
}

type resolvedVolume struct {
	binding  remotetool.VolumeBinding
	hostPath string
	locked   bool
}

type volumeMount struct {
	VolumeID     string `json:"volume_id"`
	MountPath    string `json:"mount_path"`
	DeclaredPath string `json:"declared_path,omitempty"`
	ReadOnly     bool   `json:"read_only"`
}

type slotLease struct {
	r              *SandboxRunner
	memoryAdmitted func()

	mu       sync.Mutex
	holdsCPU bool
	released bool
}

func (l *slotLease) YieldCompute() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.holdsCPU {
		return false
	}
	l.r.compute.release()
	l.holdsCPU = false
	return true
}

func (l *slotLease) ResumeCompute(ctx context.Context) bool {
	l.mu.Lock()
	if l.released || l.holdsCPU {
		defer l.mu.Unlock()
		return l.holdsCPU
	}
	l.mu.Unlock()
	if err := l.r.compute.acquire(ctx); err != nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.holdsCPU {
		l.r.compute.release()
		return l.holdsCPU
	}
	l.holdsCPU = true
	return true
}

func (l *slotLease) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	l.memoryAdmitted()
	if l.holdsCPU {
		l.r.compute.release()
		l.holdsCPU = false
	}
	<-l.r.inflightSem
}

func (r *SandboxRunner) acquireInFlight(ctx context.Context) (*slotLease, *remotetool.InvokeError) {
	return r.acquireInFlightWithin(ctx, "", 0)
}

func (r *SandboxRunner) acquireInFlightWithin(ctx context.Context, tool string, memoryWait time.Duration) (*slotLease, *remotetool.InvokeError) {
	if err := acquireOne(ctx, r.inflightSem, "in-flight slot"); err != nil {
		return nil, err
	}
	admitCtx := ctx
	if memoryWait > 0 {
		var cancel context.CancelFunc
		admitCtx, cancel = context.WithTimeout(ctx, memoryWait)
		defer cancel()
	}
	admitted, err := r.memGuard.admit(admitCtx)
	if err != nil {
		<-r.inflightSem
		if ctx.Err() == nil {
			return nil, &remotetool.InvokeError{
				Code: remotetool.ErrToolMemoryExceeded,
				Message: fmt.Sprintf("tool %q did not start: the tool runtime stayed close to its memory limit "+
					"for %s while other calls were running. Run fewer calls at the same time", tool, memoryWait),
			}
		}
		return nil, newInvokeError(remotetool.ErrInternalError, "context cancelled while waiting for memory")
	}
	return &slotLease{r: r, memoryAdmitted: admitted}, nil
}

func (l *slotLease) acquireCompute(ctx context.Context) *remotetool.InvokeError {
	if !l.ResumeCompute(ctx) {
		return newInvokeError(remotetool.ErrInternalError, "context cancelled while waiting for compute slot")
	}
	return nil
}

func acquireOne(ctx context.Context, sem chan struct{}, what string) *remotetool.InvokeError {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return &remotetool.InvokeError{
			Code:    remotetool.ErrInternalError,
			Message: "context cancelled while waiting for " + what,
		}
	}
}

func NewSandboxRunner(cfg SandboxConfig, logger *slog.Logger, metricsOpt ...*strMetrics) *SandboxRunner {
	cfg.setDefaults()
	if cfg.SeccompBindDeny && cfg.Mode != "bwrap" && logger != nil {
		logger.Warn("STR_SECCOMP_BIND_DENY is set but sandbox mode is not bwrap; bind-deny filter applies in bwrap mode only", "mode", cfg.Mode)
	}
	if cfg.Mode == "direct" && logger != nil {
		logger.Warn("sandbox mode is 'direct': tools run without filesystem or resource isolation; set STR_SANDBOX_MODE=bwrap in production")
	}
	var m *strMetrics
	if len(metricsOpt) > 0 {
		m = metricsOpt[0]
	}
	if logger != nil {
		logger.Info("sandbox admission budgets",
			"gomaxprocs", runtime.GOMAXPROCS(0),
			"computeOversubscription", computeOversubscription,
			"maxConcurrentExecutions", cfg.MaxConcurrentExecutions,
			"maxInFlight", cfg.MaxInFlight,
			"cgroupMemoryMB", cgroupMemoryLimitBytes()/(1<<20),
			"perToolRSSMB", defaultPerToolRSSBytes/(1<<20),
			"memorySoftPercent", cfg.MemorySoftPercent,
			"memoryHardPercent", cfg.MemoryHardPercent,
			"memoryGuardDisabled", cfg.MemoryGuardDisabled)
	}
	r := &SandboxRunner{
		cfg:         cfg,
		logger:      logger,
		compute:     newComputeBudget(cfg.MaxConcurrentExecutions),
		inflightSem: make(chan struct{}, cfg.MaxInFlight),
		cleanupCh:   make(chan string, workDirCleanupQueue),
		cleanupDone: make(chan struct{}),
		metrics:     m,
		cpuThrottle: cgroupCPUThrottle,
		cpuWatch:    newCPUIdleWatcher(procTreeCPU),
	}
	r.cpuWatch.onYield = func() { r.metrics.recordComputeYield() }
	if !cfg.MemoryGuardDisabled && cfg.Mode != sandboxModeKubernetesJob {
		r.memGuard = newMemoryGuard(cgroupMemoryLimitBytes(), cfg.MemorySoftPercent, cfg.MemoryHardPercent, logger, m)
		if r.memGuard == nil && logger != nil {
			logger.Info("memory guard off: no container memory limit")
		}
	}
	r.isolated, r.isolatedErr = startIsolatedBackend(cfg, logger, m)
	return r
}

const workDirCleanupQueue = 64

func (r *SandboxRunner) scheduleWorkDirCleanup(dir string) {
	if r.cleanupCh == nil || r.removerStopped() {
		r.removeWorkDir(dir)
		return
	}
	select {
	case r.cleanupCh <- dir:
		if r.removerStopped() {
			r.removeWorkDir(dir)
		}
	default:
		r.removeWorkDir(dir)
	}
}

func (r *SandboxRunner) removerStopped() bool {
	select {
	case <-r.cleanupDone:
		return true
	default:
		return false
	}
}

func (r *SandboxRunner) removeWorkDir(dir string) {
	if err := forceRemoveAll(dir); err != nil && r.logger != nil {
		r.logger.Warn("work dir cleanup failed", "workDirName", filepath.Base(dir), "error", err)
	}
}

func (r *SandboxRunner) runWorkDirCleanup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			if r.cleanupDone != nil {
				close(r.cleanupDone)
			}
			for {
				select {
				case dir := <-r.cleanupCh:
					r.removeWorkDir(dir)
				default:
					return
				}
			}
		case dir := <-r.cleanupCh:
			r.removeWorkDir(dir)
		}
	}
}

func (r *SandboxRunner) runCPUIdleWatcher(ctx context.Context) {
	r.cpuWatch.logger = r.logger
	if _, ok := r.cpuWatch.sample(nil); !ok {
		if r.logger != nil {
			r.logger.Info("cpu idle watcher disabled: process accounting not readable")
		}
		return
	}
	r.cpuWatch.run(ctx, cpuYieldTick)
}

func (r *SandboxRunner) runMemoryGuard(ctx context.Context) {
	if r.memGuard == nil {
		return
	}
	r.memGuard.run(ctx, memoryGuardTick)
}

func (r *SandboxRunner) runAdaptiveComputeBudget(ctx context.Context) {
	r.runAdaptiveComputeBudgetEvery(ctx, adaptiveComputeTick)
}

func (r *SandboxRunner) runAdaptiveComputeBudgetEvery(ctx context.Context, tick time.Duration) {
	prev, ok := r.cpuThrottle()
	if !ok {
		if r.logger != nil {
			r.logger.Info("adaptive compute budget disabled: no cgroup cpu accounting readable")
		}
		return
	}
	ceil := r.cfg.MaxInFlight
	floor := min(defaultComputeBudget(runtime.GOMAXPROCS(0)), r.cfg.MaxConcurrentExecutions, ceil)
	if r.logger != nil {
		r.logger.Info("adaptive compute budget enabled",
			"initial", r.cfg.MaxConcurrentExecutions, "floor", floor, "ceil", ceil)
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	throttledTicks, calmTicks := 0, 0
	accountingUnreadable := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cur, ok := r.cpuThrottle()
		if !ok {
			if !accountingUnreadable {
				accountingUnreadable = true
				if r.logger != nil {
					r.logger.Debug("cgroup cpu accounting unreadable this tick; holding compute budget")
				}
			}
			continue
		}
		accountingUnreadable = false
		var elapsed uint64
		if cur.periods > prev.periods {
			elapsed = cur.periods - prev.periods
		}
		ratio := throttledRatio(prev, cur)
		prev = cur
		if elapsed == 0 {
			throttledTicks, calmTicks = 0, 0
			limit, _, _ := r.compute.snapshot()
			r.metrics.recordComputeBudget(limit, 0)
			continue
		}
		if ratio > throttleShrinkAbove {
			throttledTicks++
		} else {
			throttledTicks = 0
		}
		if ratio == 0 {
			calmTicks++
		} else {
			calmTicks = 0
		}
		limit, _, waiting := r.compute.snapshot()
		next := nextComputeBudget(limit, floor, ceil, waiting, ratio,
			throttledTicks >= shrinkAfterThrottledTicks, calmTicks >= calmTicksToDouble)
		if next != limit {
			r.compute.setLimit(next)
			if r.logger != nil {
				r.logger.Debug("compute budget adjusted",
					"from", limit, "to", next, "waiting", waiting, "throttledRatio", ratio)
			}
		}
		r.metrics.recordComputeBudget(next, ratio)
	}
}

type runnerArgs struct {
	ToolName         string                    `json:"tool_name"`
	Args             map[string]any            `json:"args"`
	ToolConfig       map[string]any            `json:"tool_config"`
	ArtifactPaths    map[string]string         `json:"artifact_paths"`
	ArtifactMetadata map[string]map[string]any `json:"artifact_metadata"`
	StatusPipe       string                    `json:"status_pipe"`
	ResultFile       string                    `json:"result_file"`
	OutputDir        string                    `json:"output_dir"`
	IPCSocket        string                    `json:"ipc_socket,omitempty"`
	UserID           string                    `json:"user_id"`
	SessionID        string                    `json:"session_id"`
	AppName          string                    `json:"app_name"`
	ToolOAuthToken   string                    `json:"tool_oauth_token,omitempty"`
	ToolAuthHeaders  map[string]string         `json:"tool_auth_headers,omitempty"`
	VolumeMounts     []volumeMount             `json:"volume_mounts,omitempty"`
	TaskID           string                    `json:"task_id,omitempty"`
	A2AUserConfig    string                    `json:"a2a_user_config,omitempty"`
	SearchTurn       *int                      `json:"search_turn,omitempty"`
}

type resultJSON struct {
	Result any    `json:"result"`
	Error  string `json:"error"`
}

func (r *SandboxRunner) Execute(
	ctx context.Context,
	params *remotetool.InvokeParams,
	entry *ManifestEntry,
	artifactSvc *artifact.ScopedService,
	statusCb func(string),
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	timeout, timeoutSource := resolveExecTimeout(r.cfg.DefaultTimeoutSeconds, r.cfg.MaxTimeoutSeconds, entry, params)
	semWaitStart := time.Now()
	lease, semErr := r.acquireInFlightWithin(ctx, entry.ToolName, time.Duration(timeout)*time.Second)
	if semErr != nil {
		return nil, semErr
	}
	defer lease.Release()
	inflightWait := time.Since(semWaitStart)

	start := time.Now()

	workDir, err := r.setupWorkDir()
	if err != nil {
		r.logger.Error("sandbox setup: work dir creation failed",
			"tool", entry.ToolName,
			"corrID", params.TaskID,
			"traceID", params.TraceID,
			"error", err,
		)
		return nil, newInvokeError(remotetool.ErrInternalError, "setup work dir: %v", err)
	}
	defer r.scheduleWorkDirCleanup(workDir)

	inputDir := filepath.Join(workDir, "input")
	outputDir := filepath.Join(workDir, "output")
	statusPipe := filepath.Join(workDir, "status.pipe")
	resultFile := filepath.Join(workDir, "result.json")
	argsFile := filepath.Join(workDir, "runner_args.json")

	execLog := r.logger.With(
		"tool", entry.ToolName,
		"corrID", params.TaskID,
		"traceID", params.TraceID,
		"mode", r.cfg.Mode,
		"timeout_s", timeout,
		"timeoutSource", timeoutSource,
		"toolCategory", entry.toolCategory(),
	)

	if r.isolated != nil {
		return r.executeIsolated(ctx, toolCall{params: params, entry: entry, artifactSvc: artifactSvc, log: execLog}, statusCb, workDir, timeout, inflightWait)
	}

	artifactPaths, artifactMeta, preloadErr := r.preloadArtifacts(ctx, params, artifactSvc, inputDir)
	if preloadErr != nil {
		return nil, preloadErr
	}

	computeWaitStart := time.Now()
	if computeErr := lease.acquireCompute(ctx); computeErr != nil {
		return nil, computeErr
	}
	computeWait := time.Since(computeWaitStart)
	r.metrics.recordSemaphoreWait(inflightWait+computeWait, backendSubprocess)

	var volumes []resolvedVolume
	if len(params.VolumeBindings) > 0 && r.volumeSvc != nil {
		var volErr *remotetool.InvokeError
		volumes, volErr = r.resolveVolumes(ctx, params, entry, execLog)
		if volErr != nil {
			return nil, volErr
		}
		defer r.releaseVolumeLocks(ctx, params.TaskID, volumes, execLog)
	}

	var proxyURL string
	if len(params.NetworkBindings) > 0 {
		proxy, proxyErr := r.startEgressProxy(params)
		if proxyErr != nil {
			return nil, proxyErr
		}
		proxyURL = proxy.Address()
		defer func() { _ = proxy.Stop() }()
	}

	if mkfifoErr := mkfifo(statusPipe, 0o600); mkfifoErr != nil {
		execLog.Error("sandbox setup: status pipe creation failed", "error", mkfifoErr)
		return nil, newInvokeError(remotetool.ErrInternalError, "mkfifo: %v", mkfifoErr)
	}

	var ipcSocket string
	var ipcServer *IPCServer
	if r.llmCall != nil {
		ipcSocket = r.ipcSocketPath(workDir, params.TaskID)
		var ipcErr error
		modelAlias, aliasErr := toolConfigModelAlias(params.ToolConfig)
		if aliasErr != nil {
			return nil, &remotetool.InvokeError{
				Code:    remotetool.ErrInternalError,
				Message: aliasErr.Error(),
			}
		}

		llmWithYield := func(cbCtx context.Context, comp, sys, user string, temp float64) (string, error) {
			lease.YieldCompute()
			defer lease.ResumeCompute(cbCtx)
			return r.llmCall(cbCtx, modelAlias, comp, params, sys, user, temp)
		}
		chatModels, modelsErr := toolConfigChatModels(params.ToolConfig, r.chatModelChoice, r.toolModelAlias)
		if modelsErr != nil {
			return nil, &remotetool.InvokeError{Code: remotetool.ErrInternalError, Message: modelsErr.Error()}
		}
		ipcServer, ipcErr = NewIPCServer(ipcSocket, llmWithYield, entry.ToolName, r.logger)
		if ipcErr == nil && r.llmChat != nil {
			ipcServer.SetChat(func(cbCtx context.Context, comp, model string, req *llm.ChatRequest) (*llm.ChatResponse, error) {
				chatChoice, pickErr := chatModels.pick(model)
				if pickErr != nil {
					return nil, pickErr
				}
				lease.YieldCompute()
				defer lease.ResumeCompute(cbCtx)
				return r.llmChat(cbCtx, chatChoice, comp, params, req)
			})
		}
		if ipcErr != nil {
			execLog.Warn("IPC server creation failed; in-tool LLM calls will be unavailable for this invocation",
				"error", ipcErr, "tool", entry.ToolName, "workDir", workDir)
			ipcSocket = ""
		} else if !strings.HasPrefix(ipcSocket, workDir) {
			defer func() { _ = os.Remove(ipcSocket) }()
		}
	}

	args := r.buildRunnerArgs(params, entry, artifactPaths, artifactMeta, statusPipe, resultFile, outputDir, ipcSocket, volumes)
	r.warnUnresolvedDispatchName(entry, execLog)
	argsData, marshalErr := json.Marshal(args)
	if marshalErr != nil {
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		execLog.Error("sandbox setup: marshaling runner_args.json failed", "error", marshalErr)
		return nil, newInvokeError(remotetool.ErrInternalError, "marshal runner_args.json: %v", marshalErr)
	}
	if writeErr := os.WriteFile(argsFile, argsData, 0o600); writeErr != nil {
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		execLog.Error("sandbox setup: writing runner_args.json failed", "error", writeErr)
		return nil, newInvokeError(remotetool.ErrInternalError, "write runner_args.json: %v", writeErr)
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	var ipcWg sync.WaitGroup
	if ipcServer != nil {
		ipcWg.Go(func() { ipcServer.Serve(execCtx) })
		defer func() {
			cancel()
			ipcWg.Wait()
		}()
	}

	cmd, buildErr := r.buildCommand(execCtx, entry, []string{argsFile}, workDir, volumes, proxyURL, execLog)
	if buildErr != nil {
		execLog.Error("sandbox setup: build command failed", "error", buildErr)
		return nil, newInvokeError(remotetool.ErrInternalError, "build sandbox command: %v", buildErr)
	}
	defer func() {
		for _, f := range cmd.ExtraFiles {
			_ = f.Close()
		}
	}()
	applyGroupCancel(cmd)
	stderr := newTailBuffer(stderrCaptureMaxBytes)
	cmd.Stderr = stderr
	stdout := &discardCounter{}
	cmd.Stdout = stdout

	statusDone := make(chan struct{})
	var statusWg sync.WaitGroup
	statusWg.Go(func() {
		readStatusPipe(statusPipe, statusDone, statusCb, r.logger)
	})
	stopStatusReader := sync.OnceFunc(func() {
		close(statusDone)
		statusWg.Wait()
	})
	defer stopStatusReader()

	execLog.Debug("executing tool", "workDirName", filepath.Base(workDir))

	r.metrics.recordSetupDuration(time.Since(start)-computeWait, entry.ToolName)
	execStart := time.Now()
	beginChildStart()
	var memCall *memoryCall
	runErr := cmd.Start()
	if runErr != nil {
		abortChildStart()
	} else {
		pid := cmd.Process.Pid
		trackChild(pid)
		r.cpuWatch.watch(pid, lease)
		memCall = r.memGuard.track(pid, entry.ToolName, execLog)
		lease.memoryAdmitted()
		runErr = cmd.Wait()
		r.memGuard.untrack(pid)
		r.cpuWatch.unwatch(pid)
		untrackChild(pid)
	}
	lease.YieldCompute()
	timedOut := execCtx.Err() == context.DeadlineExceeded
	execElapsed := time.Since(execStart)

	killAndReapGroup(cmd, execLog)

	stopStatusReader()

	execStatus := "ok"
	if timedOut {
		execStatus = "timeout"
	} else if runErr != nil {
		execStatus = "error"
	}
	r.metrics.recordExecDuration(execElapsed, entry.ToolName, execStatus)

	execTime := (time.Since(start) - computeWait).Milliseconds()

	redactedStderr := redactSensitive(stderr.String())

	logCapturedStderr(execLog, entry.isOurs(), redactedStderr, stderr.Dropped())
	if stdout.bytes > 0 {
		execLog.Debug("tool stdout discarded", "bytes", stdout.bytes)
	}

	if memCall != nil && memCall.killed.Load() && runErr != nil {
		return nil, &remotetool.InvokeError{
			Code: remotetool.ErrToolMemoryExceeded,
			Message: fmt.Sprintf("tool %q was stopped because the tool runtime was close to its memory limit "+
				"and this call was using the most memory. If other calls were running, run fewer at the same "+
				"time; if it ran alone, it needs more memory than the tool runtime has", entry.ToolName),
			StderrTail: stderrTail(redactedStderr),
		}
	}

	if timedOut {
		execLog.Warn("tool execution timed out; subprocess killed", "elapsed_ms", execTime)
		return nil, &remotetool.InvokeError{
			Code:       remotetool.ErrToolTimeout,
			Message:    redactSensitive(fmt.Sprintf("tool %q timed out after %ds", entry.ToolName, timeout)),
			StderrTail: stderrTail(redactedStderr),
			StackTrace: extractStackTrace(redactedStderr),
		}
	}

	if runErr != nil {
		return nil, classifyRunError(entry.ToolName, runErr, redactedStderr)
	}

	return r.finishResult(ctx, toolCall{params: params, entry: entry, artifactSvc: artifactSvc, log: execLog}, resultFile, outputDir, execTime)
}

type toolCall struct {
	params      *remotetool.InvokeParams
	entry       *ManifestEntry
	artifactSvc *artifact.ScopedService
	log         *slog.Logger
}

func (r *SandboxRunner) finishResult(
	ctx context.Context,
	call toolCall,
	resultFile, outputDir string,
	execTime int64,
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	params, entry, artifactSvc, execLog := call.params, call.entry, call.artifactSvc, call.log
	resultData, err := os.ReadFile(resultFile)
	if err != nil {
		execLog.Error("subprocess exited without result.json", append(userDataFailureAttrs(err), "elapsed_ms", execTime)...)
		return nil, newInvokeError(remotetool.ErrExecutionError, "reading result.json: %v", err)
	}

	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		execLog.Error("result.json parse failed", append(userDataFailureAttrs(err), "elapsed_ms", execTime)...)
		return nil, newInvokeError(remotetool.ErrExecutionError, "parsing result.json: %v", err)
	}

	if result.Error != "" {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrToolError,
			Message: result.Error,
		}
	}

	toolResult := r.normalizeToolResult(result.Result)
	declaredMeta := extractDeclaredMeta(toolResult)

	spillOversizedResult(ctx, toolResult, artifactSvc,
		params.AppName, params.UserID, params.SessionID,
		entry.ToolName, r.cfg.ToolOutputLLMReturnMaxBytes, execLog)

	createdArtifacts, failedArtifacts, collectErr := r.collectOutputArtifacts(ctx, params, artifactSvc, outputDir, declaredMeta, execLog)
	if collectErr != nil {
		execLog.Error("failed to collect output artifacts", userDataFailureAttrs(collectErr)...)
	}
	if len(failedArtifacts) > 0 {
		markEnvelopeArtifactFailure(toolResult, failedArtifacts)
	}

	return &remotetool.InvokeResult{
		ToolResult:       toolResult,
		ExecutionTimeMs:  execTime,
		TimedOut:         false,
		CreatedArtifacts: createdArtifacts,
	}, nil
}

func markEnvelopeArtifactFailure(toolResult map[string]any, failed []failedOutputArtifact) {
	if toolResult == nil || len(failed) == 0 {
		return
	}
	if s, _ := toolResult["status"].(string); s == "" || s == "success" {
		toolResult["status"] = "partial"
	}
	names := make([]string, 0, len(failed))
	for _, f := range failed {
		names = append(names, f.Filename)
	}
	note := fmt.Sprintf("Failed to save artifact(s) to the store: %s. The data was not persisted.",
		strings.Join(names, ", "))
	if msg, _ := toolResult["message"].(string); msg != "" {
		toolResult["message"] = msg + " " + note
	} else {
		toolResult["message"] = note
	}
}

func (r *SandboxRunner) setupWorkDir() (string, error) {
	instanceDir, err := r.instanceWorkDir()
	if err != nil {
		return "", err
	}
	workDir, err := os.MkdirTemp(instanceDir, workDirPrefix+"*")
	if err != nil {
		return "", err
	}
	for _, sub := range []string{"input", "output", workDirHome, workDirTmp} {
		if err := os.MkdirAll(filepath.Join(workDir, sub), 0o700); err != nil {
			_ = forceRemoveAll(workDir)
			return "", err
		}
	}
	return workDir, nil
}

func (r *SandboxRunner) preloadArtifacts(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
	inputDir string,
) (artifactPaths map[string]string, artifactMeta map[string]map[string]any, invokeErr *remotetool.InvokeError) {
	paths := make(map[string]string)
	meta := make(map[string]map[string]any)

	if artifactSvc != nil && len(params.ArtifactReferences) > 0 {
		for paramName, ref := range params.ArtifactReferences {
			loc := artifactSvc.Resolve(params.AppName, params.UserID, params.SessionID, ref.Filename)
			var verPtr *int
			if ref.Version >= 0 {
				v := ref.Version
				verPtr = &v
			}
			data, mimeType, err := artifactSvc.Inner.Load(ctx, loc, verPtr)
			if err != nil {
				return nil, nil, newInvokeError(remotetool.ErrArtifactError, "loading artifact %q: %v", ref.Filename, err)
			}
			if data == nil {
				return nil, nil, newInvokeError(remotetool.ErrArtifactNotFound, "%s", artifactNotFoundMessage(ref))
			}
			safeBase, sanitizeErr := sanitizeInputFilename(ref.Filename)
			if sanitizeErr != nil {
				return nil, nil, newInvokeError(remotetool.ErrArtifactError, "artifact_references[%q]: %v", paramName, sanitizeErr)
			}
			localPath := filepath.Join(inputDir, safeBase)
			if collidingParam, collision := findPathCollision(paths, localPath); collision {
				return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "artifact_references[%q] resolves to same on-disk path as %q", paramName, collidingParam)
			}
			if err := os.WriteFile(localPath, data, 0o600); err != nil {
				return nil, nil, newInvokeError(remotetool.ErrArtifactError, "writing artifact %q to input dir: %v", ref.Filename, err)
			}
			if mimeType == "" {
				mimeType = guessMIMEType(ref.Filename)
			}
			paths[paramName] = localPath
			meta[paramName] = map[string]any{
				"filename":   ref.Filename,
				"mime_type":  mimeType,
				"version":    ref.Version,
				"local_path": localPath,
			}
		}
	}

	for paramName, raw := range params.PreloadedArtifacts {
		if _, conflict := paths[paramName]; conflict {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "param %q appears in both ArtifactReferences and PreloadedArtifacts; pick one", paramName)
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q]: expected object, got %T", paramName, raw)
		}
		filename, _ := obj["filename"].(string)
		if filename == "" {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q]: filename is required", paramName)
		}
		contentB64, _ := obj["content"].(string)
		if contentB64 == "" {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q]: content is required (base64)", paramName)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(contentB64)
		if decodeErr != nil {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q]: invalid base64: %v", paramName, decodeErr)
		}
		safeBase, sanitizeErr := sanitizeInputFilename(filename)
		if sanitizeErr != nil {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q]: %v", paramName, sanitizeErr)
		}
		localPath := filepath.Join(inputDir, safeBase)
		if collidingParam, collision := findPathCollision(paths, localPath); collision {
			return nil, nil, newInvokeError(remotetool.ErrInvalidRequest, "preloaded_artifacts[%q] resolves to same on-disk path as %q", paramName, collidingParam)
		}
		if err := os.WriteFile(localPath, data, 0o600); err != nil {
			return nil, nil, newInvokeError(remotetool.ErrInternalError, "writing preloaded artifact %q to input dir: %v", paramName, err)
		}
		mimeType, _ := obj["mime_type"].(string)
		if mimeType == "" {
			mimeType = guessMIMEType(filename)
		}
		paths[paramName] = localPath
		meta[paramName] = map[string]any{
			"filename":   filename,
			"mime_type":  mimeType,
			"version":    -1,
			"local_path": localPath,
		}
	}

	return paths, meta, nil
}

func sanitizeInputFilename(filename string) (string, error) {
	base := filepath.Base(filename)
	switch base {
	case "", ".", "..", "/":
		return "", fmt.Errorf("invalid filename %q: refuses to resolve to a safe basename", filename)
	}
	if strings.ContainsRune(base, 0) {
		return "", fmt.Errorf("invalid filename %q: contains NUL byte", filename)
	}
	return base, nil
}

func findPathCollision(paths map[string]string, localPath string) (string, bool) {
	for name, existing := range paths {
		if existing == localPath {
			return name, true
		}
	}
	return "", false
}

func (r *SandboxRunner) warnUnresolvedDispatchName(entry *ManifestEntry, execLog *slog.Logger) {
	if entry.BareToolName != "" || entry.ToolNameObserved {
		return
	}
	bare := bareToolNameOf(entry.ToolName)
	if bare == "" {
		return
	}
	if _, warned := r.dispatchNameWarned.LoadOrStore(entry.ToolName, struct{}{}); warned {
		return
	}
	execLog.Warn("dispatching an owner-prefixed tool name that --schema never confirmed; the subprocess may not recognise it",
		"bareHalf", bare)
}

func (r *SandboxRunner) buildRunnerArgs(
	params *remotetool.InvokeParams,
	entry *ManifestEntry,
	artifactPaths map[string]string,
	artifactMeta map[string]map[string]any,
	statusPipe, resultFile, outputDir, ipcSocket string,
	volumes []resolvedVolume,
) *runnerArgs {
	dispatchName := entry.ToolName
	if entry.BareToolName != "" {
		dispatchName = entry.BareToolName
	}
	args := &runnerArgs{
		ToolName:         dispatchName,
		Args:             params.Args,
		ToolConfig:       params.ToolConfig,
		ArtifactPaths:    artifactPaths,
		ArtifactMetadata: artifactMeta,
		StatusPipe:       statusPipe,
		ResultFile:       resultFile,
		OutputDir:        outputDir,
		IPCSocket:        ipcSocket,
		UserID:           params.UserID,
		SessionID:        params.SessionID,
		AppName:          params.AppName,
		ToolOAuthToken:   params.ToolOAuthToken,
		ToolAuthHeaders:  params.ToolAuthHeaders,
		TaskID:           params.ParentTaskID,
		A2AUserConfig:    params.A2AUserConfig,
		SearchTurn:       params.SearchTurn,
	}

	if args.Args == nil {
		args.Args = make(map[string]any)
	}
	if args.ToolConfig == nil {
		args.ToolConfig = make(map[string]any)
	}

	for _, vol := range volumes {
		mountPath := vol.binding.MountPath
		if r.cfg.Mode != "bwrap" && vol.hostPath != "" {
			mountPath = vol.hostPath
		}
		args.VolumeMounts = append(args.VolumeMounts, volumeMount{
			VolumeID:     vol.binding.VolumeID,
			MountPath:    mountPath,
			DeclaredPath: vol.binding.MountPath,
			ReadOnly:     vol.binding.ReadOnly,
		})
	}

	return args
}

func (r *SandboxRunner) startEgressProxy(params *remotetool.InvokeParams) (*egressproxy.Proxy, *remotetool.InvokeError) {
	var union []string
	var firstPolicy string
	for _, b := range params.NetworkBindings {
		if firstPolicy == "" {
			firstPolicy = b.PolicyName
		}
		union = append(union, b.Allowlist...)
	}
	policy := egressproxy.Policy{
		Allowlist:  union,
		PolicyName: firstPolicy,
	}
	proxy, err := egressproxy.Start(policy, r.logger)
	if err != nil {
		return nil, newInvokeError(remotetool.ErrInternalError, "start egress proxy: %v", err)
	}
	r.logger.Debug("started egress proxy",
		"taskID", params.TaskID,
		"tool", params.ToolName,
		"address", proxy.Address(),
		"allowlistSize", len(union),
		"firstPolicy", firstPolicy)
	return proxy, nil
}

func (r *SandboxRunner) buildCommand(ctx context.Context, entry *ManifestEntry, toolArgs []string, workDir string, volumes []resolvedVolume, proxyURL string, logger *slog.Logger) (*exec.Cmd, error) {
	switch r.cfg.Mode {
	case "bwrap":
		return r.buildBwrapCommand(ctx, entry, toolArgs, workDir, volumes, proxyURL, logger)
	case "prlimit-only":
		return r.buildPrlimitOnlyCommand(ctx, entry, toolArgs, workDir, proxyURL), nil
	default:
		return r.buildDirectCommand(ctx, entry, toolArgs, workDir, proxyURL), nil
	}
}

func containerPython() string {
	path, _ := resolveSTRPython()
	return path
}

func buildToolArgv(entry *ManifestEntry, toolArgs []string) []string {
	return toolInvocationArgv(entry.Executable, toolArgs...)
}

func toolInvocationArgv(executable string, args ...string) []string {
	if usesContainerPython(executable) {
		return append([]string{containerPython(), executable}, args...)
	}
	return append([]string{executable}, args...)
}

func usesContainerPython(executable string) bool {
	if isWindowsPythonScript(executable) {
		return true
	}
	fields := shebangFields(executable)
	if len(fields) == 0 {
		return false
	}
	if isEnvPythonShebang(fields) {
		return true
	}
	return isPythonInterpreterName(fields[0]) && !interpreterExists(fields[0])
}

func isEnvPythonShebang(fields []string) bool {
	return len(fields) > 1 && filepath.Base(fields[0]) == "env" && isPythonInterpreterName(fields[1])
}

func pythonShebangInterpreter(path string) string {
	fields := shebangFields(path)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func shebangFields(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	if n < 2 || buf[0] != '#' || buf[1] != '!' {
		return nil
	}
	line := string(buf[2:n])
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	return strings.Fields(line)
}

func isPythonInterpreterName(interp string) bool {
	return strings.HasPrefix(filepath.Base(interp), "python")
}

func interpreterExists(interp string) bool {
	if filepath.IsAbs(interp) {
		_, err := os.Stat(interp)
		return err == nil
	}
	_, err := exec.LookPath(interp)
	return err == nil
}

func applyDirectEnv(cmd *exec.Cmd, entry *ManifestEntry, workDir, proxyURL string) {
	_, tmp := scratchEnv(workDir)
	env := envWithPythonPath(PythonPathFor(entry.ToolDir))
	env = append(env, "_SAM_SANDBOX_LIGHT=1", "PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1",
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	if proxyURL != "" {
		proxyHostPort := strings.TrimPrefix(proxyURL, "http://")
		env = append(env,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY="+proxyHostPort,
			"no_proxy="+proxyHostPort,
		)
	}
	cmd.Env = env
	cmd.Dir = workDir
	applyProcGroup(cmd)
	hideConsoleWindow(cmd)
}

func (r *SandboxRunner) buildPrlimitOnlyCommand(ctx context.Context, entry *ManifestEntry, toolArgs []string, workDir, proxyURL string) *exec.Cmd {
	profileName := entry.SandboxProfile
	if profileName == "" {
		profileName = r.cfg.DefaultProfile
	}
	profile := GetProfile(profileName)

	argv := buildToolArgv(entry, toolArgs)

	prlimitArgs := buildPrlimitArgs(profile, entry.ResourceLimits)
	var cmd *exec.Cmd
	if len(prlimitArgs) > 0 {
		fullArgs := make([]string, 0, len(prlimitArgs)+1+len(argv))
		fullArgs = append(fullArgs, prlimitArgs...)
		fullArgs = append(fullArgs, "--")
		fullArgs = append(fullArgs, argv...)
		cmd = exec.CommandContext(ctx, r.cfg.PrlimitBin, fullArgs...)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}

	applyDirectEnv(cmd, entry, workDir, proxyURL)
	return cmd
}

func (r *SandboxRunner) buildDirectCommand(ctx context.Context, entry *ManifestEntry, toolArgs []string, workDir, proxyURL string) *exec.Cmd {
	argv := buildToolArgv(entry, toolArgs)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	applyDirectEnv(cmd, entry, workDir, proxyURL)
	return cmd
}

func federatedTokenDir(logger *slog.Logger) (string, error) {
	file := os.Getenv(llm.AzureFederatedTokenFileEnv)
	if file == "" {
		return "", nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(file))
	if err != nil {
		logger.Warn("federated token directory not masked in tool sandbox",
			"dir", filepath.Dir(file), "error", err)
		return "", nil
	}
	if dir == string(filepath.Separator) {
		return "", fmt.Errorf("%s must be in its own directory, not the filesystem root, so the tool sandbox can hide it", llm.AzureFederatedTokenFileEnv)
	}
	return dir, nil
}

const bwrapSetenv = "--setenv"

func (r *SandboxRunner) buildBwrapCommand(ctx context.Context, entry *ManifestEntry, toolArgs []string, workDir string, volumes []resolvedVolume, proxyURL string, logger *slog.Logger) (*exec.Cmd, error) {
	profileName := entry.SandboxProfile
	if profileName == "" {
		profileName = r.cfg.DefaultProfile
	}
	profile := GetProfile(profileName)

	cmdArgs := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-pid",
		"--unshare-cgroup-try",
	}

	if profile.NetworkIsolated && proxyURL == "" {
		cmdArgs = append(cmdArgs, "--unshare-net")
	} else if profile.NetworkIsolated && proxyURL != "" && r.logger != nil {
		r.logger.Warn("network binding suppresses --unshare-net; egress contained at the HTTP-proxy layer only (no kernel network-namespace isolation)",
			"tool", entry.ToolName)
	}

	cmdArgs = append(cmdArgs, bwrapFlagROBind, "/", "/")
	cmdArgs = append(cmdArgs, "--dev", "/dev")
	if r.freshProc {
		cmdArgs = append(cmdArgs, "--proc", "/proc")
	}
	cmdArgs = append(cmdArgs, bwrapFlagTmpfs, "/tmp")
	if profile.WritableVar {
		cmdArgs = append(cmdArgs, bwrapFlagTmpfs, "/var")
	}
	if !underAnyPrefix(r.cfg.WorkBaseDir, []string{"/tmp"}) {
		cmdArgs = append(cmdArgs, bwrapFlagTmpfs, filepath.Clean(r.cfg.WorkBaseDir))
	}
	cmdArgs = append(cmdArgs, bwrapFlagBind, workDir, workDir)

	cmdArgs = maskVolumeRoot(cmdArgs, r.volumeSvc)
	cmdArgs = appendVolumeMounts(cmdArgs, volumes)

	bindDests := make([]string, 0, len(volumes)+len(entry.ExtraMounts))
	for _, vol := range volumes {
		bindDests = append(bindDests, vol.binding.MountPath)
	}

	for _, em := range entry.ExtraMounts {
		hostPath, err := filepath.EvalSymlinks(em.HostPath)
		if err != nil {
			logger.Error("extra mount dropped; tool will run without it",
				"tool", entry.ToolName, "hostPath", em.HostPath,
				"mountPath", em.MountPath, "readOnly", em.ReadOnly, "error", err)
			continue
		}
		if em.ReadOnly {
			cmdArgs = append(cmdArgs, bwrapFlagROBind, hostPath, em.MountPath)
		} else {
			cmdArgs = append(cmdArgs, bwrapFlagBind, hostPath, em.MountPath)
		}
		bindDests = append(bindDests, em.MountPath)
	}

	dir, err := federatedTokenDir(logger)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", entry.ToolName, err)
	}
	if dir != "" {
		cmdArgs = append(cmdArgs, bwrapFlagTmpfs, dir, "--remount-ro", dir)
	}

	if err := checkBindMountPoints(bindDests, writableBwrapPrefixes(profile, workDir)); err != nil {
		return nil, fmt.Errorf("tool %q: %w", entry.ToolName, err)
	}

	cmdArgs = append(cmdArgs, "--chdir", workDir)

	home, tmp := scratchEnv(workDir)
	cmdArgs = append(cmdArgs, "--clearenv")
	cmdArgs = append(cmdArgs,
		bwrapSetenv, "PATH", "/usr/local/bin:/usr/bin:/bin",
		bwrapSetenv, "HOME", home,
		bwrapSetenv, "TMPDIR", tmp,
		bwrapSetenv, "TMP", tmp,
		bwrapSetenv, "TEMP", tmp,
		bwrapSetenv, "LANG", "C.UTF-8",
		bwrapSetenv, "_SAM_SANDBOX_LIGHT", "1",
		bwrapSetenv, "PYTHONDONTWRITEBYTECODE", "1",
		bwrapSetenv, "PYTHONNOUSERSITE", "1",
	)
	if hasPythonLayer(entry.ToolDir) {
		cmdArgs = append(cmdArgs, bwrapSetenv, "PYTHONPATH", PythonPathFor(entry.ToolDir))
	}

	if proxyURL != "" {
		proxyHostPort := strings.TrimPrefix(proxyURL, "http://")
		cmdArgs = append(cmdArgs,
			bwrapSetenv, "HTTP_PROXY", proxyURL,
			bwrapSetenv, "HTTPS_PROXY", proxyURL,
			bwrapSetenv, "http_proxy", proxyURL,
			bwrapSetenv, "https_proxy", proxyURL,
			bwrapSetenv, "NO_PROXY", proxyHostPort,
			bwrapSetenv, "no_proxy", proxyHostPort,
		)
	}

	var seccompFile *os.File
	if r.cfg.SeccompBindDeny {
		f, err := newSeccompFilterFile()
		if err != nil {
			return nil, fmt.Errorf("seccomp filter: %w", err)
		}
		seccompFile = f
		cmdArgs = append(cmdArgs, "--seccomp", strconv.Itoa(seccompChildFD))
	}

	cmdArgs = append(cmdArgs, "--")
	cmdArgs = append(cmdArgs, buildToolArgv(entry, toolArgs)...)

	cmd := r.wrapWithPrlimit(ctx, profile, entry.ResourceLimits, cmdArgs)
	cmd.Dir = workDir
	if seccompFile != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, seccompFile)
	}
	return cmd, nil
}

func applyGroupCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		descendants := processDescendants(pid)
		err := killProcessGroup(pid)
		killProcesses(descendants)
		return err
	}
	cmd.WaitDelay = 5 * time.Second
}

func killProcesses(pids []int) {
	for _, pid := range pids {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}

func killAndReapGroup(cmd *exec.Cmd, logger *slog.Logger) {
	if cmd.Process == nil {
		return
	}
	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		logger.Debug("killProcessGroup", "pid", cmd.Process.Pid, "error", err)
	}
	reapProcessGroup(cmd.Process.Pid)
}

func (r *SandboxRunner) wrapWithPrlimit(ctx context.Context, profile SandboxProfile, overrides *ResourceLimits, bwrapArgs []string) *exec.Cmd {
	prlimitArgs := buildPrlimitArgs(profile, overrides)
	var cmd *exec.Cmd
	if len(prlimitArgs) > 0 {
		fullArgs := make([]string, 0, len(prlimitArgs)+1+1+len(bwrapArgs))
		fullArgs = append(fullArgs, prlimitArgs...)
		fullArgs = append(fullArgs, "--", r.cfg.BwrapBin)
		fullArgs = append(fullArgs, bwrapArgs...)
		cmd = exec.CommandContext(ctx, r.cfg.PrlimitBin, fullArgs...)
	} else {
		cmd = exec.CommandContext(ctx, r.cfg.BwrapBin, bwrapArgs...)
	}
	cmd.Env = wrapperEnv()
	applyProcGroup(cmd)
	hideConsoleWindow(cmd)
	return cmd
}

func wrapperEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH")}
}

type declaredArtifactMeta struct {
	MIMEType    string
	Description string
	Tags        []string
}

func (r *SandboxRunner) collectOutputArtifacts(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
	outputDir string,
	declaredMeta map[string]declaredArtifactMeta,
	logger *slog.Logger,
) ([]remotetool.CreatedArtifact, []failedOutputArtifact, error) {
	if artifactSvc == nil {
		return nil, nil, nil
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read output artifact dir: %w", err)
	}

	var created []remotetool.CreatedArtifact
	var failed []failedOutputArtifact
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		filePath := filepath.Join(outputDir, e.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			logger.Error("reading output artifact", outputArtifactFailureAttrs(e.Name(), err)...)
			failed = append(failed, failedOutputArtifact{Filename: e.Name(), Err: err.Error()})
			continue
		}
		info, _ := e.Info()
		meta := declaredMeta[e.Name()]
		mimeType := meta.MIMEType
		if mimeType == "" {
			mimeType = guessMIMEType(e.Name())
		}
		saveRes, saveErr := artifact.SaveWithMetadata(ctx, artifactSvc,
			params.AppName, params.UserID, params.SessionID, e.Name(),
			data, mimeType,
			artifact.MetadataOpts{
				Description: meta.Description,
				Tags:        meta.Tags,
			}, r.logger)
		if saveErr != nil {
			logger.Error("saving output artifact", outputArtifactFailureAttrs(e.Name(), saveErr)...)
			failed = append(failed, failedOutputArtifact{Filename: e.Name(), Err: saveErr.Error()})
			continue
		}

		ca := remotetool.CreatedArtifact{
			Filename: e.Name(),
			Version:  saveRes.DataVersion,
			MIMEType: mimeType,
		}
		if info != nil {
			ca.SizeBytes = info.Size()
		}
		created = append(created, ca)
	}

	return created, failed, nil
}

type failedOutputArtifact struct {
	Filename string
	Err      string
}

func extractDeclaredMeta(result map[string]any) map[string]declaredArtifactMeta {
	if result == nil {
		return nil
	}
	raw, ok := result["data_objects"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]declaredArtifactMeta, len(raw))
	for _, item := range raw {
		do, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := do["name"].(string)
		if name == "" {
			continue
		}
		entry := out[name]
		if mime, _ := do["mime_type"].(string); mime != "" {
			entry.MIMEType = mime
		}
		if desc, _ := do["description"].(string); desc != "" {
			entry.Description = desc
		}
		if rawTags, _ := do["tags"].([]any); len(rawTags) > 0 {
			for _, t := range rawTags {
				if s, ok := t.(string); ok && s != "" {
					entry.Tags = append(entry.Tags, s)
				}
			}
		}
		out[name] = entry
	}
	return out
}

func (r *SandboxRunner) normalizeToolResult(raw any) map[string]any {
	if raw == nil {
		return map[string]any{
			"text":         nil,
			"data_objects": []any{},
		}
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{
		"text":         fmt.Sprintf("%v", raw),
		"data_objects": []any{},
	}
}

const maxUnixSocketPath = 104

func (r *SandboxRunner) ipcSocketPath(workDir, taskID string) string {
	natural := filepath.Join(workDir, "ipc.sock")
	if len(natural) < maxUnixSocketPath {
		return natural
	}
	return os.TempDir() + string(filepath.Separator) + fmt.Sprintf("sam-ipc-%s.sock", taskID)
}

func (r *SandboxRunner) resolveVolumes(ctx context.Context, params *remotetool.InvokeParams, entry *ManifestEntry, logger *slog.Logger) ([]resolvedVolume, *remotetool.InvokeError) {
	timeout, _ := resolveExecTimeout(r.cfg.DefaultTimeoutSeconds, r.cfg.MaxTimeoutSeconds, entry, params)
	lockDuration := time.Duration(timeout+30) * time.Second

	var resolved []resolvedVolume
	for _, binding := range params.VolumeBindings {
		hostPath, err := r.volumeSvc.Resolve(ctx, binding.VolumeID)
		if err != nil {
			r.releaseVolumeLocks(ctx, params.TaskID, resolved, logger)
			return nil, newInvokeError(remotetool.ErrVolumeError, "resolving volume %s: %v", binding.VolumeID, err)
		}

		if mode := volumeModeFromManifest(entry, binding.MountPath); mode != "" {
			binding.ReadOnly = mode == volumeModeReadOnly
		}

		rv := resolvedVolume{
			binding:  binding,
			hostPath: hostPath,
		}

		if !binding.ReadOnly {
			if err := r.volumeSvc.AcquireWriteLock(ctx, binding.VolumeID, params.TaskID, lockDuration); err != nil {
				r.releaseVolumeLocks(ctx, params.TaskID, resolved, logger)
				code := remotetool.ErrVolumeError
				if errors.Is(err, volume.ErrLocked) {
					code = remotetool.ErrVolumeLocked
				}
				return nil, newInvokeError(code, "locking volume %s: %v", binding.VolumeID, err)
			}
			rv.locked = true
		}

		_ = r.volumeSvc.RecordAccess(ctx, binding.VolumeID)

		postureLog := r.logger.Debug
		if binding.AllowExec {
			postureLog = r.logger.Info
		}
		postureLog("volume mount posture",
			"taskID", params.TaskID,
			"tool", params.ToolName,
			"volumeID", binding.VolumeID,
			"mountPath", binding.MountPath,
			"readonly", binding.ReadOnly,
			"allowExec", binding.AllowExec,
			"enforced", false)

		resolved = append(resolved, rv)
	}
	return resolved, nil
}

const volumeModeReadOnly = "readonly"

func volumeModeFromManifest(entry *ManifestEntry, mountPath string) string {
	if entry == nil {
		return ""
	}
	for _, vp := range entry.VolumeParams {
		if vp.MountPath == mountPath {
			return vp.Mode
		}
	}
	return ""
}

func (r *SandboxRunner) releaseVolumeLocks(ctx context.Context, taskID string, volumes []resolvedVolume, logger *slog.Logger) {
	for _, vol := range volumes {
		if !vol.locked {
			continue
		}
		if err := r.volumeSvc.ReleaseWriteLock(ctx, vol.binding.VolumeID, taskID); err != nil {
			logger.Warn("failed to release volume lock",
				"volumeID", vol.binding.VolumeID,
				"taskID", taskID,
				"error", err,
			)
		}
	}
}

func buildPrlimitArgs(profile SandboxProfile, overrides *ResourceLimits) []string {
	cpuSec := profile.MaxCPUSeconds
	fsizeMB := profile.MaxFileSizeMB
	nofile := profile.MaxOpenFiles
	nproc := profile.MaxProcesses
	stackMB := profile.MaxStackSizeMB

	if overrides != nil {
		if overrides.MaxCPUSeconds > 0 {
			cpuSec = overrides.MaxCPUSeconds
		}
		if overrides.MaxFileSizeMB > 0 {
			fsizeMB = overrides.MaxFileSizeMB
		}
		if overrides.MaxOpenFiles > 0 {
			nofile = overrides.MaxOpenFiles
		}
		if overrides.MaxProcesses > 0 {
			nproc = overrides.MaxProcesses
		}
		if overrides.MaxStackSizeMB > 0 {
			stackMB = overrides.MaxStackSizeMB
		}
	}

	if cpuSec == 0 && fsizeMB == 0 && nofile == 0 && nproc == 0 && stackMB == 0 {
		return nil
	}

	const mb = 1024 * 1024

	var args []string
	if cpuSec > 0 {
		args = append(args, fmt.Sprintf("--cpu=%d", cpuSec))
	}
	if fsizeMB > 0 {
		args = append(args, fmt.Sprintf("--fsize=%d", fsizeMB*mb))
	}
	if nofile > 0 {
		args = append(args, fmt.Sprintf("--nofile=%d:%d", nofile, nofile))
	}
	if nproc > 0 {
		args = append(args, fmt.Sprintf("--nproc=%d:%d", nproc, nproc))
	}
	if stackMB > 0 {
		args = append(args, fmt.Sprintf("--stack=%d", stackMB*mb))
	}

	return args
}

type volumeRooter interface {
	Root() string
}

func maskVolumeRoot(cmdArgs []string, svc volume.Service) []string {
	rooter, ok := svc.(volumeRooter)
	if !ok {
		return cmdArgs
	}
	root := filepath.Clean(rooter.Root())
	if rooter.Root() == "" || root == "/" || underAnyPrefix(root, []string{"/tmp"}) {
		return cmdArgs
	}
	return append(cmdArgs, bwrapFlagTmpfs, root)
}

func appendVolumeMounts(cmdArgs []string, volumes []resolvedVolume) []string {
	for _, vol := range volumes {
		if vol.binding.ReadOnly {
			cmdArgs = append(cmdArgs, bwrapFlagROBind, vol.hostPath, vol.binding.MountPath)
		} else {
			cmdArgs = append(cmdArgs, bwrapFlagBind, vol.hostPath, vol.binding.MountPath)
		}
	}
	return cmdArgs
}

func writableBwrapPrefixes(profile SandboxProfile, workDir string) []string {
	prefixes := []string{"/tmp", "/dev", "/proc", workDir}
	if profile.WritableVar {
		prefixes = append(prefixes, "/var")
	}
	return prefixes
}

func checkBindMountPoints(dests []string, writablePrefixes []string) error {
	for _, dest := range dests {
		if dest == "" || underAnyPrefix(dest, writablePrefixes) {
			continue
		}
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %q (pre-create it in the STR image's mkdir -p, or mount it under /tmp)", errBwrapMountPointMissing, dest)
		}
	}
	return nil
}

func underAnyPrefix(path string, prefixes []string) bool {
	clean := filepath.Clean(path)
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if clean == p || strings.HasPrefix(clean, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func guessMIMEType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".py":
		return "text/x-python"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}

func artifactNotFoundMessage(ref remotetool.ArtifactReference) string {
	if ref.Version < 0 {
		return fmt.Sprintf(remotetool.ArtifactLatestNotFoundFormat, ref.Filename)
	}
	return fmt.Sprintf(remotetool.ArtifactVersionNotFoundFormat, ref.Filename, ref.Version)
}
