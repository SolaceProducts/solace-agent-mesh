// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

type evalRunFlags struct {
	platformTargetFlags
	timeout       time.Duration
	threshold     float64
	maxCost       string
	allowWarnings bool
	format        string
	watch         bool
	cancelOnIntr  bool
	pollEvery     time.Duration
	outputDir     string
}

func newEvalRunCmd() *cobra.Command {
	flags := &evalRunFlags{
		threshold:    1.0,
		watch:        true,
		cancelOnIntr: true,
		timeout:      30 * time.Minute,
		pollEvery:    2 * time.Second,
	}
	cmd := &cobra.Command{
		Use:   "run <experiment-name>...",
		Short: "Run experiments on the platform service and wait for results",
		Long: `Run one or more experiments on the platform service and wait for results.

Resolves each <experiment-name> on the platform, posts to the trigger
endpoint, streams per-example results as they complete, and prints a
per-evaluator summary. Exits 0 when every run's pass/fail results pass (or
each run's pass-rate >= --threshold), non-zero otherwise. With --max-cost, a
workflow run whose trials' known costs together exceed it fails too. A
trial's cost is known only when every model it used is priced and the spend
for all of its calls has been recorded; trials without a known cost are not
counted, and the command reports how many there are. Costs are read when the
run finishes, so spend recorded after that is not counted either: a pass means
the known costs are within the ceiling, not that the run cost no more.

A run completes with warnings when some trials errored or ended early (for
example, blocked by sign-in or stopped by a failed workflow node), a workflow
lost a branch but still finished, or an evaluator could not score a trial.
Such a run fails unless --allow-warnings is set. With it, the run passes or
fails on --threshold and --max-cost, and every trial that broke counts as a
failure for each evaluator that gives a pass/fail verdict: one that produced
no score or no result, and one the agent errored on even if it was scored. A
score an evaluator could not produce counts as a failure too, and a run with
no pass/fail score to check fails.

Multiple experiments run concurrently; their live output is prefixed with
the experiment name so the interleaved lines stay readable.

Each streamed per-example line starts with a result glyph:
  ✓ pass   ✗ fail   • score only, no pass/fail verdict
A score of 0.50 or higher counts as a pass.

The platform is selected with the same precedence as ` + "`sam api`" + `:
  --url > --target > --manifest > SAM_WEBUI_URL > single cached login.
Authentication mirrors ` + "`sam api`" + ` too: the bearer token comes from
SAM_AUTH_TOKEN, then SAM_PLATFORM_TOKEN, then the cached Agent Mesh access token
written by ` + "`sam auth login`" + ` (auto-refreshed on a 401).`,
		Example: `  sam eval run my-experiment
  sam eval run exp-a exp-b --threshold 0.9
  sam eval run order-handler-regression --threshold 0.9 --max-cost 2.50
  sam eval run order-handler-paths --threshold 0.9 --allow-warnings
  sam eval run my-experiment --watch=false
  sam eval run my-experiment --target dev --format json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEvalRunMulti(cmd, args, flags)
		},
	}
	flags.register(cmd)
	cmd.Flags().DurationVar(&flags.timeout, "timeout", 30*time.Minute,
		"Overall wait timeout while polling run status.")
	cmd.Flags().Float64Var(&flags.threshold, "threshold", 1.0,
		"Minimum pass-rate (0.0-1.0) to consider a run a success. Default 1.0 (every result must pass).")
	cmd.Flags().StringVar(&flags.maxCost, "max-cost", "",
		"Most a workflow run's trials may cost together, as a decimal in the pricing currency (e.g. 2.50). A run whose known costs exceed it fails; trials with no known cost are not counted. Workflow experiments with one model only; needs --watch. Unset means no ceiling.")
	cmd.Flags().BoolVar(&flags.allowWarnings, "allow-warnings", false,
		"Check a run that completed with warnings against --threshold and --max-cost instead of failing it. Each trial that errored or produced no score counts as a failure for every evaluator; a run with no pass/fail score still fails. Needs --watch.")
	addFormatFlag(cmd, &flags.format)
	cmd.Flags().BoolVar(&flags.watch, "watch", true,
		"Poll runs to completion. Set --watch=false to fire-and-forget; the command prints the run IDs and exits 0 immediately.")
	cmd.Flags().BoolVar(&flags.cancelOnIntr, "cancel-on-interrupt", true,
		"Cancel in-flight runs via the run's cancel endpoint when the user sends Ctrl-C.")
	cmd.Flags().DurationVar(&flags.pollEvery, "poll-every", 2*time.Second,
		"How often to poll run status while watching.")
	cmd.Flags().StringVarP(&flags.outputDir, "output-dir", "o", "",
		"Download each run's full per-example trace (execution data + task events) under this directory after it completes.")
	return cmd
}

type lineLogger struct {
	mu      *sync.Mutex
	w       io.Writer
	resultW io.Writer
	prefix  string
	quiet   bool
}

func (l *lineLogger) printf(format string, a ...any) {
	if l.quiet {
		return
	}
	l.write(l.w, format, a...)
}

func (l *lineLogger) result(format string, a ...any) {
	l.write(l.resultW, format, a...)
}

func (l *lineLogger) write(w io.Writer, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prefix != "" {
		_, _ = io.WriteString(w, l.prefix)
	}
	_, _ = fmt.Fprintf(w, format, a...)
}

type expResult struct {
	name        string
	run         *evalplatform.RunResponse
	results     []evalplatform.RunResultResponse
	consistency []evaluatorConsistency
	cost        *runCost
	counts      gateCounts
	err         error
}

func (f *evalRunFlags) checkFlags(cmd *cobra.Command) (*costCeiling, error) {
	if _, err := parseFormat(f.format); err != nil {
		return nil, err
	}
	ceiling, err := parseMaxCost(f.maxCost, cmd.Flags().Changed("max-cost"), f.watch)
	if err != nil {
		return nil, err
	}
	if f.allowWarnings && !f.watch {
		return nil, errors.New("--allow-warnings needs --watch: a run's outcome is checked when it finishes")
	}
	return ceiling, nil
}

func runEvalRunMulti(cmd *cobra.Command, names []string, f *evalRunFlags) error {
	ctx := commandContext(cmd)

	ceiling, err := f.checkFlags(cmd)
	if err != nil {
		return err
	}

	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}
	if ceiling != nil {
		if err := refuseExperimentsWithoutCost(ctx, c, names); err != nil {
			return err
		}
	}

	evalNames := buildEvaluatorNameMap(ctx, c)

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	var mu sync.Mutex

	if !f.watch {
		return triggerWithoutWatching(ctx, cmd, c, names, f.format)
	}

	pollCtx, cancelPoll := context.WithTimeout(ctx, f.timeout)
	defer cancelPoll()

	runs := &runRegistry{}
	registerRun := runs.add
	if f.cancelOnIntr {
		defer cancelRunsOnInterrupt(&lineLogger{mu: &mu, w: errOut}, c, runs, cancelPoll)()
	}

	multi := len(names) > 1
	results := make([]expResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		lg := &lineLogger{mu: &mu, w: errOut, resultW: out, quiet: f.format == jsonFormat}
		if multi && f.format != jsonFormat {
			lg.prefix = "[" + name + "] "
		}
		wg.Go(func() {
			results[i] = runOneExperiment(ctx, pollCtx, name, oneExpArgs{
				client: c, evalNames: evalNames, flags: f, ceiling: ceiling, lg: lg, registerRun: registerRun,
			})
		})
	}
	wg.Wait()

	if f.format == jsonFormat {
		if err := writeRunResultsJSON(out, results); err != nil {
			return err
		}
	}

	return aggregateOutcomes(results)
}

type oneExpArgs struct {
	client      *reconcile.Client
	evalNames   map[string]string
	flags       *evalRunFlags
	ceiling     *costCeiling
	lg          *lineLogger
	registerRun func(string)
}

func runOneExperiment(ctx, pollCtx context.Context, name string, a oneExpArgs) expResult {
	trig, err := triggerExperiment(ctx, a.client, name)
	if err != nil {
		return expResult{name: name, err: err}
	}
	run := &trig.Runs[0]
	a.registerTriggered(trig)
	a.lg.printf("Triggered run %s on experiment %q (status: %s).\n", run.ID, name, run.Status)
	if len(trig.Runs) > 1 {
		a.lg.printf("  note: %s\n", multiModelNote(trig, run.ID))
	}

	final, untracked, err := a.waitForRun(pollCtx, trig)
	if err != nil {
		return expResult{name: name, run: final, err: err}
	}

	res, err := a.client.GetEvalRunResults(ctx, run.ID)
	if err != nil {
		return expResult{name: name, run: final, err: fmt.Errorf("fetch results for run %s: %w", run.ID, err)}
	}

	consistency := consistencyByEvaluator(res, a.evalNames, shapeOfRun(final))
	if a.flags.format != jsonFormat {
		writeRunSummary(a.lg, final, res, a.evalNames, consistency)
	}
	if a.flags.outputDir != "" {
		dumpRunTraces(ctx, a.client, a.lg, a.flags.outputDir, name, run.ID, res)
	}
	gate := runGate{threshold: a.flags.threshold, ceiling: a.ceiling, allowWarnings: a.flags.allowWarnings}
	cost, verdict := runVerdict(final, res, gate)
	if cost != nil && cost.unknown > 0 {
		a.lg.printf("  note: cost of %d of %d trial(s) is unknown and not counted against --max-cost; the known costs total %s.\n",
			cost.unknown, len(res), cost.known.FloatString(6))
	}
	counts := countForGate(final, res, gate.countsBroken(final))
	if counts.ungraded > 0 || counts.errored > 0 {
		a.lg.printf("  note: of %d trial(s), %d produced no score (%s) and %d errored; they count as failures, so --threshold is checked against %d/%d passed.\n",
			max(final.ProgressTotal, len(res)), counts.ungraded, counts.ungradedKinds(), counts.errored, counts.pass, counts.total)
	}
	return expResult{name: name, run: final, results: res, consistency: consistency, cost: cost, counts: counts, err: errors.Join(verdict, untracked)}
}

func multiModelNote(trig *evalTrigger, trackedRunID string) string {
	return fmt.Sprintf("experiment declares %d model runs; only %s is reported, but the command waits for all of them and fails if any fails.", len(trig.Runs), trackedRunID)
}

func (a oneExpArgs) registerTriggered(trig *evalTrigger) {
	for _, r := range trig.Runs {
		a.registerRun(r.ID)
	}
}

func (a oneExpArgs) waitForRun(ctx context.Context, trig *evalTrigger) (run *evalplatform.RunResponse, untracked, err error) {
	run, operation, err := followEvalOperation(ctx, a.lg, a.client, trig, a.flags.pollEvery, a.evalNames)
	return run, untrackedRunFailure(operation, trig.Runs[0].ID), err
}

func refuseExperimentsWithoutCost(ctx context.Context, c *reconcile.Client, names []string) error {
	for _, name := range names {
		exp, err := lookupExperimentByName(ctx, c, name)
		if err != nil {
			return err
		}
		if experimentTargetType(*exp) != "workflow" {
			return fmt.Errorf("experiment %q evaluates an agent; --max-cost applies only to workflow experiments, whose trials report a cost", name)
		}
		if len(exp.Models) > 1 {
			return fmt.Errorf("experiment %q runs %d models and --max-cost counts only the first run's cost; give it one model to gate its cost", name, len(exp.Models))
		}
	}
	return nil
}

type runRegistry struct {
	mu  sync.Mutex
	ids []string
}

func (r *runRegistry) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *runRegistry) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ids)
}

func cancelRunsOnInterrupt(notice *lineLogger, c *reconcile.Client, runs *runRegistry, cancelPoll context.CancelFunc) (stop func()) {
	done := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			notice.printf("received interrupt, canceling run(s)...\n")
			for _, id := range runs.snapshot() {
				_, _ = c.CancelEvalRun(context.Background(), id)
			}
			cancelPoll()
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

func triggerWithoutWatching(ctx context.Context, cmd *cobra.Command, c *reconcile.Client, names []string, format string) error {
	out := cmd.OutOrStdout()
	triggered := make([]map[string]any, 0, len(names))
	for _, name := range names {
		trig, err := triggerExperiment(ctx, c, name)
		if err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", name, err)
			continue
		}
		run := trig.Runs[0]
		if format == jsonFormat {
			triggered = append(triggered, map[string]any{
				"experiment": name, "runId": run.ID, "status": run.Status,
			})
			continue
		}
		_, _ = fmt.Fprintf(out, "Triggered run %s on experiment %q (status: %s). Watch disabled.\n",
			run.ID, name, run.Status)
	}
	if format == jsonFormat {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(triggered)
	}
	return nil
}

type evalTrigger struct {
	*reconcile.EvalTrigger
	experimentID string
}

func triggerExperiment(ctx context.Context, c *reconcile.Client, name string) (*evalTrigger, error) {
	exp, err := lookupExperimentByName(ctx, c, name)
	if err != nil {
		return nil, err
	}
	trig, err := c.TriggerExperimentRun(ctx, exp.ID, "")
	if err != nil {
		return nil, fmt.Errorf("trigger experiment %q: %w", name, err)
	}
	if len(trig.Runs) == 0 {
		return nil, errors.New("trigger returned no runs")
	}
	return &evalTrigger{EvalTrigger: trig, experimentID: exp.ID}, nil
}

func followEvalOperation(
	ctx context.Context,
	lg *lineLogger,
	c *reconcile.Client,
	trig *evalTrigger,
	interval time.Duration,
	evalNames map[string]string,
) (*evalplatform.RunResponse, *apiv2.Operation, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	runID := trig.Runs[0].ID
	seen := map[string]bool{}
	completed := 0
	lastStatus := ""
	lastProgress := -1
	flushed := false

	for {
		operation, err := c.GetEvalOperation(ctx, trig.experimentID, trig.Operation.ID)
		if err != nil {
			return nil, nil, runGoneOr(runID, fmt.Errorf("get operation %s: %w", trig.Operation.ID, err))
		}
		terminal := isTerminalOperation(operation.Status)
		run, err := c.GetEvalRun(ctx, runID)
		if err != nil {
			return nil, nil, runGoneOr(runID, fmt.Errorf("get run %s: %w", runID, err))
		}
		if run.Status != lastStatus {
			lg.printf("  status=%s progress=%d/%d\n", run.Status, run.ProgressCurrent, run.ProgressTotal)
			lastStatus = run.Status
		}
		runEnded := evalplatform.IsTerminalStatus(run.Status) || terminal
		if !flushed && (run.ProgressCurrent != lastProgress || runEnded) {
			lastProgress = run.ProgressCurrent
			if res, rerr := c.GetEvalRunResults(ctx, runID); rerr == nil {
				completed = streamNewResults(lg, res, seen, completed, run.ProgressTotal, evalNames, runEnded)
				flushed = runEnded
			}
		}
		if terminal {
			return run, operation, nil
		}
		select {
		case <-ctx.Done():
			return run, operation, ctx.Err()
		case <-t.C:
		}
	}
}

func untrackedRunFailure(operation *apiv2.Operation, trackedRunID string) error {
	if operation == nil || operation.Status != operationFailed {
		return nil
	}
	if operation.Error == nil {
		return fmt.Errorf("operation %s failed", operation.ID)
	}
	if operation.Error.ErrorID == trackedRunID {
		return nil
	}
	return fmt.Errorf("run %s of another model failed: %s", operation.Error.ErrorID, operation.Error.Message)
}

const (
	operationSucceeded = "succeeded"
	operationFailed    = "failed"
	operationCancelled = "cancelled"
)

func isTerminalOperation(status string) bool {
	return status == operationSucceeded || status == operationFailed || status == operationCancelled
}

func runGoneOr(runID string, err error) error {
	var httpErr *reconcile.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("run %s was deleted while waiting for it: %w", runID, err)
	}
	return err
}

func streamNewResults(
	lg *lineLogger,
	results []evalplatform.RunResultResponse,
	seen map[string]bool,
	completed, total int,
	evalNames map[string]string,
	force bool,
) int {
	ordered := append([]evalplatform.RunResultResponse(nil), results...)
	sortResultsByCompletion(ordered)
	for _, r := range ordered {
		if seen[r.ID] {
			continue
		}
		if !force && !resultScored(r) {
			continue
		}
		seen[r.ID] = true
		completed++
		lg.printf("%s\n", formatResultLine(r, completed, total, evalNames))
	}
	return completed
}

func sortResultsByCompletion(rs []evalplatform.RunResultResponse) {
	slices.SortFunc(rs, func(a, b evalplatform.RunResultResponse) int {
		return cmp.Or(cmp.Compare(a.CreatedTime, b.CreatedTime), cmp.Compare(a.ID, b.ID))
	})
}

func resultScored(r evalplatform.RunResultResponse) bool {
	if len(r.Scores) > 0 {
		return true
	}
	return r.ErrorMessage != nil && *r.ErrorMessage != ""
}

func formatResultLine(r evalplatform.RunResultResponse, seq, total int, evalNames map[string]string) string {
	glyph := resultGlyph(r)
	snippet := promptSnippet(r.InputPrompt, 56)

	var b strings.Builder
	if total > 0 {
		fmt.Fprintf(&b, "  %s ex %d/%d", glyph, seq, total)
	} else {
		fmt.Fprintf(&b, "  %s ex %d", glyph, seq)
	}
	if snippet != "" {
		fmt.Fprintf(&b, " — %q", snippet)
	}
	if r.ErrorMessage != nil && *r.ErrorMessage != "" {
		fmt.Fprintf(&b, " [error: %s]", oneLine(*r.ErrorMessage, 80))
		return b.String()
	}
	for _, s := range r.Scores {
		label := evalLabel(evalNames, s.EvaluatorID)
		verdict := ""
		if s.Choice != nil && *s.Choice != "" {
			verdict = *s.Choice + " "
		}
		fmt.Fprintf(&b, " [%s: %s%.2f]", label, verdict, s.Score)
	}
	return b.String()
}

func resultGlyph(r evalplatform.RunResultResponse) string {
	if r.ErrorMessage != nil && *r.ErrorMessage != "" {
		return "✗"
	}
	hasVerdict := false
	for _, s := range r.Scores {
		if s.Passed == nil {
			continue
		}
		hasVerdict = true
		if !*s.Passed {
			return "✗"
		}
	}
	if hasVerdict {
		return "✓"
	}
	return "•"
}

func promptSnippet(p *string, max int) string {
	if p == nil {
		return ""
	}
	return oneLine(*p, max)
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if max > 0 && len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

func checkRunOutcome(run *evalplatform.RunResponse, results []evalplatform.RunResultResponse, gate runGate) error {
	switch run.Status {
	case evalplatform.StatusCompleted:
	case evalplatform.StatusCompletedWithWarnings:
		if !gate.allowWarnings {
			return fmt.Errorf("run %s completed with warnings", run.ID)
		}
	case evalplatform.StatusFailed:
		msg := "<no error message>"
		if run.ErrorMessage != nil && *run.ErrorMessage != "" {
			msg = *run.ErrorMessage
		}
		return fmt.Errorf("run %s failed: %s", run.ID, msg)
	case evalplatform.StatusCancelled:
		return fmt.Errorf("run %s was cancelled", run.ID)
	default:
		return fmt.Errorf("run %s ended in non-terminal status %q", run.ID, run.Status)
	}

	counts := countForGate(run, results, gate.countsBroken(run))
	if counts.total == 0 {
		if counts.counted {
			return fmt.Errorf("run %s completed with warnings and has no pass/fail score to check", run.ID)
		}
		return nil
	}
	rate := float64(counts.pass) / float64(counts.total)
	if rate < gate.threshold {
		return fmt.Errorf("run %s pass rate %.2f%% below threshold %.2f%% (%d/%d passed%s)",
			run.ID, rate*100, gate.threshold*100, counts.pass, counts.total, counts.brokenNote())
	}
	return nil
}

type gateCounts struct {
	pass, total    int
	counted        bool
	ungraded       int
	ungradedByKind map[string]int
	errored        int
}

func countForGate(run *evalplatform.RunResponse, results []evalplatform.RunResultResponse, countBroken bool) gateCounts {
	if !countBroken {
		pass, total := scoreCounts(results)
		return gateCounts{pass: pass, total: total}
	}
	c := gateCounts{counted: true, ungradedByKind: map[string]int{}}
	verdictsPerTrial := 0
	for _, r := range results {
		if len(r.Scores) == 0 {
			c.ungraded++
			c.ungradedByKind[cmp.Or(derefString(r.FailureKind), "unclassified")]++
			continue
		}
		verdicts := verdictCount(r.Scores)
		verdictsPerTrial = max(verdictsPerTrial, verdicts)
		c.total += verdicts
		if derefString(r.ErrorMessage) != "" {
			c.errored++
			continue
		}
		c.pass += passCount(r.Scores)
	}
	if missing := run.ProgressTotal - len(results); missing > 0 {
		c.ungraded += missing
		c.ungradedByKind["no_result"] += missing
	}
	c.total += c.ungraded * verdictsPerTrial
	return c
}

func verdictCount(scores []evalplatform.ScoreResponse) int {
	n := 0
	for _, s := range scores {
		if s.Passed != nil {
			n++
		}
	}
	return n
}

func passCount(scores []evalplatform.ScoreResponse) int {
	n := 0
	for _, s := range scores {
		if s.Passed != nil && *s.Passed {
			n++
		}
	}
	return n
}

func (c gateCounts) ungradedKinds() string {
	kinds := slices.Sorted(maps.Keys(c.ungradedByKind))
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, c.ungradedByKind[k]))
	}
	return strings.Join(parts, ", ")
}

func (c gateCounts) brokenNote() string {
	var parts []string
	if c.ungraded > 0 {
		parts = append(parts, fmt.Sprintf("%d trial(s) with no score", c.ungraded))
	}
	if c.errored > 0 {
		parts = append(parts, fmt.Sprintf("%d errored trial(s)", c.errored))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, " and ") + " count as failures"
}

func aggregateOutcomes(results []expResult) error {
	var failed []string
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.name, r.err))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	if len(failed) == 1 {
		return errors.New(failed[0])
	}
	return fmt.Errorf("%d experiment(s) failed:\n  %s", len(failed), strings.Join(failed, "\n  "))
}

func scoreCounts(results []evalplatform.RunResultResponse) (int, int) {
	pass, total := 0, 0
	for _, r := range results {
		for _, s := range r.Scores {
			if s.Passed == nil {
				continue
			}
			total++
			if *s.Passed {
				pass++
			}
		}
	}
	return pass, total
}

func writeRunSummary(lg *lineLogger, run *evalplatform.RunResponse, results []evalplatform.RunResultResponse, evalNames map[string]string, consistency []evaluatorConsistency) {
	lg.result("\nRun %s - status: %s\n", run.ID, run.Status)
	if run.Model != "" {
		lg.result("Model: %s\n", run.Model)
	}

	type bucket struct{ pass, fail, total int }
	byEvaluator := map[string]*bucket{}
	for _, r := range results {
		for _, s := range r.Scores {
			label := evalLabel(evalNames, s.EvaluatorID)
			b := byEvaluator[label]
			if b == nil {
				b = &bucket{}
				byEvaluator[label] = b
			}
			b.total++
			if s.Passed != nil {
				if *s.Passed {
					b.pass++
				} else {
					b.fail++
				}
			}
		}
	}
	if len(byEvaluator) == 0 {
		lg.result("No scored results.\n")
		return
	}
	keys := make([]string, 0, len(byEvaluator))
	for k := range byEvaluator {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	lg.result("Per-evaluator results:\n")
	overallPass, overallTotal := 0, 0
	for _, k := range keys {
		b := byEvaluator[k]
		lg.result("  %s: %d pass / %d fail / %d total\n", k, b.pass, b.fail, b.total)
		overallPass += b.pass
		overallTotal += b.total
	}
	if overallTotal > 0 {
		lg.result("Overall pass rate: %d/%d (%.1f%%)\n", overallPass, overallTotal, float64(overallPass)/float64(overallTotal)*100)
	}
	writeConsistency(lg, consistency)
	lg.result("\nLegend: ✓ pass  ✗ fail  • score only, no pass/fail verdict (a score of 0.50+ counts as a pass)\n")
}

func writeRunResultsJSON(w io.Writer, results []expResult) error {
	objs := make([]map[string]any, 0, len(results))
	for _, r := range results {
		objs = append(objs, runSummaryObject(r))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if len(objs) == 1 {
		return enc.Encode(objs[0])
	}
	return enc.Encode(objs)
}

func runSummaryObject(r expResult) map[string]any {
	pass, total := r.counts.pass, r.counts.total
	rate := 0.0
	if total > 0 {
		rate = float64(pass) / float64(total)
	}
	obj := map[string]any{
		"experiment": r.name,
		"passed":     pass,
		"total":      total,
		"passRate":   rate,
		"results":    r.results,
	}
	if len(r.consistency) > 0 {
		obj["consistency"] = r.consistency
	}
	if r.counts.counted {
		obj["ungradedTrials"] = r.counts.ungraded
		obj["ungradedByFailureKind"] = r.counts.ungradedByKind
		obj["erroredTrials"] = r.counts.errored
	}
	if r.run != nil {
		obj["runId"] = r.run.ID
		obj["status"] = r.run.Status
		obj["model"] = r.run.Model
	}
	if r.cost != nil {
		obj["knownCost"] = r.cost.known.FloatString(10)
		obj["unknownCostTrials"] = r.cost.unknown
	}
	if r.err != nil {
		obj["error"] = r.err.Error()
	}
	return obj
}

func dumpRunTraces(ctx context.Context, c *reconcile.Client, lg *lineLogger, baseDir, expName, runID string, results []evalplatform.RunResultResponse) {
	dir := filepath.Join(baseDir, sanitizePathSegment(expName), runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		lg.printf("  trace dump: mkdir %s: %v\n", dir, err)
		return
	}
	if err := writeJSONFile(filepath.Join(dir, "results.json"), results); err != nil {
		lg.printf("  trace dump: write results.json: %v\n", err)
	}

	ordered := append([]evalplatform.RunResultResponse(nil), results...)
	sortResultsByCompletion(ordered)

	execCount, eventCount := 0, 0
	for i, r := range ordered {
		stem := fmt.Sprintf("ex%02d-%s", i+1, shortID(r.ID))
		if data, derr := c.GetEvalRunResultExecutionData(ctx, r.ID); derr == nil {
			if werr := writeJSONFile(filepath.Join(dir, stem+".execution.json"), data); werr == nil {
				execCount++
			} else {
				lg.printf("  trace dump: write %s.execution.json: %v\n", stem, werr)
			}
		}
		if evs, eerr := c.GetEvalRunResultTaskEvents(ctx, r.ID); eerr == nil {
			if werr := writeJSONFile(filepath.Join(dir, stem+".events.json"), evs); werr == nil {
				eventCount++
			}
		}
	}
	lg.printf("Wrote traces to %s (%d execution, %d events of %d results).\n", dir, execCount, eventCount, len(ordered))
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func sanitizePathSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', ' ':
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" {
		return "experiment"
	}
	return s
}

func buildEvaluatorNameMap(ctx context.Context, c *reconcile.Client) map[string]string {
	out := map[string]string{}
	evs, err := c.ListEvaluators(ctx)
	if err != nil {
		return out
	}
	for _, e := range evs {
		out[e.ID] = e.Name
	}
	return out
}

func evalLabel(m map[string]string, id string) string {
	if n, ok := m[id]; ok && n != "" {
		return n
	}
	return id
}

func lookupExperimentByName(ctx context.Context, c *reconcile.Client, name string) (*wire.ExperimentResponse, error) {
	all, err := c.ListExperiments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	var matches []wire.ExperimentResponse
	for _, e := range all {
		if e.Name == name {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("experiment %q not found on platform", name)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("experiment %q is ambiguous: %d entries match", name, len(matches))
	}
}

type costCeiling struct {
	amount *big.Rat
	text   string
}

func parseMaxCost(raw string, given, watch bool) (*costCeiling, error) {
	if raw == "" && !given {
		return nil, nil
	}
	amount, ok := new(big.Rat).SetString(raw)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("--max-cost %q is not a non-negative decimal amount", raw)
	}
	if !watch {
		return nil, errors.New("--max-cost needs --watch: a run's cost is checked when it finishes")
	}
	return &costCeiling{amount: amount, text: raw}, nil
}

type runCost struct {
	known   *big.Rat
	unknown int
}

func sumKnownCosts(results []evalplatform.RunResultResponse) runCost {
	sum := runCost{known: new(big.Rat)}
	for _, r := range results {
		cost, ok := new(big.Rat), false
		if r.Cost != nil {
			_, ok = cost.SetString(*r.Cost)
		}
		if !ok {
			sum.unknown++
			continue
		}
		sum.known.Add(sum.known, cost)
	}
	return sum
}

func runVerdict(run *evalplatform.RunResponse, results []evalplatform.RunResultResponse, gate runGate) (*runCost, error) {
	outcome := checkRunOutcome(run, results, gate)
	if gate.ceiling == nil {
		return nil, outcome
	}
	cost := sumKnownCosts(results)
	if cost.known.Cmp(gate.ceiling.amount) <= 0 {
		return &cost, outcome
	}
	return &cost, errors.Join(outcome, fmt.Errorf("run %s cost at least %s, over --max-cost %s", run.ID, cost.known.FloatString(6), gate.ceiling.text))
}

type runGate struct {
	threshold     float64
	ceiling       *costCeiling
	allowWarnings bool
}

func (g runGate) countsBroken(run *evalplatform.RunResponse) bool {
	return g.allowWarnings && run.Status == evalplatform.StatusCompletedWithWarnings
}
