// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type Mode string

const (
	ModeImplement Mode = modeImplement
	ModePlan      Mode = modePlan
)

const (
	StopCompleted       = stopCompleted
	StopMaxSteps        = stopMaxSteps
	StopBudgetExhausted = stopBudgetExhausted
	StopContextOverflow = stopContextOverflow
	StopLLMError        = stopLLMError
	StopCancelled       = stopCancelled
	StopMaxTokens       = stopMaxTokens
)

type ChatFunc func(ctx context.Context, msgs []sdk.ChatMessage, opts sdk.ChatOptions) (*sdk.ChatResult, error)

type Options struct {
	Mode            Mode
	Dir             string
	RepoRoot        string
	Task            string
	VerifyCommand   string
	Plan            string
	Reset           bool
	MaxSteps        int
	OutputSchema    map[string]any
	ValidateResult  func(json.RawMessage) error
	ReadDirs        []string
	Env             []string
	Instructions    string
	FetchDomains    []string
	NoShell         bool
	ShellAllow      []string
	NoExec          bool
	MaxTokens       int
	History         []sdk.ChatMessage
	ReasoningEffort string
	Deadline        time.Time
	HostTools       []HostTool
	Skills          []string
	Chat            ChatFunc
	Status          func(string)
	Record          func(Event)
	NotesFile       string
}

type Outcome struct {
	Report       string
	StopReason   string
	Result       any
	Steps        int
	ToolCalls    int
	ToolsUsed    map[string]int
	Usage        sdk.ChatUsage
	ModelTime    time.Duration
	ToolTime     map[string]time.Duration
	Model        string
	Err          string
	Changes      *ChangeSet
	DiffErr      string
	Plan         string
	PlanCheck    *PlanCheck
	Verify       *ShellResult
	VerifyRounds int
	History      []sdk.ChatMessage
}

func (o *Outcome) Answered() bool {
	return o.StopReason == StopCompleted || o.StopReason == StopMaxSteps
}

const (
	verifyTimeoutSeconds = 600
	verifyRetries        = 2
	verifyToolTime       = "verify"
)

func Run(ctx context.Context, o Options) (*Outcome, error) {
	if strings.TrimSpace(o.Task) == "" {
		return nil, errors.New("task is required")
	}
	if o.Chat == nil {
		return nil, errors.New("a chat function is required")
	}
	spec, err := newResultSpec(o.OutputSchema)
	if err != nil {
		return nil, err
	}
	if spec != nil {
		spec.validate = o.ValidateResult
	}
	root, err := resolveDir(o.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving dir: %w", err)
	}
	repoRoot := root
	if o.RepoRoot != "" {
		if repoRoot, err = resolveDir(o.RepoRoot); err != nil {
			return nil, fmt.Errorf("resolving repo root: %w", err)
		}
	}
	if o.NoExec {
		o.NoShell, o.ShellAllow, o.VerifyCommand, o.FetchDomains = true, nil, "", nil
	}
	if hostErr := checkHostTools(o.HostTools); hostErr != nil {
		return nil, hostErr
	}
	skillDirs := make([]string, 0, len(o.Skills))
	for _, d := range o.Skills {
		resolved, resolveErr := resolveDir(d)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolving skill dir: %w", resolveErr)
		}
		skillDirs = append(skillDirs, resolved)
	}
	skills, err := loadSkills(skillDirs)
	if err != nil {
		return nil, err
	}
	readDirs := make([]string, 0, len(o.ReadDirs)+len(skillDirs))
	readDirs = append(readDirs, skillDirs...)
	for _, d := range o.ReadDirs {
		resolved, resolveErr := resolveReadDir(d)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolving read dir: %w", resolveErr)
		}
		readDirs = append(readDirs, resolved)
	}
	mode := o.Mode
	if mode == "" {
		mode = ModeImplement
	}
	status := o.Status
	if status == nil {
		status = func(string) {}
	}
	ws := &workspace{root: root, readOnly: mode == ModePlan, readDirs: readDirs, env: o.Env, fetchDomains: normalizeDomains(o.FetchDomains), shell: newShellPolicy(o.NoShell, o.ShellAllow), hostTools: o.HostTools, noExec: o.NoExec}
	instructions := o.Instructions
	if instructions != "" && !strings.HasSuffix(instructions, "\n") {
		instructions += "\n"
	}
	instructions += ws.shell.note() + skillsNote(skills)
	if len(ws.fetchDomains) > 0 {
		instructions += fetchNote(ws.fetchDomains)
	}
	cfg := loopConfig{
		mode:          string(mode),
		task:          o.Task,
		verifyCommand: o.VerifyCommand,
		subdir:        subdirLabel(repoRoot, root),
		instructions:  instructions + repoInstructions(repoRoot, root),
		result:        spec,
		status:        status,
		record:        o.Record,
		notes:         newNotesKeeper(root, o.NotesFile),
		history:       o.History,
		maxTokens:     o.MaxTokens,
		reasoning:     o.ReasoningEffort,
		deadline:      o.Deadline,
	}
	if mode == ModePlan {
		cfg.maxSteps = stepLimit(o.MaxSteps, defaultPlanSteps)
		return finish(runLoop(ctx, o.Chat, ws, cfg), spec), nil
	}

	if o.Reset {
		if resetErr := resetState(root); resetErr != nil {
			return nil, fmt.Errorf("could not reset the workspace state: %w", resetErr)
		}
	}
	base, err := snapshotWorkspace(ctx, root, o.NoExec)
	if err != nil {
		return nil, fmt.Errorf("could not snapshot the workspace before editing: %w", err)
	}
	if strings.TrimSpace(o.Plan) != "" {
		if err := writePlan(root, o.Plan); err != nil {
			return nil, fmt.Errorf("could not save the plan: %w", err)
		}
	}
	ws.base = base
	cfg.plan = readPlan(root)
	cfg.maxSteps = stepLimit(o.MaxSteps, defaultMaxSteps)
	out := finish(runLoop(ctx, o.Chat, ws, cfg), spec)
	out.Plan = ""
	if o.VerifyCommand != "" {
		verifyAndRetry(ctx, o, ws, cfg, spec, out)
	}

	changes, diffErr := base.changes(context.WithoutCancel(ctx))
	out.Changes = &changes
	if diffErr != nil {
		out.DiffErr = diffErr.Error()
	}
	if plan := readPlan(root); plan != "" {
		out.Plan = plan
		if len(plannedFiles(plan)) > 0 {
			pc := checkPlan(plan, changedPaths(changes.Files))
			out.PlanCheck = &pc
		}
	}
	return out, nil
}

