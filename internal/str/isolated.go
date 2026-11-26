// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const sandboxModeKubernetesJob = "kubernetes-job"

const (
	backendSubprocess    = "subprocess"
	backendKubernetesJob = sandboxModeKubernetesJob

	isolatedPollInterval     = 5 * time.Second
	isolatedFastPollInterval = 500 * time.Millisecond
	isolatedWaitGrace        = 15 * time.Second
	isolatedFollowDrain      = 10 * time.Second
	isolatedCleanupTimeout   = 15 * time.Second
	isolatedSweepAge         = time.Hour

	isolatedImagePullGrace = 30 * time.Second

	podImageNotPulled = "the pod's image could not be pulled"

	isolatedMaxResultBytes = 8 << 20

	isolatedMaxLogLineBytes = isolatedMaxResultBytes
)

var (
	errIsolatedInputArtifacts            = errors.New("isolated execution does not support input artifacts yet")
	errIsolatedVolumes                   = errors.New("isolated execution does not support volume bindings yet")
	errIsolatedNetworkBindings           = errors.New("isolated execution does not support network bindings yet")
	errIsolatedNonNativeExecutable       = errors.New("isolated execution supports native executables only, not scripts")
	errIsolatedExecutableOutsideToolsets = errors.New("isolated execution needs the tool executable under the toolsets dir the STR serves")
	errIsolatedOutputArtifacts           = errors.New("isolated execution does not support output artifacts yet")
	errIsolatedJobVanished               = errors.New("isolated job disappeared while it was awaited")
	errIsolatedResultTooLarge            = fmt.Errorf("isolated execution cannot return a result larger than %d MiB", isolatedMaxResultBytes>>20)
	errIsolatedResultUnread              = errors.New("isolated job finished but its result could not be read from the pod log")
)

var podStartBlockers = map[string]string{
	"ImagePullBackOff":           podImageNotPulled,
	"ErrImagePull":               podImageNotPulled,
	"ErrImageNeverPull":          podImageNotPulled,
	"InvalidImageName":           podImageNotPulled,
	"CreateContainerConfigError": "the pod's container could not be configured",
}

type isolatedJobFailure struct {
	Reason   string
	Detail   string
	LogTail  string
	Deadline bool

	Startup bool

	PodLost bool
}

func (e *isolatedJobFailure) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

type isolatedOutcome struct {
	Result      []byte
	OutputFiles int
}

type isolatedBackend struct {
	cfg              IsolatedConfig
	kube             *kubeClient
	logger           *slog.Logger
	metrics          *strMetrics
	pollInterval     time.Duration
	fastPollInterval time.Duration
	imagePullGrace   time.Duration
}

func newIsolatedBackend(cfg IsolatedConfig, logger *slog.Logger, metrics *strMetrics) (*isolatedBackend, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	kube, err := newInClusterKubeClient()
	if err != nil {
		return nil, err
	}
	if cfg.Image == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		image, err := kube.ownImage(ctx, readPodNamespace(), os.Getenv("HOSTNAME"), isolatedOwnContainerName)
		if err != nil {
			return nil, fmt.Errorf("STR_JOB_IMAGE is unset and this pod's image could not be read: %w", err)
		}
		cfg.Image = image
	}
	cfg.NodeSelector = withOwnArch(cfg.NodeSelector)
	return &isolatedBackend{
		cfg:              cfg,
		kube:             kube,
		logger:           logger,
		metrics:          metrics,
		pollInterval:     isolatedPollInterval,
		fastPollInterval: isolatedFastPollInterval,
		imagePullGrace:   isolatedImagePullGrace,
	}, nil
}

