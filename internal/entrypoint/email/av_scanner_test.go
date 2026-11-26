// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
)

type fakeScanner struct {
	verdict ScannerVerdict
	err     error
	calls   int64
}

func (f *fakeScanner) Scan(_ context.Context, _ string, _ []byte) (ScannerVerdict, error) {
	atomic.AddInt64(&f.calls, 1)
	return f.verdict, f.err
}

func newScannerWorker(disp Dispatcher, scanner AttachmentScanner, mime *MIMEFilter) *Worker {
	return &Worker{
		Resolver:               NewEmailIdentityResolver(slog.Default(), false),
		Policy:                 &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs:     []string{"protection.outlook.com"},
		MaxMessageSizeBytes:    25 * 1024 * 1024,
		MaxAttachmentSizeBytes: 10 * 1024 * 1024,
		Dispatcher:             disp,
		Logger:                 slog.Default(),
		Scanner:                scanner,
		MIMEFilter:             mime,
	}
}

func TestWorker_ScannerCleanDispatches(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{verdict: ScanClean}
	w := newScannerWorker(disp, scanner, nil)
	raw := mustReadFixture(t, "synthetic/with_pdf_attachment.eml")
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})

	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Errorf("clean scan should dispatch; calls=%d want 1", got)
	}
	if got := atomic.LoadInt64(&scanner.calls); got != 1 {
		t.Errorf("scanner should have been invoked once; calls=%d", got)
	}
}

func TestWorker_ScannerInfectedDrops(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{verdict: ScanInfected}
	w := newScannerWorker(disp, scanner, nil)
	raw := mustReadFixture(t, "synthetic/with_pdf_attachment.eml")
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})

	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("infected scan should drop the message; calls=%d want 0", got)
	}
	if got := atomic.LoadInt64(&scanner.calls); got != 1 {
		t.Errorf("scanner should have been invoked once; calls=%d", got)
	}
}

func TestWorker_ScannerInconclusiveDropsFailClosed(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{verdict: ScanInconclusive}
	w := newScannerWorker(disp, scanner, nil)
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/with_pdf_attachment.eml")})

	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("inconclusive scan should fail-close; calls=%d want 0", got)
	}
}

func TestWorker_ScannerErrorDropsFailClosed(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{err: errors.New("engine unavailable")}
	w := newScannerWorker(disp, scanner, nil)
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/with_pdf_attachment.eml")})

	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("scanner transport error should fail-close; calls=%d want 0", got)
	}
}

func TestWorker_MIMEFilterShortCircuitsScanner(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{verdict: ScanClean}
	mime := &MIMEFilter{Deny: []string{"application/pdf"}}
	w := newScannerWorker(disp, scanner, mime)
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/with_pdf_attachment.eml")})

	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("MIME-denied attachment should drop the message; calls=%d", got)
	}
	if got := atomic.LoadInt64(&scanner.calls); got != 0 {
		t.Errorf("scanner must not run when MIME filter rejects first; calls=%d", got)
	}
}

func TestWorker_MIMEFilterAllowsThroughToScanner(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	scanner := &fakeScanner{verdict: ScanClean}
	mime := &MIMEFilter{Allow: []string{"application/pdf"}}
	w := newScannerWorker(disp, scanner, mime)
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/with_pdf_attachment.eml")})

	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Errorf("allowed + clean should dispatch; calls=%d want 1", got)
	}
	if got := atomic.LoadInt64(&scanner.calls); got != 1 {
		t.Errorf("allowed attachment should reach the scanner; calls=%d want 1", got)
	}
}
