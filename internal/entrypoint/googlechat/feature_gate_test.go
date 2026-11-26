// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestInit_RefusesWhileFeatureFlagOff(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyGoogleChatGateway), "")

	catalog := features.NewRegistry()
	if err := features.LoadFlags(catalog); err != nil {
		t.Fatal(err)
	}
	enabled := features.NewRegistry()
	enabled.Register(features.Definition{Key: features.KeyGoogleChatGateway, Default: true})

	cases := []struct {
		name         string
		opts         []runtime.Option
		wantDisabled bool
	}{
		{name: "no registry", wantDisabled: true},
		{name: "catalog default", opts: []runtime.Option{runtime.WithFeatureRegistry(catalog)}, wantDisabled: true},
		{name: "flag on", opts: []runtime.Option{runtime.WithFeatureRegistry(enabled)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brk := broker.NewDevBroker()
			if err := brk.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			core := entrypoint.NewGatewayCore(NewAdapter(nil), map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
			core.SetServices(runtime.NewRuntime(brk, nil, tc.opts...).Services())

			err := core.Init(context.Background(), nil)
			if err == nil {
				t.Fatal("Init must fail: the flag-on case has no adapter_config")
			}
			if got := errors.Is(err, errFeatureDisabled); got != tc.wantDisabled {
				t.Fatalf("errors.Is(err, errFeatureDisabled) = %v, want %v (err: %v)", got, tc.wantDisabled, err)
			}
		})
	}
}