func startIsolatedBackend(cfg SandboxConfig, logger *slog.Logger, metrics *strMetrics) (*isolatedBackend, error) {
	if cfg.Mode != sandboxModeKubernetesJob {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	backend, err := newIsolatedBackend(cfg.Isolated, logger, metrics)
	if err != nil {
		return nil, fmt.Errorf("kubernetes-job backend: %w", err)
	}
	logger.Info("kubernetes-job backend enabled",
		"namespace", backend.cfg.Namespace, "image", backend.cfg.Image, "runtimeClass", backend.cfg.RuntimeClass,
		"nodeSelector", backend.cfg.NodeSelector, "startTimeout_s", int(backend.cfg.startTimeout().Seconds()))
	return backend, nil
}

func rejectIsolatedUnsupported(params *remotetool.InvokeParams, entry *ManifestEntry) error {
	switch {
	case len(params.ArtifactReferences) > 0 || len(params.PreloadedArtifacts) > 0:
		return errIsolatedInputArtifacts
	case len(params.VolumeBindings) > 0:
		return errIsolatedVolumes
	case len(params.NetworkBindings) > 0:
		return errIsolatedNetworkBindings
	case len(shebangFields(entry.Executable)) > 0 || usesContainerPython(entry.Executable):
		return errIsolatedNonNativeExecutable
	}
	return nil
}

func (r *SandboxRunner) executeIsolated(
	ctx context.Context,
	call toolCall,
	statusCb func(string),
	workDir string,
	timeout int,
	inflightWait time.Duration,
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	params, entry, execLog := call.params, call.entry, call.log
	start := time.Now()
	r.metrics.recordSemaphoreWait(inflightWait, backendKubernetesJob)

	if err := rejectIsolatedUnsupported(params, entry); err != nil {
		execLog.Warn("isolated execution refused", "error", err)
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: err.Error()}
	}
	toolURL, err := isolatedToolURL(r.cfg.Isolated.PackageURL, r.cfg.Isolated.ToolsetsDir, entry.Executable)
	if err != nil {
		execLog.Warn("isolated execution refused", "error", err)
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: err.Error()}
	}

	b := r.isolated
	r.warnUnresolvedDispatchName(entry, execLog)
	args := r.buildRunnerArgs(params, entry, nil, nil, "", "", "", "", nil)
	argsData, err := marshalIsolatedArgs(args)
	if err != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInternalError, Message: err.Error()}
	}

	inv := isolatedInvocation{
		jobName:        isolatedJobName(b.cfg.Namespace, entry.ToolName, params.TaskID),
		hash:           isolatedInvocationHash(b.cfg.Namespace, entry.ToolName, params.TaskID),
		tool:           entry.ToolName,
		timeoutSeconds: timeout,
		resources:      b.cfg.Resources.withDefaults(),
		toolURL:        toolURL,
		runnerArgs:     argsData,

		deadlineSeconds: timeout + int(b.cfg.startTimeout().Seconds()),
	}
	jobLog := execLog.With("backend", backendKubernetesJob, "job", inv.jobName, "namespace", b.cfg.Namespace)

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(inv.deadlineSeconds)*time.Second+isolatedWaitGrace)
	defer cancel()

	execStart := time.Now()
	outcome, runErr := b.run(execCtx, inv, statusCb, jobLog)
	execElapsed := time.Since(execStart)

	timedOut := isolatedTimedOut(runErr) || (runErr != nil && errors.Is(execCtx.Err(), context.DeadlineExceeded))
	status := "ok"
	switch {
	case runErr == nil:
	case timedOut:
		status = "timeout"
	default:
		status = "error"
	}
	r.metrics.recordExecDuration(execElapsed, entry.ToolName, status)

	if runErr != nil {
		return nil, isolatedInvokeError(entry.ToolName, timeout, runErr, timedOut, entry.isOurs(), jobLog)
	}
	if outcome.OutputFiles > 0 {
		jobLog.Warn("isolated tool produced output files that cannot be collected", "files", outcome.OutputFiles)
		return nil, newInvokeError(remotetool.ErrExecutionError, "tool %q: %v (%d file(s) written)", entry.ToolName, errIsolatedOutputArtifacts, outcome.OutputFiles)
	}

	resultFile := filepath.Join(workDir, "result.json")
	if len(outcome.Result) > 0 {
		if writeErr := os.WriteFile(resultFile, outcome.Result, 0o600); writeErr != nil {
			jobLog.Error("writing isolated result.json failed", "error", writeErr)
			return nil, newInvokeError(remotetool.ErrInternalError, "write result.json: %v", writeErr)
		}
	}
	execTime := time.Since(start).Milliseconds()
	return r.finishResult(ctx, toolCall{params: params, entry: entry, artifactSvc: call.artifactSvc, log: jobLog}, resultFile, filepath.Join(workDir, "output"), execTime)
}

