// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spotdraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
)

const Origin = "http://localhost:8800"

const (
	maxRetryAfter    = 5 * time.Second
	maxResponseBytes = 1 << 20
)

type operation struct {
	name    string
	method  string
	suffix  string
	timeout time.Duration
}

var (
	opConsentStatus = operation{name: "consent_status", method: http.MethodPost, suffix: "consent_status/", timeout: 5 * time.Second}
	opClickwrap     = operation{name: "clickwrap", method: http.MethodGet, suffix: "", timeout: 10 * time.Second}
	opExecute       = operation{name: "execute", method: http.MethodPost, suffix: "execute/", timeout: 15 * time.Second}
)

type ConsentStatus string

const (
	NotAccepted           ConsentStatus = "NOT_ACCEPTED"
	AcceptedOlderVersion  ConsentStatus = "ACCEPTED_OLDER_VERSION"
	AcceptedLatestVersion ConsentStatus = "ACCEPTED_LATEST_VERSION"
)

func (s ConsentStatus) Agreed() bool {
	return s == AcceptedOlderVersion || s == AcceptedLatestVersion
}

type Clickwrap struct {
	ID         int64       `json:"id"`
	PublicID   string      `json:"public_id"`
	Agreements []Agreement `json:"agreements"`
	Settings   Settings    `json:"settings"`
}

type Settings struct {
	Type  string         `json:"type"`
	Texts []CheckboxText `json:"clickwrap_texts_raw"`
}

type CheckboxText struct {
	Text string `json:"text"`
}

const AgreementListPlaceholder = "{#agreement_list#}"

func (c Clickwrap) CheckboxText() string {
	if len(c.Settings.Texts) == 0 {
		return ""
	}
	return c.Settings.Texts[0].Text
}

type Agreement struct {
	ID             int64   `json:"id"`
	CurrentVersion Version `json:"current_version"`
}

type Version struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	FullVersionNumber string `json:"full_version_number"`
	PublicURL         string `json:"public_url"`
}

type Acceptance struct {
	UserIdentifier string
	Email          string
	FirstName      string
	LastName       string
	Opened         map[int64]bool
}

type StatusError struct {
	Op         string
	Code       int
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("spotdraft: %s answered %d", e.Op, e.Code)
}

var (
	ErrNoAgreements    = errors.New("spotdraft: the clickwrap has no published agreement")
	ErrUnknownStatus   = errors.New("spotdraft: unknown consent status")
	errMalformedAnswer = errors.New("spotdraft: malformed answer")
)

func Retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.Code >= http.StatusInternalServerError || status.Code == http.StatusTooManyRequests
	}
	return !errors.Is(err, errMalformedAnswer) && !errors.Is(err, ErrNoAgreements) && !errors.Is(err, ErrUnknownStatus)
}

type Client struct {
	baseURL string
	http    *http.Client

	clickwrapAttempts int
	executeAttempts   int
	firstRetryDelay   time.Duration
}

type Option func(*Client)

func WithFirstRetryDelay(d time.Duration) Option {
	return func(c *Client) { c.firstRetryDelay = d }
}

func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:           strings.TrimSuffix(baseURL, "/"),
		http:              &http.Client{CheckRedirect: refuseRedirect},
		clickwrapAttempts: 3,
		executeAttempts:   3,
		firstRetryDelay:   time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (c *Client) ConsentStatus(ctx context.Context, clickwrapID, userIdentifier string) (ConsentStatus, error) {
	var answer struct {
		Status ConsentStatus `json:"status"`
	}
	body := map[string]string{"user_identifier": userIdentifier}
	if err := c.call(ctx, opConsentStatus, clickwrapID, body, &answer); err != nil {
		return "", err
	}
	switch answer.Status {
	case NotAccepted, AcceptedOlderVersion, AcceptedLatestVersion:
		return answer.Status, nil
	default:
		return "", ErrUnknownStatus
	}
}

func (c *Client) Clickwrap(ctx context.Context, clickwrapID string) (Clickwrap, error) {
	var clickwrap Clickwrap
	err := c.retry(ctx, c.clickwrapAttempts, func(attemptCtx context.Context) error {
		clickwrap = Clickwrap{}
		return c.call(attemptCtx, opClickwrap, clickwrapID, nil, &clickwrap)
	})
	if err != nil {
		return Clickwrap{}, err
	}
	if len(clickwrap.Agreements) == 0 {
		return Clickwrap{}, ErrNoAgreements
	}
	return clickwrap, nil
}

