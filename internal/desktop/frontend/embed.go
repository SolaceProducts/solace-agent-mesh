// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build embedui || desktop

package frontend

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var assets embed.FS

func Assets() (fs.FS, error) {
	return fs.Sub(assets, "dist/static")
}
