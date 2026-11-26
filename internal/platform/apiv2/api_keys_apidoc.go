// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (APIKey) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `APIKey is a machine token that lets a pipeline call the management API as a
service account. Nothing here can reconstruct the key's secret.`,
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: "ID is the key's UUID."},
			"Type":         {Doc: "Type is always \"apiKey\"."},
			"UserID":       {Doc: "UserID is the UUID of the service account that owns the key. The key acts as that service account."},
			"Label":        {Doc: "Label is the key's display name, 1 to 255 characters without control characters."},
			"Prefix":       {Doc: "Prefix is the first 8 characters of the secret after its samk_ marker, kept so two keys can be told apart. The prefix is not the secret."},
			"Scopes":       {Doc: "Scopes are the scopes the key selected when it was created. What the key can do now also depends on its owner's roles, which are checked on every request."},
			"Status":       {Doc: "Status is the key's state: active until its expiry passes, then expired. An active key whose owner has since lost the access it selected is still active."},
			"ExpiresTime":  {Doc: "ExpiresTime is when the key stops working. Null for a key created with neverExpires."},
			"LastUsedTime": {Doc: "LastUsedTime is when the key last authenticated a request. Null until the key is first used."},
			"CreatedBy":    {Doc: "CreatedBy identifies the user who created the key: their email, or their user ID when no email is known."},
			"UpdatedBy":    {Doc: "UpdatedBy identifies the user who last changed the key, in the same form as createdBy."},
			"CreatedTime":  {Doc: "CreatedTime is when the key was created."},
			"UpdatedTime":  {Doc: "UpdatedTime is when the key was last changed."},
		},
	}
}

func (CreatedAPIKey) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreatedAPIKey is a new API key with its secret, which no other response
carries and which cannot be read again.`,
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: "ID is the key's UUID."},
			"Type":         {Doc: "Type is always \"apiKey\"."},
			"UserID":       {Doc: "UserID is the UUID of the service account that owns the key. The key acts as that service account."},
			"Label":        {Doc: "Label is the key's display name, 1 to 255 characters without control characters."},
			"Prefix":       {Doc: "Prefix is the first 8 characters of the secret after its samk_ marker, kept so two keys can be told apart."},
			"Secret":       {Doc: "Secret is the key itself. Store it now: no other response shows it, and it cannot be retrieved again."},
			"Scopes":       {Doc: "Scopes are the scopes the key selected, sorted and without duplicates."},
			"Status":       {Doc: "Status is the key's state: active for a new key whose expiry has not passed."},
			"ExpiresTime":  {Doc: "ExpiresTime is when the key stops working. Null for a key created with neverExpires."},
			"LastUsedTime": {Doc: "LastUsedTime is always null for a new key."},
			"CreatedBy":    {Doc: "CreatedBy identifies the user who created the key: their email, or their user ID when no email is known."},
			"UpdatedBy":    {Doc: "UpdatedBy identifies the user who last changed the key, in the same form as createdBy."},
			"CreatedTime":  {Doc: "CreatedTime is when the key was created."},
			"UpdatedTime":  {Doc: "UpdatedTime is when the key was last changed."},
		},
	}
}

func (CreateAPIKeyRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateAPIKeyRequest is the body that issues a key for a custom service account.
Send either expiresTime or neverExpires, not both.`,
		Fields: map[string]apidoc.Field{
			"UserID":       {Doc: "UserID is the UUID of the custom service account that owns the key."},
			"Label":        {Doc: "Label is the key's display name, 1 to 255 characters without control characters."},
			"Scopes":       {Doc: "Scopes are the scopes the key can use. The owner must hold every one, and the key can never be widened later."},
			"ExpiresTime":  {Doc: "ExpiresTime is when the key stops working, which must be in the future. Leave it out or send null when neverExpires is true."},
			"NeverExpires": {Doc: "NeverExpires issues a key that does not expire. Leave it out or send false when expiresTime is set."},
		},
	}
}

func (UpdateAPIKeyRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateAPIKeyRequest is the body that renames a key. The label is the only
field that can change, so a body naming any other field is rejected.`,
		Fields: map[string]apidoc.Field{
			"ID":    {Doc: "ID is the UUID of the key to rename."},
			"Label": {Doc: "Label is the key's new display name, 1 to 255 characters without control characters."},
		},
	}
}

func (listAPIKeysRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `listAPIKeysRequest selects a page of API keys. An unknown query parameter
fails with 400.`,
		Fields: map[string]apidoc.Field{
			"Page":   {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":   {Doc: "Sort orders the list by label, prefix, createdTime, updatedTime, expiresTime or lastUsedTime, as field:asc or field:desc; leave it out for newest first."},
			"UserID": {Doc: "UserID limits the list to the keys of the service account with this UUID. Leave it out to list the keys of every owner. An empty value fails with 400."},
			"Status": {Doc: "Status limits the list to active or expired keys. Leave it out to list both. An empty value fails with 400."},
		},
	}
}

func (listUserAPIKeysRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `listUserAPIKeysRequest selects a page of one service account's API keys. An
unknown query parameter, userId included, fails with 400.`,
		Fields: map[string]apidoc.Field{
			"UserID": {Doc: "UserID is the UUID of the service account whose keys to list."},
			"Page":   {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":   {Doc: "Sort orders the list by label, prefix, createdTime, updatedTime, expiresTime or lastUsedTime, as field:asc or field:desc; leave it out for newest first."},
			"Status": {Doc: "Status limits the list to active or expired keys. Leave it out to list both. An empty value fails with 400."},
		},
	}
}
