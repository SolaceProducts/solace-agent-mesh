// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "errors"

var ErrShareNotFound = errors.New("share not found")

type PromptShareResponse struct {
	ID            string `json:"id"`
	UserEmail     string `json:"userEmail"`
	AccessLevel   string `json:"accessLevel"`
	SharedByEmail string `json:"sharedByEmail"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type BatchPromptShareResponse struct {
	Created        []PromptShareResponse `json:"created"`
	Updated        []PromptShareResponse `json:"updated"`
	TotalProcessed int                   `json:"totalProcessed"`
}

type UpdatePromptShareRequest struct {
	AccessLevel string `json:"accessLevel"`
}

type PromptShareReplaceResult struct {
	Created       []PromptShareResponse
	Updated       []PromptShareResponse
	RevokedEmails []string
}