func isolatedFailureAttrs(ours bool, failure *isolatedJobFailure) []any {
	if ours {
		return []any{"reason", failure.Reason, "detail", failure.Detail}
	}
	return []any{"reasonLen", len(failure.Reason), "detailLen", len(failure.Detail)}
}

func isolatedTimedOut(err error) bool {
	var failure *isolatedJobFailure
	return errors.As(err, &failure) && failure.Deadline
}

func isolatedInvokeError(tool string, timeout int, err error, timedOut bool, ours bool, log *slog.Logger) *remotetool.InvokeError {
	var failure *isolatedJobFailure
	switch {
	case timedOut:
		log.Warn("isolated job timed out", "timeout_s", timeout)
		invokeErr := &remotetool.InvokeError{
			Code:    remotetool.ErrToolTimeout,
			Message: redactSensitive(fmt.Sprintf("tool %q timed out after %ds", tool, timeout)),
		}
		if errors.As(err, &failure) {
			invokeErr.StderrTail = stderrTail(redactSensitive(failure.LogTail))
		}
		return invokeErr
	case errors.Is(err, context.Canceled):
		return &remotetool.InvokeError{Code: remotetool.ErrSandboxFailed, Message: fmt.Sprintf("tool %q was cancelled", tool)}
	case errors.Is(err, errIsolatedResultTooLarge):
		log.Warn("isolated job result is too large to return")
		return &remotetool.InvokeError{Code: remotetool.ErrExecutionError, Message: fmt.Sprintf("tool %q: %v", tool, err)}
	case errors.As(err, &failure) && failure.Startup:
		log.Warn("isolated job pod did not start", isolatedFailureAttrs(ours, failure)...)
		return &remotetool.InvokeError{
			Code:    remotetool.ErrSandboxFailed,
			Message: redactSensitive(fmt.Sprintf("tool %q could not start in its isolated job: %s", tool, failure.Error())),
		}
	case errors.As(err, &failure) && failure.PodLost:
		log.Warn("isolated job pod was lost", isolatedFailureAttrs(ours, failure)...)
		return &remotetool.InvokeError{
			Code:    remotetool.ErrSandboxFailed,
			Message: redactSensitive(fmt.Sprintf("tool %q: the pod of its isolated job was lost: %s", tool, failure.Detail)),
		}
	case errors.As(err, &failure):
		log.Warn("isolated job failed", "reason", failure.Reason)
		tail := redactSensitive(failure.LogTail)
		return &remotetool.InvokeError{
			Code:       remotetool.ErrToolExitNonzero,
			Message:    redactSensitive(fmt.Sprintf("tool %q failed in its isolated job: %s", tool, failure.Error())),
			StderrTail: stderrTail(tail),
			StackTrace: extractStackTrace(tail),
		}
	default:
		log.Error("isolated job could not be run", failureLogAttrs(ours, err)...)
		return &remotetool.InvokeError{
			Code:    remotetool.ErrSandboxFailed,
			Message: redactSensitive(fmt.Sprintf("tool %q: isolated job: %v", tool, err)),
		}
	}
}

