// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !embeddocs

package docsembed

import "io/fs"

func Available() bool { return false }

func FS() fs.FS { return nil }
