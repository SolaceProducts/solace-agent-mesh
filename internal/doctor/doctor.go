// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

var Version = "dev"

type Doctor struct {
	Checks []Check
	Logger *slog.Logger
	Out    io.Writer
}

func New(checks []Check, logger *slog.Logger) *Doctor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Doctor{
		Checks: checks,
		Logger: logger,
		Out:    os.Stdout,
	}
}

func (d *Doctor) Run(ctx context.Context, env Env, doctorCtx Context) []CheckResult {
	skipSet := parseSkipList(env.SkipChecks)
	results := make([]CheckResult, 0, len(d.Checks))
	for _, c := range d.Checks {
		if _, skip := skipSet[strings.ToLower(c.Name())]; skip {
			results = append(results, CheckResult{
				Name:   c.DisplayName(),
				Status: StatusSkip,
				Reason: "Skipped via SAM_DOCTOR_SKIP_CHECKS",
			})
			continue
		}
		results = append(results, runCheck(ctx, c, env, doctorCtx))
	}
	return results
}

func (d *Doctor) HasBlockingFailures(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

func runCheck(ctx context.Context, c Check, env Env, doctorCtx Context) (out CheckResult) {
	defer func() {
		if r := recover(); r != nil {
			out = CheckResult{
				Name:   c.DisplayName(),
				Status: StatusFail,
				Reason: fmt.Sprintf("Unexpected error during check: %v", r),
			}
		}
	}()
	return c.Run(ctx, env, doctorCtx)
}

func parseSkipList(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.Split(s, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

type CLIOptions struct {
	Verbose       bool
	NoFailOnError bool
	Out           io.Writer
	ErrOut        io.Writer
}

func RunCLI(ctx context.Context, opts CLIOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}

	level := slog.LevelInfo
	if opts.Verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(samlog.NewHandler(errOut, &slog.HandlerOptions{Level: level}, "text"))

	env := LoadEnv()
	doctorCtx, ok := DetectContext(env)
	if !ok {
		if env.DoctorContext != "" {
			return fmt.Errorf("SAM_DOCTOR_CONTEXT=%q is not recognized; valid values: local, wheel, helm", env.DoctorContext)
		}
		doctorCtx = ContextLocal
		_, _ = fmt.Fprintln(out, "SAM_DOCTOR_CONTEXT not set — defaulting to 'local'; set it to 'wheel' or 'helm' for deployment checks.")
	}

	d := New(BuildChecksForContext(doctorCtx), logger)
	d.Out = out
	results := d.Run(ctx, env, doctorCtx)
	d.PrintReport(results, doctorCtx)

	if d.HasBlockingFailures(results) {
		if opts.NoFailOnError {
			_, _ = fmt.Fprintln(out, "Failures detected but --no-fail-on-error is set — exiting 0")
			return nil
		}
		return errBlockingFailures
	}
	return nil
}

var errBlockingFailures = fmt.Errorf("doctor: blocking failures")

func IsBlockingFailureError(err error) bool {
	return err == errBlockingFailures
}
