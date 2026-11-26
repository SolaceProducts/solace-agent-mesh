// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !embedui && !desktop

package frontend

import (
	"errors"
	"io/fs"
)

func Assets() (fs.FS, error) {
	return nil, errors.New("no embedded frontend assets (build with embedui or desktop tag)")
}