func (b *isolatedBackend) run(ctx context.Context, inv isolatedInvocation, statusCb func(string), log *slog.Logger) (*isolatedOutcome, error) {
	createStart := time.Now()
	job, adopted, err := b.createOrAdopt(ctx, inv, log)
	if err != nil {
		var refused *kubeStatusError
		if !errors.As(err, &refused) {
			b.cleanup(inv, log)
		}
		return nil, err
	}
	defer b.cleanup(inv, log)

	if err := b.ensureSecret(ctx, inv, job.Metadata.UID); err != nil {
		return nil, err
	}
	return b.await(ctx, inv, job, createStart, adopted, statusCb, log)
}

func (b *isolatedBackend) createOrAdopt(ctx context.Context, inv isolatedInvocation, log *slog.Logger) (*kubeJob, bool, error) {
	const attempts = 2
	for range attempts {
		job, err := b.kube.createJob(ctx, b.cfg.Namespace, buildIsolatedJob(b.cfg, inv))
		if err == nil {
			log.Debug("isolated job created")
			return job, false, nil
		}
		if !errors.Is(err, errKubeAlreadyExists) {
			return nil, false, fmt.Errorf("create job: %w", err)
		}
		existing, getErr := b.kube.getJob(ctx, b.cfg.Namespace, inv.jobName)
		if errors.Is(getErr, errKubeNotFound) {
			continue
		}
		if getErr != nil {
			return nil, false, fmt.Errorf("get existing job: %w", getErr)
		}
		_, _, finished := jobOutcome(existing)
		log.Info("adopting isolated job created by an earlier delivery",
			"jobAge_s", int(time.Since(existing.Metadata.CreationTimestamp).Seconds()), "finished", finished)
		return existing, true, nil
	}
	return nil, false, errors.New("job kept appearing and disappearing while creating it")
}

func (b *isolatedBackend) ensureSecret(ctx context.Context, inv isolatedInvocation, jobUID string) error {
	err := b.kube.createSecret(ctx, b.cfg.Namespace, buildIsolatedSecret(b.cfg.Namespace, inv, jobUID))
	if err != nil && !errors.Is(err, errKubeAlreadyExists) {
		return fmt.Errorf("create secret: %w", err)
	}
	return nil
}

func (b *isolatedBackend) cleanup(inv isolatedInvocation, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), isolatedCleanupTimeout)
	defer cancel()
	if err := b.kube.deleteJob(ctx, b.cfg.Namespace, inv.jobName); err != nil {
		log.Warn("deleting isolated job failed; its TTL and the start-up sweep will remove it", "error", err)
	}
	if err := b.kube.deleteSecret(ctx, b.cfg.Namespace, inv.jobName); err != nil {
		log.Warn("deleting isolated job secret failed; it is garbage-collected with the job", "error", err)
	}
}

func (b *isolatedBackend) await(
	ctx context.Context,
	inv isolatedInvocation,
	job *kubeJob,
	createStart time.Time,
	adopted bool,
	statusCb func(string),
	log *slog.Logger,
) (*isolatedOutcome, error) {
	followCtx, stopFollow := context.WithCancel(ctx)
	defer stopFollow()
	w := b.newJobWatch(inv, job.Metadata.UID, statusCb, log)
	defer w.stop()

	for {
		current, err := b.kube.getJob(ctx, b.cfg.Namespace, inv.jobName)
		switch {
		case errors.Is(err, errKubeNotFound):
			return nil, errIsolatedJobVanished
		case err != nil:
			log.Warn("isolated job status poll failed", "error", err)
		default:
			job = current
		}
		if _, _, finished := jobOutcome(job); finished {
			break
		}
		w.refollowIfCut()
		if err := w.observePod(ctx, followCtx); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w.startTimer.C:
			if err := w.startExpired(ctx); err != nil {
				return nil, err
			}
		case <-w.toolDeadline:
			if err := w.deadlineExpired(ctx, stopFollow); err != nil {
				return nil, err
			}
		case <-w.ticker.C:
		case <-w.wake:
			w.wake = nil
			w.ticker.Reset(b.fastPollInterval)
		}
	}
	done := time.Since(createStart)

	pod, _ := w.pod(ctx)
	parser := b.collectLogs(ctx, pod, w.follower, stopFollow, log)
	b.recordTimings(inv, createStart, done, pod, adopted, log)

	succeeded, cond, _ := jobOutcome(job)
	if !succeeded {
		return nil, describeJobFailure(cond, pod, parser)
	}
	if !parser.sawEnd {
		return nil, errIsolatedResultUnread
	}
	if parser.resultTooLarge {
		return nil, errIsolatedResultTooLarge
	}
	return &isolatedOutcome{Result: parser.resultBytes(), OutputFiles: parser.outputFiles}, nil
}

