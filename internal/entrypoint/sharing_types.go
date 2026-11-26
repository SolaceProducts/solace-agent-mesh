// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

type ShareRequest struct {
	UserEmail   string `json:"userEmail"`
	AccessLevel string `json:"accessLevel"`
}

type ShareResponse struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	UserEmail     string `json:"userEmail"`
	AccessLevel   string `json:"accessLevel"`
	SharedByEmail string `json:"sharedByEmail"`
	CreatedTime   string `json:"createdAt"`
	UpdatedTime   string `json:"updatedAt"`
}

type ProjectSharesResponse struct {
	ProjectID  string          `json:"projectId"`
	OwnerEmail string          `json:"ownerEmail"`
	Shares     []ShareResponse `json:"shares"`
}

type BatchShareRequest struct {
	Shares []ShareRequest `json:"shares"`
}

type BatchShareResponse struct {
	ProjectID      string          `json:"projectId"`
	Created        []ShareResponse `json:"created"`
	Updated        []ShareResponse `json:"updated"`
	TotalProcessed int             `json:"totalProcessed"`
}

type BatchDeleteRequest struct {
	UserEmails []string `json:"userEmails"`
}

type BatchDeleteResponse struct {
	ProjectID     string   `json:"projectId"`
	DeletedCount  int      `json:"deletedCount"`
	DeletedEmails []string `json:"deletedEmails"`
}
