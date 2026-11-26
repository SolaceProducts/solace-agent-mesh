// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"
)

func TestDeployedEntrypointsReportsADatabaseFailure(t *testing.T) {
	db := testOpenDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DeployedEntrypoints(t.Context(), db, nil, slog.Default()); err == nil {
		t.Error("DeployedEntrypoints read a closed database without error")
	}
	if got, err := DeployedEntrypoints(t.Context(), nil, nil, slog.Default()); err != nil || got != nil {
		t.Errorf("no platform database: got %v, %v", got, err)
	}
}
