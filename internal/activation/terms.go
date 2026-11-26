// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
)

var (
	ErrAgreementUnlinked    = errors.New("activation: this build has no license agreement linked")
	ErrTermsUnavailable     = errors.New("activation: the license agreement could not be loaded")
	ErrAgreementNotRecorded = errors.New("activation: the agreement could not be recorded")
)

type Terms struct {
	Agreed       bool
	CheckboxText string
	Documents    []TermsDocument
}

type TermsDocument struct {
	ID      string
	Name    string
	Version string
	URL     string
}

func (s *Service) Terms(ctx context.Context) (Terms, error) {
	s.mu.Lock()
	subject, err := s.termsSubjectLocked()
	s.mu.Unlock()
	if err != nil {
		return Terms{}, err
	}
	return s.clickwrapTerms(ctx, s.agreements.EULAClickwrapID, subject)
}

func (s *Service) termsSubjectLocked() (string, error) {
	switch {
	case s.record.Account == nil:
		return "", ErrNotSignedIn
	case s.clickwraps == nil || s.agreements.EULAClickwrapID == "":
		return "", ErrAgreementUnlinked
	}
	return s.record.Account.Subject, nil
}

func (s *Service) clickwrapTerms(ctx context.Context, clickwrapID, subject string) (Terms, error) {
	clickwrap, err := s.clickwraps.Clickwrap(ctx, clickwrapID)
	if err != nil {
		s.logger.Warn("license agreement not loaded", "clickwrapId", clickwrapID, "error", err)
		return Terms{}, causedBy(ErrTermsUnavailable, err)
	}
	s.mu.Lock()
	s.shown[clickwrapID] = clickwrap
	s.mu.Unlock()

	status, err := s.clickwraps.ConsentStatus(ctx, clickwrapID, subject)
	if err != nil {
		s.logger.Warn("license agreement check failed, showing it as not agreed", "clickwrapId", clickwrapID, "error", err)
	}
	terms := Terms{Agreed: err == nil && status.Agreed(), CheckboxText: clickwrap.CheckboxText()}
	terms.Documents = make([]TermsDocument, 0, len(clickwrap.Agreements))
	for _, a := range clickwrap.Agreements {
		terms.Documents = append(terms.Documents, TermsDocument{
			ID:      strconv.FormatInt(a.ID, 10),
			Name:    a.CurrentVersion.Name,
			Version: a.CurrentVersion.FullVersionNumber,
			URL:     a.CurrentVersion.PublicURL,
		})
	}
	return terms, nil
}

func (s *Service) sendAgreement(ctx context.Context, clickwrapID string, account Account, opened []string) (string, error) {
	status, err := s.clickwraps.ConsentStatus(ctx, clickwrapID, account.Subject)
	if err != nil {
		return "", fmt.Errorf("check consent before recording: %w", err)
	}
	if status.Agreed() {
		return "", nil
	}
	s.mu.Lock()
	clickwrap, shown := s.shown[clickwrapID]
	s.mu.Unlock()
	if !shown {
		if clickwrap, err = s.clickwraps.Clickwrap(ctx, clickwrapID); err != nil {
			return "", err
		}
	}
	firstName, lastName := splitName(account.Name)
	return s.clickwraps.Execute(ctx, clickwrap, spotdraft.Acceptance{
		UserIdentifier: account.Subject,
		Email:          account.Email,
		FirstName:      firstName,
		LastName:       lastName,
		Opened:         openedAgreements(clickwrap, opened),
	})
}

func splitName(name string) (first, last string) {
	first, last, _ = strings.Cut(strings.TrimSpace(name), " ")
	return first, strings.TrimSpace(last)
}

func openedAgreements(clickwrap spotdraft.Clickwrap, opened []string) map[int64]bool {
	agreements := map[int64]bool{}
	for _, a := range clickwrap.Agreements {
		if slices.Contains(opened, strconv.FormatInt(a.ID, 10)) {
			agreements[a.ID] = true
		}
	}
	return agreements
}
