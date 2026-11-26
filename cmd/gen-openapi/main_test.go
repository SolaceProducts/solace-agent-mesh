// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
)

func TestInfoVersionIsTheAPIVersionUnlessOverridden(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, run([]string{"-out", dir}))

	for _, file := range []string{openapigen.FullSpecFile, openapigen.PublicSpecFile} {
		doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join(dir, file))
		require.NoError(t, err, file)
		assert.Equal(t, "2.0.0", doc.Info.Version, file)
	}
}

func TestChatConfigIsInTheFullSpecOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, run([]string{"-out", dir}))

	const path = "/api/v2/chat/config"
	full, err := openapi3.NewLoader().LoadFromFile(filepath.Join(dir, openapigen.FullSpecFile))
	require.NoError(t, err)
	public, err := openapi3.NewLoader().LoadFromFile(filepath.Join(dir, openapigen.PublicSpecFile))
	require.NoError(t, err)

	assert.NotNil(t, full.Paths.Find(path), "the full spec lists the draft chat route")
	assert.Nil(t, public.Paths.Find(path), "the public spec leaves an unpublished route out")
}
