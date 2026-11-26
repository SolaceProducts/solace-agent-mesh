// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logkeycheck_test

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/lint/logkeycheck"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), logkeycheck.Analyzer, "a")
}
