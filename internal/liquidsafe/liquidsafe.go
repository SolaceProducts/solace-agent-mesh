// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"fmt"

	"github.com/osteele/liquid"
)

const IncludeDisabledMessage = "include disabled: filesystem template loading is not permitted"

type denyStore struct{}

func (denyStore) ReadTemplate(name string) ([]byte, error) {
	return nil, fmt.Errorf("%s (template=%q)", IncludeDisabledMessage, name)
}

func NewEngine() *liquid.Engine {
	e := liquid.NewEngine()
	e.RegisterTemplateStore(denyStore{})
	registerDateFilter(e)
	return e
}

func NewStrictEngine() *liquid.Engine {
	e := NewEngine()
	e.StrictVariables()
	return e
}