type jobWatch struct {
	b        *isolatedBackend
	inv      isolatedInvocation
	selector string
	jobUID   string
	statusCb func(string)
	log      *slog.Logger

	follower      *logFollower
	followStarted time.Time
	relayed       int
	blockedSince  time.Time

	ticker       *time.Ticker
	startTimer   *time.Timer
	toolTimer    *time.Timer
	wake         <-chan struct{}
	toolDeadline <-chan time.Time
}

func (b *isolatedBackend) newJobWatch(inv isolatedInvocation, jobUID string, statusCb func(string), log *slog.Logger) *jobWatch {
	return &jobWatch{
		b:          b,
		inv:        inv,
		selector:   "job-name=" + inv.jobName,
		jobUID:     jobUID,
		statusCb:   statusCb,
		log:        log,
		ticker:     time.NewTicker(b.pollInterval),
		startTimer: time.NewTimer(b.cfg.startTimeout()),
	}
}

func (w *jobWatch) stop() {
	w.ticker.Stop()
	w.startTimer.Stop()
	if w.toolTimer != nil {
		w.toolTimer.Stop()
	}
}

func (w *jobWatch) pod(ctx context.Context) (pod *kubePod, listed bool) {
	return w.b.jobPod(ctx, w.selector, w.jobUID, w.log)
}

func (w *jobWatch) refollowIfCut() {
	if w.follower == nil || !w.follower.cut() || time.Since(w.followStarted) < w.b.pollInterval {
		return
	}
	w.log.Warn("isolated job pod log stream ended before the tool did; following it again")
	w.relayed = max(w.relayed, w.follower.parser.statusLines)
	w.follower, w.wake = nil, nil
	w.ticker.Reset(w.b.pollInterval)
}

func (w *jobWatch) observePod(ctx, followCtx context.Context) error {
	if w.follower != nil {
		return nil
	}
	pod, listed := w.pod(ctx)
	if !listed {
		return nil
	}
	if pod != nil && podHasStarted(pod) {
		w.follow(followCtx, pod)
		return nil
	}
	blocker := podStartBlocker(pod)
	if blocker == "" {
		w.blockedSince = time.Time{}
		return nil
	}
	if w.blockedSince.IsZero() {
		w.blockedSince = time.Now()
	}
	if time.Since(w.blockedSince) < w.b.imagePullGrace {
		return nil
	}
	return podStartFailure(pod, blocker)
}

func (w *jobWatch) follow(followCtx context.Context, pod *kubePod) {
	w.startTimer.Stop()
	first := w.followStarted.IsZero()
	w.follower = w.b.startFollower(followCtx, pod.Metadata.Name, w.relayed, w.statusCb, w.log)
	w.followStarted = time.Now()
	if !first {
		return
	}
	w.wake = w.follower.wake
	if left, running := toolTimeLeft(pod, w.inv.timeoutSeconds); running {
		w.toolTimer = time.NewTimer(left)
		w.toolDeadline = w.toolTimer.C
	}
}

func (w *jobWatch) startExpired(ctx context.Context) error {
	pod, listed := w.pod(ctx)
	switch {
	case !listed:
		w.startTimer.Reset(w.b.pollInterval)
		return nil
	case pod != nil && podHasStarted(pod):
		return nil
	}
	return podStartFailure(pod, fmt.Sprintf("the pod did not start within %s", w.b.cfg.startTimeout()))
}

