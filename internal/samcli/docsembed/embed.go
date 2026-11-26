// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build embeddocs

package docsembed

import (
	"embed"
	"io/fs"

	"github.com/SolaceProducts/solace-agent-mesh/internal/publisheddocs"
)

//go:embed all:payload
var assets embed.FS

var payload, _ = fs.Sub(assets, "payload")

func Available() bool { return true }

func FS() fs.FS { return publisheddocs.FS(payload) }
