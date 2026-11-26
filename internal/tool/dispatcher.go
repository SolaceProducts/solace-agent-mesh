// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import "context"

type Dispatcher interface {
	Dispatch(ctx context.Context, call Call, toolCtx *Context) (*Result, error)

	GetTool(name string) Tool

	ListTools() []Tool

	Register(t Tool) error
}

type Call struct {
	ID string

	Name string

	Arguments string
}
