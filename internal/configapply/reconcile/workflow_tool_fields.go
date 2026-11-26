// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var featureRegistry = sync.OnceValue(func() *features.Registry {
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		reconcileLogger.Warn("failed to load feature catalog; workflow tool validation stays strict", "error", err)
	}
	return reg
})

func permissiveToolValidation() bool {
	return featureRegistry().IsEnabled(features.KeyWorkflowToolValidationPermissive)
}

const (
	workflowProblemFormat  = "workflow %q (%s): %w"
	workflowAdvisoryFormat = "workflow %q (%s): %v%s"
)

func parseFailureReport(r resolve.ResolvedResource, parseErr error, unchanged bool) (advisories []string, fatal error) {
	switch {
	case !errors.Is(parseErr, workflow.ErrEscalationChoiceRemoved):
		return []string{fmt.Sprintf("workflow %q (%s): tool-node checks skipped, the body did not parse (%v)", r.Name, r.SourceFile, parseErr)}, nil
	case unchanged:
		return []string{fmt.Sprintf("workflow %q (%s): %v; the deployed workflow will not load until the key is removed (reported, not fatal: this plan is not changing it)", r.Name, r.SourceFile, parseErr)}, nil
	default:
		return nil, fmt.Errorf(workflowProblemFormat, r.Name, r.SourceFile, parseErr)
	}
}

func validateWorkflowToolFields(r resolve.ResolvedResource, appConfig map[string]any, unchanged bool) (advisories []string, err error) {
	unchangedSuffix := ""
	if unchanged {
		unchangedSuffix = " (reported, not fatal: this plan is not changing this workflow)"
	}

	var sourceFatal error
	if sourceErr := schema.WorkflowToolSourceError(appConfig); sourceErr != nil {
		if unchanged {
			advisories = append(advisories, fmt.Sprintf(workflowAdvisoryFormat, r.Name, r.SourceFile, sourceErr, unchangedSuffix))
		} else {
			sourceFatal = fmt.Errorf(workflowProblemFormat, r.Name, r.SourceFile, sourceErr)
		}
	}
	if entryErr := schema.WorkflowPlatformConnectorEntryError(appConfig); entryErr != nil {
		if unchanged {
			advisories = append(advisories, fmt.Sprintf(workflowAdvisoryFormat, r.Name, r.SourceFile, entryErr, unchangedSuffix))
		} else {
			sourceFatal = errors.Join(sourceFatal, fmt.Errorf(workflowProblemFormat, r.Name, r.SourceFile, entryErr))
		}
	}

	problems, parseErr := workflow.ToolNodeArgumentProblems(r.Name, appConfig, workflow.StaticCheckOptions{
		Permissive: unchanged || permissiveToolValidation(),
	})
	if parseErr != nil {
		reconcileLogger.Debug("workflow tool-field check skipped; body did not parse",
			"workflow", r.Name, "error", parseErr)
		parseAdvisories, parseFatal := parseFailureReport(r, parseErr, unchanged)
		return append(advisories, parseAdvisories...), errors.Join(sourceFatal, parseFatal)
	}
	unknownToolAdvisories, unknownToolFatal := unknownToolNodeReport(r, appConfig, unchanged, unchangedSuffix)
	advisories = append(advisories, unknownToolAdvisories...)
	sourceFatal = errors.Join(sourceFatal, unknownToolFatal)
	var fatal []string
	for _, p := range problems {
		if p.Fatal {
			fatal = append(fatal, p.Message)
			continue
		}
		advisories = append(advisories, fmt.Sprintf("workflow %q (%s): %s%s", r.Name, r.SourceFile, p.Message, unchangedSuffix))
	}
	if len(fatal) > 0 {
		argumentFatal := fmt.Errorf(
			"workflow %q (%s): %s (if the argument is one the platform accepts, your sam CLI may be "+
				"older than the platform — %s; otherwise set "+
				"SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE=true in this environment to "+
				"downgrade this to a warning)",
			r.Name, r.SourceFile, strings.Join(fatal, "; "), rebuildHint)
		if sourceFatal == nil {
			return advisories, argumentFatal
		}
		return advisories, errors.Join(sourceFatal, argumentFatal)
	}
	return advisories, sourceFatal
}

func unknownToolNodeReport(r resolve.ResolvedResource, appConfig map[string]any, unchanged bool, unchangedSuffix string) (advisories []string, fatal error) {
	for _, p := range workflow.UnknownToolNodeProblems(appConfig) {
		line := fmt.Sprintf("workflow %q (%s): %s", r.Name, r.SourceFile, p.Message)
		switch {
		case !p.Fatal:
			advisories = append(advisories, line)
		case unchanged:
			advisories = append(advisories, line+unchangedSuffix)
		default:
			fatal = errors.Join(fatal, fmt.Errorf(workflowProblemFormat, r.Name, r.SourceFile, errors.New(p.Message)))
		}
	}
	return advisories, fatal
}
