// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

type platformTargetFlags struct {
	url      string
	target   string
	manifest string
	insecure bool
}

func (f *platformTargetFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.url, "url", "",
		"Platform URL (or bare hostname). Overrides --target/--manifest.")
	cmd.Flags().StringVar(&f.target, "target", "",
		"Cached target name (sam auth login) to address.")
	cmd.Flags().StringVarP(&f.manifest, "manifest", "m", "",
		"Manifest file whose target (URL and auth) to use.")
	cmd.Flags().BoolVar(&f.insecure, "insecure", false,
		"Allow sending the bearer token over plain http://. TLS certificates are still verified.")
}

func newPlatformClient(ctx context.Context, f platformTargetFlags) (*reconcile.Client, error) {
	tg, err := resolveGatewayTarget(ctx, gatewayTargetRef{
		URL:      f.url,
		Target:   f.target,
		Manifest: f.manifest,
	}, "")
	if err != nil {
		return nil, err
	}

	cred, err := resolveGatewayCredential(ctx, tg)
	if err != nil {
		return nil, err
	}
	if safeErr := assertSafeBearerOverHTTP(tg.URL, cred.Token, f.insecure); safeErr != nil {
		return nil, safeErr
	}
	recordAddressedGateway(tg.URL, "", cred.Token, f.insecure, false)

	resolved := target.Resolved{URL: tg.URL, Token: cred.Token}
	if cred.Refresh != nil {
		if store, sErr := tokencache.NewDefault(); sErr == nil {
			rf := tokencache.NewResolver(store, oauth.NewClient())
			return reconcile.NewClientWithAutoRefresh(resolved, *cred.Refresh, rf), nil
		}
	}
	return reconcile.NewClient(resolved), nil
}
