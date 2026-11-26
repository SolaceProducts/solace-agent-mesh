// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestAttachPDFExtractorFactoryNoExecutor(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	adapter.pdfExtractorExecutor = nil

	ix := &projectIndexer{}
	adapter.attachPDFExtractorFactory(ix, core, "test-ns", auth.ProviderTypeEnterprise)

	if ix.pdfExtractorFactory != nil {
		t.Error("expected nil factory when executor is nil")
	}
}

func TestAttachPDFExtractorFactoryWithExecutor(t *testing.T) {
	adapter, core, brk := setupTestAdapter(t)
	if adapter.pdfExtractorExecutor == nil {
		t.Skip("expected pdfExtractorExecutor to be set by setupTestAdapter; skipping")
	}
	_ = brk

	ix := &projectIndexer{}
	adapter.attachPDFExtractorFactory(ix, core, "test-ns", auth.ProviderTypeEnterprise)

	if ix.pdfExtractorFactory == nil {
		t.Fatal("expected non-nil factory")
	}

	ext := ix.pdfExtractorFactory("alice", "proj-1", "Bearer-test-token")
	if ext == nil {
		t.Fatal("factory returned nil PDFExtractor")
	}
}

func TestAttachPDFExtractorFactoryNoopAuth(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	if adapter.pdfExtractorExecutor == nil {
		t.Skip("expected pdfExtractorExecutor to be set by setupTestAdapter; skipping")
	}

	ix := &projectIndexer{}
	adapter.attachPDFExtractorFactory(ix, core, "test-ns", auth.ProviderTypeNoop)

	if ix.pdfExtractorFactory != nil {
		t.Error("expected nil factory under noop auth — STR sandbox would reject unsigned tokens")
	}
}

func TestAttachPDFExtractorFactoryNilGuard(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	adapter.pdfExtractorExecutor = remote.NewExecutor(adapter.core.Services().Broker(), "ns", "gateway-indexer-x")

	ix := &projectIndexer{}
	adapter.attachPDFExtractorFactory(ix, core, "test-ns", auth.ProviderTypeEnterprise)
	if ix.pdfExtractorFactory == nil {
		t.Fatal("expected factory to be installed")
	}

	adapter.pdfExtractorExecutor = nil
	if got := ix.pdfExtractorFactory("alice", "proj-1", ""); got != nil {
		t.Error("expected nil PDFExtractor after executor is nil")
	}
}

func TestAttachPDFExtractorFactoryNeverForwardsAnAPIKey(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	if adapter.pdfExtractorExecutor == nil {
		t.Skip("expected pdfExtractorExecutor to be set by setupTestAdapter; skipping")
	}
	ix := &projectIndexer{}
	adapter.attachPDFExtractorFactory(ix, core, "test-ns", auth.ProviderTypeEnterprise)
	if ix.pdfExtractorFactory == nil {
		t.Fatal("expected non-nil factory")
	}

	if ext := ix.pdfExtractorFactory("alice", "proj-1", scheduledTaskTestKey); ext != nil {
		t.Fatal("the factory built a remote extractor carrying an API key")
	}
	if ext := ix.pdfExtractorFactory("alice", "proj-1", "an.ordinary.jwt"); ext == nil {
		t.Error("the guard also declined an ordinary token")
	}
}
