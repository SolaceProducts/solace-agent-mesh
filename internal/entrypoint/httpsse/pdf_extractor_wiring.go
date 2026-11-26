// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/pdfremote"
)

func (a *HTTPSSEAdapter) attachPDFExtractorFactory(ix *projectIndexer, core *entrypoint.GatewayCore, artifactNamespace, authProviderType string) {
	if authProviderType == auth.ProviderTypeNoop {
		a.logger.Info("PDF extractor: in-process fallback (auth disabled — STR sandbox would reject unsigned tokens)", "tool", pdfremote.ToolName)
		return
	}
	if a.pdfExtractorExecutor == nil {
		return
	}
	ns := core.Namespace()
	ix.pdfExtractorFactory = func(userID, projectID, authToken string) convert.PDFExtractor {
		exec := a.pdfExtractorExecutor
		if exec == nil {
			return nil
		}
		strCannotVerifyToken := apikey.HasMarker(authToken)
		if strCannotVerifyToken {
			return nil
		}
		return pdfremote.New(pdfremote.Config{
			Executor:       exec,
			AppName:        artifactNamespace,
			UserID:         userID,
			SessionID:      projectSessionPrefix + projectID,
			AuthToken:      authToken,
			TimeoutSeconds: 120,
			CorrIDPrefix:   "pdf-extract-" + ns,
			Logger:         a.logger,
		})
	}
	a.logger.Info("PDF extractor wired to STR sandbox", "tool", pdfremote.ToolName)
}
