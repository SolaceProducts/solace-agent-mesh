// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type contextView struct {
	userID    string
	sessionID string
	appName   string
}

func newContextView(tc *sdk.ToolContext) *contextView {
	if tc == nil {
		return &contextView{}
	}
	return &contextView{
		userID:    tc.UserID,
		sessionID: tc.SessionID,
		appName:   tc.AppName,
	}
}

func (c *contextView) Lookup(expr string) (any, bool) {
	if c == nil {
		return nil, false
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	expr = strings.TrimPrefix(expr, "dummy:")
	switch expr {
	case "user_id",
		"a2a_user_config.user_profile.id",
		"a2a_user_config.user_profile.user_id":
		if c.userID == "" {
			return nil, false
		}
		return c.userID, true
	case "session_id", "a2a_context.session_id":
		if c.sessionID == "" {
			return nil, false
		}
		return c.sessionID, true
	case "app_name", "a2a_context.app_name":
		if c.appName == "" {
			return nil, false
		}
		return c.appName, true
	}
	return nil, false
}
