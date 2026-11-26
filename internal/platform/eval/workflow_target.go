// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type WorkflowTarget struct {
	Name         string
	InstanceName string
	InputSchema  map[string]any
}

type WorkflowTargetResolver interface {
	ResolveWorkflow(ctx context.Context, name string) (*WorkflowTarget, error)
}

type WorkflowInvokeChecker interface {
	CanInvokeWorkflow(ctx context.Context, principal, instanceName string) (bool, error)
}

type WorkflowTargetValidator struct {
	resolver WorkflowTargetResolver
	invoke   WorkflowInvokeChecker
	examples *store.EvalExampleRepo
	logger   *slog.Logger
}

func NewWorkflowTargetValidator(resolver WorkflowTargetResolver, invoke WorkflowInvokeChecker, examples *store.EvalExampleRepo, logger *slog.Logger) *WorkflowTargetValidator {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &WorkflowTargetValidator{resolver: resolver, invoke: invoke, examples: examples, logger: logger}
}

func (v *WorkflowTargetValidator) Resolve(ctx context.Context, name string, runAs *string) (*WorkflowTarget, error) {
	target, err := v.resolver.ResolveWorkflow(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow %q: %w", name, err)
	}
	if target == nil {
		return nil, &ValidationError{Field: "target_workflow", Message: fmt.Sprintf("no workflow named %q exists", name)}
	}
	if v.invoke == nil {
		return target, nil
	}
	principal := runAsPrincipal(runAs)
	allowed, err := v.invoke.CanInvokeWorkflow(ctx, principal, target.InstanceName)
	if err != nil {
		return nil, fmt.Errorf("check %s may invoke workflow %q: %w", principal, name, err)
	}
	if !allowed {
		return nil, &ValidationError{
			Field: "run_as",
			Message: fmt.Sprintf("%s cannot invoke workflow %q: it needs the %s scope", principal, name,
				rbac.WorkflowInvokeScopeForInstance(target.InstanceName)),
		}
	}
	return target, nil
}

func (v *WorkflowTargetValidator) CheckExamples(ctx context.Context, target *WorkflowTarget, datasetID string) error {
	rows, err := v.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return fmt.Errorf("list examples: %w", err)
	}
	schema := v.compileInputSchema(target)
	problems := fieldErrors{}
	misfits := 0
	for _, ex := range rows {
		exampleProblems := workflowExampleProblems(schema, ex)
		if len(exampleProblems) > 0 {
			misfits++
		}
		for _, problem := range exampleProblems {
			problems.add(fmt.Sprintf("examples[%d].%s", ex.SequenceNumber, problem.field), "%s", problem.message)
		}
	}
	if misfits == 0 {
		return nil
	}
	ve, _ := problems.err().(*ValidationError)
	ve.Message = misfitSummary(misfits)
	return ve
}

func (v *WorkflowTargetValidator) compileInputSchema(target *WorkflowTarget) *structured.Schema {
	schema, err := structured.CompileInputSchema(target.InputSchema)
	if err != nil {
		v.logger.Warn("eval: workflow input_schema does not compile; examples are not checked against it",
			"workflowInstance", target.InstanceName, "error", err)
		return nil
	}
	return schema
}

func misfitSummary(misfits int) string {
	if misfits == 1 {
		return "1 example cannot run against this workflow"
	}
	return fmt.Sprintf("%d examples cannot run against this workflow", misfits)
}

func (v *WorkflowTargetValidator) Validate(ctx context.Context, name string, runAs *string, datasetID string) (*WorkflowTarget, error) {
	target, err := v.Resolve(ctx, name, runAs)
	if err != nil {
		return nil, err
	}
	if err := v.CheckExamples(ctx, target, datasetID); err != nil {
		return nil, err
	}
	return target, nil
}

type exampleProblem struct {
	field   string
	message string
}

func workflowExampleProblems(schema *structured.Schema, ex *store.EvalExample) []exampleProblem {
	var problems []exampleProblem
	if ex.ArtifactByRole(store.EvalArtifactRoleInput) != nil {
		problems = append(problems, exampleProblem{"inputArtifact", "a workflow example cannot attach an input file; put its input in inputData"})
	}
	if schema == nil {
		return problems
	}
	for _, violation := range schema.Validate(WorkflowInput(ex, schema)) {
		problems = append(problems, exampleProblem{"inputData", violation})
	}
	return problems
}

func WorkflowInput(ex *store.EvalExample, schema *structured.Schema) map[string]any {
	if raw := inputDataJSON(ex.InputData); raw != nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var data map[string]any
		if dec.Decode(&data) == nil && data != nil {
			return data
		}
	}
	return structured.TextRunInput(strings.TrimSpace(ex.Prompt), schema)
}

func runAsPrincipal(runAs *string) string {
	if runAs != nil && *runAs != "" {
		return *runAs
	}
	return rbac.EvalSystemUserSub
}
