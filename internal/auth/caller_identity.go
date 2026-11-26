// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

type CallerIdentity struct {
	ID string

	Sub    string
	Issuer string

	Email string
	Phone string

	Name string

	Scopes []string

	CredentialID string

	Claims map[string]any
}

func (c CallerIdentity) Resolved() bool { return c.ID != "" }

func CallerFromIdentity(id *Identity) CallerIdentity {
	if id == nil {
		return CallerIdentity{}
	}
	c := CallerIdentity{
		Sub:          id.UserID,
		Scopes:       id.Scopes,
		CredentialID: id.CredentialID,
		Claims:       id.Claims,
	}
	if s, ok := id.Claims["email"].(string); ok {
		c.Email = s
	}
	if s, ok := id.Claims["iss"].(string); ok {
		c.Issuer = s
	}
	if s, ok := id.Claims["name"].(string); ok {
		c.Name = s
	}
	return c
}
