// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSpecificationBytes = 10 << 20

	specificationReadTimeout = 15 * time.Second

	specificationFormatJSON = "json"
	specificationFormatYAML = "yaml"
)

var (
	errSpecificationTooLarge     = errors.New("specification body is past the size cap")
	errSpecificationNotText      = errors.New("specification body is not UTF-8 text")
	errSpecificationNotADocument = errors.New("specification body is not an OpenAPI document")
)

type ConnectorSpecificationResponse struct {
	Content string `json:"content"`
	Format  string `json:"format"`
}

var (
	specificationBlockVersion = regexp.MustCompile(`(?m)^["']?(?:openapi["']?[ \t]*:[ \t]*["']?3\.|swagger["']?[ \t]*:[ \t]*["']?2\.0)`)
	specificationFlowVersion  = regexp.MustCompile(`^\{\s*["']?(?:openapi["']?\s*:\s*["']?3\.|swagger["']?\s*:\s*["']?2\.0)`)
)

type ConnectorSpecificationReader struct {
	client  *http.Client
	timeout time.Duration
}

func NewConnectorSpecificationReader() *ConnectorSpecificationReader {
	return &ConnectorSpecificationReader{client: httpClientWithSSRFGuard(nil), timeout: specificationReadTimeout}
}

func (r *ConnectorSpecificationReader) Read(ctx context.Context, specURL string) (*ConnectorSpecificationResponse, error) {
	if err := validateOutboundURLField(specURLSubject, specURL); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, &ValidationError{Message: "specification URL is not a valid URL"}
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, specificationFetchFailure(err, 0)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, specificationFetchFailure(nil, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecificationBytes+1))
	if err != nil {
		return nil, specificationFetchFailure(err, 0)
	}
	if len(body) > maxSpecificationBytes {
		return nil, &ExternalServiceError{
			Message: fmt.Sprintf("The OpenAPI specification at the URL is larger than the %d MB the platform reads.", maxSpecificationBytes>>20),
			Cause:   newOutboundCause(errSpecificationTooLarge),
		}
	}

	format, err := specificationFormat(body)
	if err != nil {
		return nil, &ExternalServiceError{Message: specificationRefusal(err), Cause: newOutboundCause(err)}
	}
	return &ConnectorSpecificationResponse{Content: string(body), Format: format}, nil
}

func specificationRefusal(err error) string {
	if errors.Is(err, errSpecificationNotText) {
		return "The specification URL did not return UTF-8 text. Save the specification as UTF-8."
	}
	return "The specification URL did not return an OpenAPI document (JSON or YAML with a top-level openapi or swagger version)."
}

func specificationFormat(body []byte) (string, error) {
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return "", errSpecificationNotText
	}
	trimmed := bytes.TrimLeft(body, " \t\r\n\ufeff")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var object map[string]json.RawMessage
		if json.Unmarshal(trimmed, &object) == nil {
			return specificationFormatJSON, requireSpecificationVersion(isSpecificationVersion(object))
		}
		return specificationFormatYAML, requireSpecificationVersion(specificationFlowVersion.Match(trimmed))
	}
	return specificationFormatYAML, requireSpecificationVersion(specificationBlockVersion.Match(trimmed))
}

func requireSpecificationVersion(found bool) error {
	if found {
		return nil
	}
	return errSpecificationNotADocument
}

func isSpecificationVersion(object map[string]json.RawMessage) bool {
	openapi := strings.Trim(string(object["openapi"]), `"`)
	swagger := strings.Trim(string(object["swagger"]), `"`)
	return strings.HasPrefix(openapi, "3.") || swagger == "2.0"
}

func specificationFetchFailure(err error, status int) error {
	if failure, ok := guardedFetchFailure(err, specURLSubject, specURLSubject); ok {
		return failure
	}
	reason := statusSuffix(status)
	if err != nil {
		reason = ": " + describeTransportFailure(err)
	}
	return &ExternalServiceError{
		Message: "Could not fetch the OpenAPI specification from the URL" + reason + ". Verify the URL and that it is reachable from the platform.",
		Cause:   newOutboundCause(fmt.Errorf("read specification: %w", causeOrStatus(err, status))),
	}
}

type specificationStatusError int

func (e specificationStatusError) Error() string {
	return fmt.Sprintf("upstream returned status %d", int(e))
}

func causeOrStatus(err error, status int) error {
	if err != nil {
		return err
	}
	return specificationStatusError(status)
}
