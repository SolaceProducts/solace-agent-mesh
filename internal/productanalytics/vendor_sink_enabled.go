// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package productanalytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	mixpanel "github.com/mixpanel/mixpanel-go"
)

const (
	logMsgVendorAccepted    = "product analytics vendor accepted the batch"
	logMsgVendorRejected    = "product analytics vendor rejected records in the batch"
	logMsgVendorNoInstallID = "product analytics events dropped, the install marker was unreadable"
)

const (
	vendorPropSessionID     = "sessionId"
	vendorPropPublisherID   = "publisherId"
	vendorPropSeq           = "seq"
	vendorPropReceivedAt    = "receivedAt"
	vendorPropSchemaVersion = "schemaVersion"
)

const (
	vendorPropIP    = "ip"
	vendorGeoOptOut = "0"
)

const vendorHTTPTimeout = 30 * time.Second

type vendorSink struct {
	client *mixpanel.ApiClient
	logger *slog.Logger
}

func NewVendorSink(logger *slog.Logger) (Sink, bool) {
	if mixpanelToken == "" {
		return nil, false
	}
	return newVendorSink(mixpanelToken, "", logger), true
}

func newVendorSink(token, endpoint string, logger *slog.Logger) *vendorSink {
	if logger == nil {
		logger = slog.Default()
	}
	options := []mixpanel.Options{
		mixpanel.HttpClient(&http.Client{Timeout: vendorHTTPTimeout}),
	}
	if endpoint != "" {
		options = append(options, mixpanel.ProxyApiLocation(endpoint))
	}
	return &vendorSink{
		client: mixpanel.NewApiClient(token, options...),
		logger: logger,
	}
}

func (s *vendorSink) Send(ctx context.Context, events []Event) error {
	payload := make([]*mixpanel.Event, 0, len(events))
	unmarked := 0
	for _, ev := range events {
		if ev.DistinctID == "" {
			unmarked++
			continue
		}
		payload = append(payload, s.vendorEvent(ev))
	}

	if len(payload) == 0 {
		if unmarked == 0 {
			return nil
		}
		return NonRetryable(fmt.Errorf("%d event(s) had no install marker", unmarked))
	}

	result, err := s.client.Import(ctx, payload, mixpanel.ImportOptionsRecommend)
	if err != nil {
		return s.classify(err, len(payload), unmarked)
	}
	s.logger.Debug(logMsgVendorAccepted, "count", result.NumRecordsImported)
	if unmarked > 0 {
		s.logger.Warn(logMsgVendorNoInstallID, "count", unmarked)
		return PartialDrop(fmt.Errorf("%d event(s) had no install marker", unmarked), unmarked)
	}
	return nil
}

func (s *vendorSink) vendorEvent(ev Event) *mixpanel.Event {
	props := make(map[string]any, len(ev.Properties))
	for key, value := range ev.Properties {
		props[key] = value
	}
	props[vendorPropIP] = vendorGeoOptOut
	props[vendorPropPublisherID] = ev.PublisherID
	props[vendorPropSeq] = ev.Seq
	props[vendorPropReceivedAt] = ev.ReceivedAt.UTC().Format(TimestampLayout)
	props[vendorPropSchemaVersion] = ev.SchemaVersion
	if ev.SessionID != "" {
		props[vendorPropSessionID] = ev.SessionID
	}

	out := s.client.NewEvent(ev.Name, ev.DistinctID, props)
	out.AddTime(ev.OccurredAt)
	out.AddInsertID(ev.EventID)
	return out
}

func (s *vendorSink) classify(err error, total, unmarked int) error {
	var validation mixpanel.ImportFailedValidationError
	if errors.As(err, &validation) {
		s.logValidationFailure(validation)
		wrapped := fmt.Errorf("the vendor rejected records in the batch: %w", err)
		if validation.NumRecordsImported > 0 {
			return PartialDrop(wrapped, total-validation.NumRecordsImported+unmarked)
		}
		return NonRetryable(wrapped)
	}
	var rateLimited mixpanel.ImportRateLimitError
	if errors.As(err, &rateLimited) {
		return fmt.Errorf("the vendor rate limited the batch: %w", err)
	}
	var refused mixpanel.ImportGenericError
	if errors.As(err, &refused) {
		return NonRetryable(fmt.Errorf("the vendor refused the batch: %w", err))
	}
	return fmt.Errorf("send the batch to the vendor: %w", err)
}

func (s *vendorSink) logValidationFailure(failure mixpanel.ImportFailedValidationError) {
	attrs := []any{
		"imported", failure.NumRecordsImported,
		"rejected", len(failure.FailedImportRecords),
	}
	if len(failure.FailedImportRecords) > 0 {
		first := failure.FailedImportRecords[0]
		attrs = append(attrs,
			"field", redactSecrets(first.Field),
			"vendorMessage", redactSecrets(first.Message),
		)
	}
	s.logger.Warn(logMsgVendorRejected, attrs...)
}

func (s *vendorSink) Identify(ctx context.Context, installID string, traits map[string]any) error {
	profile := mixpanel.NewPeopleProperties(installID, traits)
	if err := s.client.PeopleSet(ctx, []*mixpanel.PeopleProperties{profile}); err != nil {
		return fmt.Errorf("set the vendor profile for the install: %w", err)
	}
	return nil
}

func (s *vendorSink) MaxBatchSize() int { return mixpanel.MaxImportEvents }
