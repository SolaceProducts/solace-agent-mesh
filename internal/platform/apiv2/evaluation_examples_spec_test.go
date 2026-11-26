// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpec_ExampleUpdateClearsThePathAndFactsWithNull(t *testing.T) {
	doc := buildSpecs(t).Full
	body := bodySchema(t, doc, operationAt(t, doc, http.MethodPatch, examplePath))

	assert.True(t, body.Properties["expectedPath"].Value.Nullable, "null clears expectedPath")
	assert.True(t, body.Properties["expectedFacts"].Value.Nullable, "null clears expectedFacts")
}
