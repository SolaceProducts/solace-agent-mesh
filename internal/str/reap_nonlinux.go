// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !linux

package str

import (
	"context"
	"log/slog"
)

func StartGlobalReaper(_ context.Context, _ *slog.Logger) {
}

func beginChildStart() {
}

func abortChildStart() {
}

func trackChild(int) {
}

func untrackChild(int) {
}