func (c *Client) Execute(ctx context.Context, clickwrap Clickwrap, acceptance Acceptance) (string, error) {
	sdkForm := executeRequest(clickwrap, acceptance)
	contractID, err := c.execute(ctx, clickwrap, acceptance, sdkForm)
	if !refusedAsInvalid(err) {
		return contractID, err
	}
	return c.execute(ctx, clickwrap, acceptance, publishedSchemaForm(sdkForm, clickwrap))
}

func publishedSchemaForm(sdkForm executeBody, clickwrap Clickwrap) executeBody {
	sdkForm.ClickwrapID = clickwrap.ID
	return sdkForm
}

func refusedAsInvalid(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Code == http.StatusBadRequest
}

func (c *Client) execute(ctx context.Context, clickwrap Clickwrap, acceptance Acceptance, body executeBody) (string, error) {
	var contractID string
	err := c.retry(ctx, c.executeAttempts, func(attemptCtx context.Context) error {
		var answer struct {
			ID int64 `json:"id"`
		}
		err := c.call(attemptCtx, opExecute, clickwrap.PublicID, body, &answer)
		if err == nil {
			contractID = strconv.FormatInt(answer.ID, 10)
		}
		return err
	}, func(checkCtx context.Context) (bool, error) {
		status, err := c.ConsentStatus(checkCtx, clickwrap.PublicID, acceptance.UserIdentifier)
		return status.Agreed(), err
	})
	return contractID, err
}

type executeAgreement struct {
	ID         int64 `json:"id"`
	VersionID  int64 `json:"version_id"`
	HasClicked bool  `json:"has_clicked"`
}

type executeBody struct {
	ClickwrapPublicID string             `json:"clickwrap_public_id"`
	ClickwrapID       int64              `json:"clickwrap_id,omitempty"`
	UserIdentifier    string             `json:"user_identifier"`
	UserEmail         string             `json:"user_email,omitempty"`
	FirstName         string             `json:"first_name,omitempty"`
	LastName          string             `json:"last_name,omitempty"`
	Agreements        []executeAgreement `json:"agreements"`
}

func executeRequest(clickwrap Clickwrap, acceptance Acceptance) executeBody {
	agreements := make([]executeAgreement, 0, len(clickwrap.Agreements))
	for _, a := range clickwrap.Agreements {
		agreements = append(agreements, executeAgreement{ID: a.ID, VersionID: a.CurrentVersion.ID, HasClicked: acceptance.Opened[a.ID]})
	}
	return executeBody{
		ClickwrapPublicID: clickwrap.PublicID,
		UserIdentifier:    acceptance.UserIdentifier,
		UserEmail:         acceptance.Email,
		FirstName:         acceptance.FirstName,
		LastName:          acceptance.LastName,
		Agreements:        agreements,
	}
}

func (c *Client) retry(ctx context.Context, attempts int, attempt func(context.Context) error, landed ...func(context.Context) (bool, error)) error {
	delay := c.firstRetryDelay
	for n := 1; ; n++ {
		err := attempt(ctx)
		if err == nil || n == attempts || !Retryable(err) {
			return err
		}
		if waitErr := backoff.Sleep(ctx, retryDelay(err, delay)); waitErr != nil {
			return err
		}
		delay *= 2
		for _, check := range landed {
			done, checkErr := check(ctx)
			if checkErr != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func retryDelay(err error, fallback time.Duration) time.Duration {
	var status *StatusError
	if errors.As(err, &status) && status.RetryAfter > 0 {
		return min(status.RetryAfter, maxRetryAfter)
	}
	return fallback
}

func (c *Client) call(ctx context.Context, op operation, clickwrapID string, body, answer any) error {
	ctx, cancel := context.WithTimeout(ctx, op.timeout)
	defer cancel()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("spotdraft: encode %s: %w", op.name, err)
		}
		payload = bytes.NewReader(encoded)
	}
	endpoint := c.baseURL + "/api/v2.1/public/clickwrap/" + url.PathEscape(clickwrapID) + "/" + op.suffix
	req, err := http.NewRequestWithContext(ctx, op.method, endpoint, payload)
	if err != nil {
		return fmt.Errorf("spotdraft: build %s: %w", op.name, err)
	}
	req.Header.Set("clickwrap-id", clickwrapID)
	req.Header.Set("Origin", Origin)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("spotdraft: %s: %w", op.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return &StatusError{Op: op.name, Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(answer); err != nil {
		return fmt.Errorf("%w: %s: %w", errMalformedAnswer, op.name, err)
	}
	return nil
}

func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0)
	}
	return 0
}
