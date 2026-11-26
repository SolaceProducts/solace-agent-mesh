// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

const evaluationsPath = "/evaluations"

type evaluationHandler struct {
	datasets    *eval.DatasetService
	examples    *eval.ExampleService
	artifacts   *eval.ExampleArtifactService
	evaluators  *eval.EvaluatorService
	experiments *eval.ExperimentService
	runs        *eval.RunService
	watchlist   *eval.WatchlistService
	toolAuth    *entplatform.EvalToolAuthService
	analytics   *platpkg.ResourceReporter
}

func evaluationRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, _ *slog.Logger) []Route {
	if deps.EvalDatasetSvc == nil || deps.EvalExampleSvc == nil || deps.EvalExampleArtifactSvc == nil ||
		deps.EvalEvaluatorSvc == nil || deps.EvalExperimentSvc == nil || deps.EvalRunSvc == nil ||
		deps.EvalWatchlistSvc == nil || deps.EvalToolAuthSvc == nil {
		return nil
	}
	h := &evaluationHandler{
		datasets:    deps.EvalDatasetSvc,
		examples:    deps.EvalExampleSvc,
		artifacts:   deps.EvalExampleArtifactSvc,
		evaluators:  deps.EvalEvaluatorSvc,
		experiments: deps.EvalExperimentSvc,
		runs:        deps.EvalRunSvc,
		watchlist:   deps.EvalWatchlistSvc,
		toolAuth:    deps.EvalToolAuthSvc,
		analytics:   core.ResourceAnalytics(),
	}
	return slices.Concat(
		datasetRoutes(h),
		generationRoutes(h),
		exampleRoutes(h),
		evaluatorRoutes(h),
		experimentRoutes(h),
		runsRoutes(h),
		watchlistRoutes(h),
		runResultRoutes(h),
		toolAuthRoutes(h),
	)
}

func evaluationError(err error) error {
	if nf, ok := errors.AsType[*apierr.NotFoundError](err); ok && nf.Resource == "EvalDataset" {
		return &apierr.ValidationError{Message: err.Error(), Details: map[string][]string{"datasetId": {"must name an existing dataset"}}}
	}
	return camelFieldError(err)
}

func camelFieldError(err error) error {
	if ve, ok := errors.AsType[*apierr.ValidationError](err); ok {
		details := map[string][]string{}
		for field, problems := range ve.Details {
			details[snakeToCamel(field)] = camelProblems(field, problems)
		}
		if len(details) == 0 && ve.Field != "" {
			details[snakeToCamel(ve.Field)] = camelProblems(ve.Field, []string{ve.Message})
		}
		return &apierr.ValidationError{Message: ve.Message, Details: details}
	}
	return err
}

func camelProblems(field string, problems []string) []string {
	camel := snakeToCamel(field)
	if camel == field {
		return problems
	}
	renamed := make([]string, len(problems))
	for i, problem := range problems {
		renamed[i] = strings.ReplaceAll(problem, field, camel)
	}
	return renamed
}

func snakeToCamel(name string) string {
	words := strings.Split(name, "_")
	for i := 1; i < len(words); i++ {
		if words[i] != "" {
			words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
		}
	}
	return strings.Join(words, "")
}