func (w *jobWatch) deadlineExpired(ctx context.Context, stopFollow context.CancelFunc) error {
	if pod, listed := w.pod(ctx); listed && podContainerExited(pod) {
		w.toolDeadline = nil
		w.ticker.Reset(w.b.fastPollInterval)
		return nil
	}
	stopFollow()
	tail := ""
	if w.follower != nil {
		<-w.follower.done
		tail = w.follower.parser.tail.String()
	}
	return &isolatedJobFailure{
		Reason:   "DeadlineExceeded",
		Detail:   "the tool ran past its timeout",
		LogTail:  tail,
		Deadline: true,
	}
}

func (b *isolatedBackend) jobPod(ctx context.Context, selector, jobUID string, log *slog.Logger) (pod *kubePod, listed bool) {
	pods, err := b.kube.listPods(ctx, b.cfg.Namespace, selector)
	if err != nil {
		log.Warn("listing isolated job pods failed", "error", err)
		return nil, false
	}
	for i := range pods {
		candidate := &pods[i]
		if !podOwnedBy(candidate, jobUID) {
			continue
		}
		if pod == nil || candidate.Metadata.CreationTimestamp.After(pod.Metadata.CreationTimestamp) {
			pod = candidate
		}
	}
	return pod, true
}

func podOwnedBy(pod *kubePod, uid string) bool {
	for _, owner := range pod.Metadata.OwnerReferences {
		if owner.UID == uid {
			return true
		}
	}
	return false
}

func podHasStarted(pod *kubePod) bool {
	switch pod.Status.Phase {
	case "Running", "Succeeded", "Failed":
		return true
	}
	return false
}

func podContainerState(pod *kubePod) *kubeContainerState {
	if pod == nil {
		return nil
	}
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == isolatedContainerName {
			return &pod.Status.ContainerStatuses[i].State
		}
	}
	return nil
}

func toolTimeLeft(pod *kubePod, timeoutSeconds int) (left time.Duration, running bool) {
	state := podContainerState(pod)
	if state != nil && state.Terminated != nil {
		return 0, false
	}
	started := time.Now()
	if state != nil && state.Running != nil && !state.Running.StartedAt.IsZero() {
		started = state.Running.StartedAt
	}
	return max(time.Until(started.Add(time.Duration(timeoutSeconds)*time.Second)), 0), true
}

func podContainerExited(pod *kubePod) bool {
	state := podContainerState(pod)
	return state != nil && state.Terminated != nil
}

func podStartBlocker(pod *kubePod) string {
	state := podContainerState(pod)
	if state == nil || state.Waiting == nil {
		return ""
	}
	return podStartBlockers[state.Waiting.Reason]
}

func podStartFailure(pod *kubePod, reason string) *isolatedJobFailure {
	failure := &isolatedJobFailure{Reason: reason, Startup: true}
	if pod == nil {
		failure.Detail = "no pod was created for the job"
		return failure
	}
	if state := podContainerState(pod); state != nil && state.Waiting != nil && state.Waiting.Reason != "" {
		failure.Detail = strings.Trim(state.Waiting.Reason+": "+state.Waiting.Message, ": ")
		return failure
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == "PodScheduled" && cond.Status == "False" {
			failure.Detail = strings.Trim(cond.Reason+": "+cond.Message, ": ")
			return failure
		}
	}
	failure.Detail = "pod phase " + pod.Status.Phase
	return failure
}

func jobOutcome(job *kubeJob) (succeeded bool, cond *kubeJobCondition, finished bool) {
	for i := range job.Status.Conditions {
		c := &job.Status.Conditions[i]
		if c.Status != "True" {
			continue
		}
		switch c.Type {
		case "Complete":
			return true, c, true
		case "Failed":
			return false, c, true
		}
	}
	if job.Status.Succeeded > 0 {
		return true, nil, true
	}
	return false, nil, false
}

