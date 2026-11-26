// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"fmt"
	"strings"
)

const (
	bearerSchemeName        = "bearerAuth"
	bearerSchemeDescription = "A sign-in access token from your identity provider, or an Agent Mesh API key, sent as `Authorization: Bearer <token>`."

	permissionsLabel = "Token Permissions"
	apiKeySentence   = "API keys can call this operation."
	scopeSeparator   = " **or** "
)

func accessParagraph(a Access) string {
	var permissions string
	switch {
	case a.Gate != "":
		permissions = a.Gate
	case len(a.Scopes) > 0:
		quoted := make([]string, len(a.Scopes))
		for i, scope := range a.Scopes {
			quoted[i] = "`" + scope + "`"
		}
		permissions = "[ " + strings.Join(quoted, scopeSeparator) + " ]"
	}
	paragraph := fmt.Sprintf("%s: %s", permissionsLabel, permissions)
	if a.APIKeys {
		paragraph += " " + apiKeySentence
	}
	return paragraph
}

func checkAccess(op Operation) error {
	switch {
	case op.Access.Gate == "" && len(op.Access.Scopes) == 0:
		return fmt.Errorf("%s: no Access: set its Scopes or its Gate", op.label())
	case op.Access.Gate != "" && len(op.Access.Scopes) > 0:
		return fmt.Errorf("%s: Access sets both Scopes and a Gate", op.label())
	}
	return nil
}
