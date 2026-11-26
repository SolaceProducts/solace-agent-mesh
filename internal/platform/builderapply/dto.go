// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

const (
	actionCreate    = "create"
	actionUpdate    = "update"
	actionUnchanged = "unchanged"
)

const (
	statusPlanned   = "planned"
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
)

type ApplyBuildRequest struct {
	DryRun bool `json:"dryRun,omitempty"`

	BuildName string `json:"buildName,omitempty" validate:"omitempty,max=255"`

	BuilderSessionID string `json:"builderSessionId,omitempty" validate:"omitempty,uuid"`

	Components []BuildComponentInput `json:"components" validate:"required,max=100,dive"`
}

type BuildComponentInput struct {
	Kind string `json:"kind" validate:"required,oneof=connector a2a_agent agent workflow gateway"`

	Name string `json:"name" validate:"required,min=1,max=255"`

	Content string `json:"content" validate:"required"`

	Connectors []string `json:"connectors,omitempty" validate:"omitempty,max=5,dive,max=255"`

	SourceFilename string `json:"sourceFilename,omitempty" validate:"omitempty,max=255"`

	ResourceID string `json:"resourceId,omitempty" validate:"omitempty,uuid"`
}

type ApplyBuildResponse struct {
	DryRun     bool                   `json:"dryRun"`
	Operations []BuildOperationResult `json:"operations"`
	Summary    BuildApplySummary      `json:"summary"`

	Dropped []DroppedResource `json:"dropped"`

	DroppedCheckFailed bool `json:"droppedCheckFailed,omitempty"`
}

const (
	deploymentDeployed       = "deployed"
	deploymentDeployFailed   = "deploy_failed"
	deploymentUndeployFailed = "undeploy_failed"
	deploymentNotDeployed    = "not_deployed"
)

type DroppedResource struct {
	Kind string `json:"kind"`

	ID string `json:"id"`

	Name string `json:"name"`

	CreatedBySession bool `json:"createdBySession"`

	Deployment string `json:"deployment"`
}

const (
	removalRemoved  = "removed"
	removalNotFound = "not_found"
	removalRefused  = "refused"
	removalFailed   = "failed"
)

type RemoveDroppedRequest struct {
	BuilderSessionID string `json:"builderSessionId" validate:"required,uuid"`

	Resources []RemovalTarget `json:"resources" validate:"required,min=1,max=50,dive"`
}

type RemovalTarget struct {
	Kind string `json:"kind" validate:"required,oneof=agent workflow"`

	ID string `json:"id" validate:"required,uuid"`
}

type RemoveDroppedResponse struct {
	Results []RemovalResult `json:"results"`
}

type RemovalResult struct {
	Kind string `json:"kind"`

	ID string `json:"id"`

	Name string `json:"name,omitempty"`

	Status string `json:"status"`

	Error *OperationError `json:"error,omitempty"`
}

type BuildOperationResult struct {
	Kind string `json:"kind"`

	Name string `json:"name"`

	ResolvedName string `json:"resolvedName,omitempty"`

	SourceFilename string `json:"sourceFilename,omitempty"`

	Action string `json:"action"`

	Status string `json:"status"`

	ResourceID string `json:"resourceId,omitempty"`

	ChangedFields []string `json:"changedFields,omitempty"`

	RenamedFrom string `json:"renamedFrom,omitempty"`

	Warnings []string `json:"warnings,omitempty"`

	ChangesDeclined bool `json:"changesDeclined,omitempty"`

	Error *OperationError `json:"error,omitempty"`
}

type OperationError struct {
	Message           string              `json:"message"`
	ErrorID           string              `json:"errorId"`
	HTTPStatus        int                 `json:"httpStatus"`
	ValidationDetails map[string][]string `json:"validationDetails,omitempty"`
}

type BuildApplySummary struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}