func describeJobFailure(cond *kubeJobCondition, pod *kubePod, parser *isolatedLogParser) *isolatedJobFailure {
	failure := &isolatedJobFailure{Reason: "job failed", LogTail: parser.tail.String()}
	if cond != nil {
		failure.Reason = cond.Reason
		failure.Detail = cond.Message
		failure.Deadline = cond.Reason == "DeadlineExceeded"
	}
	if pod != nil && pod.Status.Reason != "" && !failure.Deadline {
		failure.PodLost = true
		failure.Detail = strings.Trim(pod.Status.Reason+": "+pod.Status.Message, ": ")
		return failure
	}
	if detail := containerDetail(podContainerState(pod)); detail != "" {
		failure.Detail = strings.Trim(failure.Detail+"; "+detail, "; ")
	}
	return failure
}

func containerDetail(state *kubeContainerState) string {
	switch {
	case state == nil:
		return ""
	case state.Terminated != nil:
		return fmt.Sprintf("container %s (exit code %d)", state.Terminated.Reason, state.Terminated.ExitCode)
	case state.Waiting != nil && state.Waiting.Reason != "":
		return "container waiting: " + state.Waiting.Reason
	}
	return ""
}

func (b *isolatedBackend) recordTimings(inv isolatedInvocation, createStart time.Time, done time.Duration, pod *kubePod, adopted bool, log *slog.Logger) {
	fields := []any{"createToDone_ms", done.Milliseconds(), "adopted", adopted}
	if state := podContainerState(pod); state != nil {
		startedAt := time.Time{}
		switch {
		case state.Running != nil:
			startedAt = state.Running.StartedAt
		case state.Terminated != nil:
			startedAt = state.Terminated.StartedAt
		}
		if !startedAt.IsZero() {
			running := max(startedAt.Sub(createStart), 0)
			fields = append(fields, "createToRunning_ms", running.Milliseconds())
			if !adopted {
				b.metrics.recordIsolatedJob("running", running, inv.tool)
			}
		}
	}
	if !adopted {
		b.metrics.recordIsolatedJob("done", done, inv.tool)
	}
	log.Info("isolated job finished", fields...)
}

func (b *isolatedBackend) collectLogs(ctx context.Context, pod *kubePod, follower *logFollower, stopFollow context.CancelFunc, log *slog.Logger) *isolatedLogParser {
	if follower != nil {
		select {
		case <-follower.done:
		case <-time.After(isolatedFollowDrain):
			stopFollow()
			<-follower.done
		}
		if follower.parser.sawEnd {
			return follower.parser
		}
	}
	parser := newIsolatedLogParser(nil, nil, b.logger)
	if pod == nil {
		return parser
	}
	stream, err := b.kube.podLogs(ctx, b.cfg.Namespace, pod.Metadata.Name, false)
	if err != nil {
		log.Warn("reading isolated job pod log failed", "error", err)
		return parser
	}
	defer func() { _ = stream.Close() }()
	parser.consume(stream)
	return parser
}

type logFollower struct {
	parser   *isolatedLogParser
	done     chan struct{}
	wake     chan struct{}
	wakeOnce sync.Once
}

func (f *logFollower) signalWake() { f.wakeOnce.Do(func() { close(f.wake) }) }

func (f *logFollower) cut() bool {
	select {
	case <-f.done:
		return !f.parser.sawEnd
	default:
		return false
	}
}

func (b *isolatedBackend) startFollower(ctx context.Context, pod string, skipStatus int, statusCb func(string), log *slog.Logger) *logFollower {
	f := &logFollower{done: make(chan struct{}), wake: make(chan struct{})}
	f.parser = newIsolatedLogParser(statusCb, f.signalWake, b.logger)
	f.parser.skipStatus = skipStatus
	go func() {
		defer close(f.done)
		defer f.signalWake()
		stream, err := b.kube.podLogs(ctx, b.cfg.Namespace, pod, true)
		if err != nil {
			log.Warn("following isolated job pod log failed", "error", err)
			return
		}
		defer func() { _ = stream.Close() }()
		f.parser.consume(stream)
	}()
	return f
}