func verifyAndRetry(ctx context.Context, o Options, ws *workspace, cfg loopConfig, spec *resultSpec, out *Outcome) {
	stepBudget := cfg.maxSteps
	for {
		if out.StopReason == StopCancelled {
			return
		}
		cfg.status("running verify command")
		started := time.Now()
		r := runShell(ctx, ws.root, o.VerifyCommand, verifyTimeoutSeconds, ws.env)
		out.ToolTime[verifyToolTime] += time.Since(started)
		r.Output = lastLines(r.Output, 80)
		out.Verify = &r
		stepsLeft := stepBudget - out.Steps
		if r.ExitCode == 0 || out.StopReason != StopCompleted || out.VerifyRounds >= verifyRetries || stepsLeft <= 0 {
			return
		}
		if o.MaxTokens > 0 {
			if cfg.maxTokens = o.MaxTokens - tokensUsed(out.Usage); cfg.maxTokens <= 0 {
				return
			}
		}
		out.VerifyRounds++
		cfg.history, cfg.maxSteps = out.History, stepsLeft
		cfg.followUp = verifyFailedPrompt(r, spec != nil)
		merge(out, finish(runLoop(ctx, o.Chat, ws, cfg), spec))
	}
}

func verifyFailedPrompt(r ShellResult, wantsResult bool) string {
	msg := fmt.Sprintf("You reported done, but the verify command fails:\n$ %s\n%s\n\nFix the code so it passes, run the command yourself, then report again.", r.Command, r)
	if wantsResult {
		msg += " Call submit_result again with your final answer."
	}
	return msg
}

func merge(out, next *Outcome) {
	out.Steps += next.Steps
	out.ToolCalls += next.ToolCalls
	for name, n := range next.ToolsUsed {
		out.ToolsUsed[name] += n
	}
	addUsage(&out.Usage, next.Usage)
	out.ModelTime += next.ModelTime
	for name, d := range next.ToolTime {
		out.ToolTime[name] += d
	}
	out.Report, out.StopReason, out.Err, out.History = next.Report, next.StopReason, next.Err, next.History
	if next.Model != "" {
		out.Model = next.Model
	}
	if next.Result != nil {
		out.Result = next.Result
	}
}

func finish(lo loopOutcome, spec *resultSpec) *Outcome {
	out := &Outcome{
		Report: lo.Report, StopReason: lo.StopReason, Result: lo.Result,
		Steps: lo.Steps, ToolCalls: lo.ToolCalls, ToolsUsed: lo.ToolsUsed,
		Usage: lo.Usage, ModelTime: lo.ModelTime, ToolTime: lo.ToolTime,
		Model: lo.Model, Err: lo.Err, History: lo.History,
	}
	if spec == nil && out.Answered() {
		out.Plan = lo.Report
	}
	return out
}

func subdirLabel(repoRoot, root string) string {
	rel, err := filepath.Rel(repoRoot, root)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func stepLimit(requested, fallback int) int {
	if requested <= 0 {
		return fallback
	}
	return min(requested, maxMaxSteps)
}

func resolveDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func resolveReadDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return evalExistingPrefix(abs)
}
