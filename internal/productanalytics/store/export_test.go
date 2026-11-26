// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func NewTestDB(t *testing.T) *DB { return newTestDB(t) }

func IsValidInstallID(id string) bool { return isValidInstallID(id) }