type isolatedLogParser struct {
	onStatus    func(string)
	onResultEnd func()
	logger      *slog.Logger
	inResult    bool
	sawEnd      bool
	result      bytes.Buffer
	outputFiles int
	tail        *tailBuffer

	resultTooLarge bool

	statusLines int
	skipStatus  int
}

func newIsolatedLogParser(onStatus func(string), onResultEnd func(), logger *slog.Logger) *isolatedLogParser {
	return &isolatedLogParser{
		onStatus:    onStatus,
		onResultEnd: onResultEnd,
		logger:      logger,
		tail:        newTailBuffer(stderrCaptureMaxBytes),
	}
}

func (p *isolatedLogParser) consume(r io.Reader) {
	reader := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	tooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		switch {
		case tooLong:
		case len(line)+len(chunk) > isolatedMaxLogLineBytes:
			tooLong = true
		default:
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		switch {
		case tooLong:
			p.resultTooLarge = p.resultTooLarge || p.inResult
		case len(line) > 0:
			p.feed(string(line))
		}
		line, tooLong = line[:0], false
		if err != nil {
			return
		}
	}
}

func (p *isolatedLogParser) feed(line string) {
	trimmed := strings.TrimRight(line, "\r\n")
	switch {
	case p.inResult && trimmed == isolatedResultEnd:
		p.inResult = false
		p.sawEnd = true
		if p.onResultEnd != nil {
			p.onResultEnd()
		}
	case p.inResult:
		if p.resultTooLarge || p.result.Len()+len(trimmed) >= isolatedMaxResultBytes {
			p.resultTooLarge = true
			p.result.Reset()
			return
		}
		p.result.WriteString(trimmed)
		p.result.WriteByte('\n')
	case trimmed == isolatedResultBegin:
		p.inResult = true
		p.resultTooLarge = false
		p.result.Reset()
	case strings.HasPrefix(trimmed, isolatedStatusPrefix):
		p.statusLines++
		if p.onStatus != nil && p.statusLines > p.skipStatus {
			processStatusLine([]byte(strings.TrimPrefix(trimmed, isolatedStatusPrefix)), p.onStatus, p.logger)
		}
	case strings.HasPrefix(trimmed, isolatedOutputMarker):
		p.outputFiles, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, isolatedOutputMarker)))
	default:
		_, _ = p.tail.Write([]byte(line))
	}
}

func (p *isolatedLogParser) resultBytes() []byte {
	return bytes.TrimSpace(p.result.Bytes())
}

func (b *isolatedBackend) sweepFinishedJobs(ctx context.Context, olderThan time.Duration) {
	selector := isolatedLabelManagedBy + "=" + isolatedManagedByValue
	jobs, err := b.kube.listJobs(ctx, b.cfg.Namespace, selector)
	if err != nil {
		b.logger.Warn("isolated job sweep could not list jobs", "error", err)
		return
	}
	deleted := 0
	for i := range jobs {
		job := &jobs[i]
		finishedAt, ok := jobFinishedAt(job)
		if !ok || time.Since(finishedAt) < olderThan {
			continue
		}
		if err := b.kube.deleteJob(ctx, b.cfg.Namespace, job.Metadata.Name); err != nil {
			b.logger.Warn("isolated job sweep could not delete a job", "job", job.Metadata.Name, "error", err)
			continue
		}
		deleted++
	}
	b.logger.Info("isolated job sweep finished", "listed", len(jobs), "deleted", deleted)
}

func jobFinishedAt(job *kubeJob) (time.Time, bool) {
	if _, cond, finished := jobOutcome(job); finished {
		if cond != nil {
			return cond.LastTransitionTime, true
		}
		if job.Status.CompletionTime != nil {
			return *job.Status.CompletionTime, true
		}
	}
	return time.Time{}, false
}
