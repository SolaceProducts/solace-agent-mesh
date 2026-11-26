// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

const (
	OperationPending    = "pending"
	OperationInProgress = "inProgress"
	OperationSucceeded  = "succeeded"
	OperationFailed     = "failed"
	OperationCancelled  = "cancelled"
	OperationResource   = "operation"
)

type Operation struct {
	ID            string          `json:"id" example:"0190f5f1-4a2b-7c3d-8e9f-0a1b2c3d4e5f"`
	Type          string          `json:"type" example:"operation"`
	ResourceID    *string         `json:"resourceId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	OperationType string          `json:"operationType" example:"run"`
	CreatedBy     string          `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime   Instant         `json:"createdTime"`
	CompletedTime Instant         `json:"completedTime"`
	Status        string          `json:"status" example:"inProgress"`
	Error         *OperationError `json:"error"`
}

type OperationError struct {
	Message string `json:"message" example:"A run in this operation failed. Get the run by the error's errorId to see its status."`
	ErrorID string `json:"errorId" example:"0190f5f2-6c7d-7e8f-9a0b-1c2d3e4f5a6b"`
}
