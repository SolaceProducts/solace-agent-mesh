// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type CreateWorkflowRequest struct {
	Name        string         `json:"name" validate:"required,min=3,max=255"`
	Description string         `json:"description" validate:"required,min=10,max=2000"`
	AppConfig   map[string]any `json:"appConfig" validate:"required"`
}

type UpdateWorkflowRequest struct {
	Name        string         `json:"name" validate:"required,min=3,max=255"`
	Description string         `json:"description" validate:"required,min=10,max=2000"`
	AppConfig   map[string]any `json:"appConfig" validate:"required"`
}

type WorkflowResponse struct {
	ID               string         `json:"id" cfgapply:"read-only"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	AppConfig        map[string]any `json:"appConfig"`
	DeploymentStatus string         `json:"deploymentStatus" cfgapply:"read-only"`
	RuntimeStatus    RuntimeStatus  `json:"runtimeStatus" cfgapply:"read-only"`
	CreatedBy        string         `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy        string         `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime      Timestamp      `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime      Timestamp      `json:"updatedAt" cfgapply:"read-only"`
}

type WorkflowConfigurationResponse struct {
	ConfigurationFile string `json:"configurationFile"`
}

type CreateWorkflowDeploymentRequest struct {
	WorkflowID string `json:"workflowId" validate:"required"`
	Action     string `json:"action" validate:"required,oneof=deploy update undeploy"`
	TestMode   bool   `json:"testMode"`
}

type WorkflowDeploymentResponse struct {
	ID            string    `json:"id"`
	WorkflowID    string    `json:"workflowId"`
	Status        string    `json:"status"`
	Action        string    `json:"action"`
	ErrorMessage  string    `json:"errorMessage,omitempty"`
	CreatedBy     string    `json:"createdBy"`
	UpdatedBy     string    `json:"updatedBy"`
	CreatedTime   Timestamp `json:"createdAt"`
	UpdatedTime   Timestamp `json:"updatedAt"`
	FailureReason string    `json:"-"`
	ErrorID       string    `json:"-"`
}
