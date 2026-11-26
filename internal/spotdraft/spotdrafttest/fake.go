// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spotdrafttest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
)

const (
	OpClickwrap     = "clickwrap"
	OpConsentStatus = "consent_status"
	OpExecute       = "execute"
)

type Execution struct {
	ClickwrapPublicID string            `json:"clickwrap_public_id"`
	UserIdentifier    string            `json:"user_identifier"`
	UserEmail         string            `json:"user_email"`
	FirstName         string            `json:"first_name"`
	LastName          string            `json:"last_name"`
	Agreements        []ExecutedVersion `json:"agreements"`
}

type ExecutedVersion struct {
	ID         int64 `json:"id"`
	VersionID  int64 `json:"version_id"`
	HasClicked bool  `json:"has_clicked"`
}

type failure struct {
	status     int
	retryAfter string
	afterWork  bool
}

type Fake struct {
	mu         sync.Mutex
	clickwraps map[string]spotdraft.Clickwrap
	revisions  map[string]int
	agreed     map[string]map[string]int
	executions []Execution
	failures   map[string][]failure
	calls      map[string]int
	down       bool
	nextID     int64

	enforcePublishedExecuteSchema bool
}

func New() *Fake {
	return &Fake{
		clickwraps: map[string]spotdraft.Clickwrap{},
		revisions:  map[string]int{},
		agreed:     map[string]map[string]int{},
		failures:   map[string][]failure{},
		calls:      map[string]int{},
		nextID:     100,
	}
}

const CheckboxText = "I agree to " + spotdraft.AgreementListPlaceholder + "."

func (f *Fake) AddClickwrap(publicID string, names ...string) spotdraft.Clickwrap {
	f.mu.Lock()
	defer f.mu.Unlock()
	clickwrap := spotdraft.Clickwrap{
		ID:       f.id(),
		PublicID: publicID,
		Settings: spotdraft.Settings{Type: "SINGLE_CHECKBOX", Texts: []spotdraft.CheckboxText{{Text: CheckboxText}}},
	}
	for _, name := range names {
		agreementID := f.id()
		clickwrap.Agreements = append(clickwrap.Agreements, spotdraft.Agreement{
			ID: agreementID,
			CurrentVersion: spotdraft.Version{
				ID:                f.id(),
				Name:              name,
				FullVersionNumber: "1.0",
				PublicURL:         "https://legal.example.test/" + url.PathEscape(strings.ToLower(name)),
			},
		})
	}
	f.clickwraps[publicID] = clickwrap
	f.revisions[publicID] = 1
	return clickwrap
}

func (f *Fake) PublishVersion(publicID, fullVersionNumber string) spotdraft.Clickwrap {
	f.mu.Lock()
	defer f.mu.Unlock()
	clickwrap := f.clickwraps[publicID]
	agreements := make([]spotdraft.Agreement, 0, len(clickwrap.Agreements))
	for _, a := range clickwrap.Agreements {
		a.CurrentVersion.ID = f.id()
		a.CurrentVersion.FullVersionNumber = fullVersionNumber
		agreements = append(agreements, a)
	}
	clickwrap.Agreements = agreements
	f.clickwraps[publicID] = clickwrap
	f.revisions[publicID]++
	return clickwrap
}

func (f *Fake) id() int64 {
	f.nextID++
	return f.nextID
}

func (f *Fake) MarkAgreed(publicID, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markAgreedLocked(publicID, user)
}

func (f *Fake) markAgreedLocked(publicID, user string) {
	if f.agreed[publicID] == nil {
		f.agreed[publicID] = map[string]int{}
	}
	f.agreed[publicID][user] = f.revisions[publicID]
}

func (f *Fake) Agreed(publicID, user string) bool {
	return f.consentStatus(publicID, user).Agreed()
}

func (f *Fake) consentStatus(publicID, user string) spotdraft.ConsentStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch agreed := f.agreed[publicID][user]; {
	case agreed == 0:
		return spotdraft.NotAccepted
	case agreed < f.revisions[publicID]:
		return spotdraft.AcceptedOlderVersion
	default:
		return spotdraft.AcceptedLatestVersion
	}
}

func (f *Fake) FailNext(op string, status int, retryAfter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[op] = append(f.failures[op], failure{status: status, retryAfter: retryAfter})
}

func (f *Fake) FailNextExecuteAfterRecording(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[OpExecute] = append(f.failures[OpExecute], failure{status: status, afterWork: true})
}

func (f *Fake) EnforcePublishedExecuteSchema() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enforcePublishedExecuteSchema = true
}

func (f *Fake) SetDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *Fake) Calls(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *Fake) Executions() []Execution {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Execution(nil), f.executions...)
}

func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agreed = map[string]map[string]int{}
	f.executions = nil
	f.failures = map[string][]failure{}
	f.calls = map[string]int{}
	f.down = false
	f.enforcePublishedExecuteSchema = false
}

func (f *Fake) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2.1/public/clickwrap/{id}/", f.handleClickwrap)
	mux.HandleFunc("POST /api/v2.1/public/clickwrap/{id}/consent_status/", f.handleConsentStatus)
	mux.HandleFunc("POST /api/v2.1/public/clickwrap/{id}/execute/", f.handleExecute)
}

func (f *Fake) Handler() http.Handler {
	mux := http.NewServeMux()
	f.Register(mux)
	return mux
}

func (f *Fake) handleClickwrap(w http.ResponseWriter, r *http.Request) {
	clickwrap, ok := f.begin(w, r, OpClickwrap)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, clickwrap)
}

func (f *Fake) handleConsentStatus(w http.ResponseWriter, r *http.Request) {
	clickwrap, ok := f.begin(w, r, OpConsentStatus)
	if !ok {
		return
	}
	var req struct {
		UserIdentifier string `json:"user_identifier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserIdentifier == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "user_identifier is required"})
		return
	}
	status := f.consentStatus(clickwrap.PublicID, req.UserIdentifier)
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "trigger_acceptance": status != spotdraft.AcceptedLatestVersion})
}

func (f *Fake) handleExecute(w http.ResponseWriter, r *http.Request) {
	clickwrap, ok := f.begin(w, r, OpExecute)
	if !ok {
		return
	}
	var body struct {
		Execution
		ClickwrapID json.RawMessage `json:"clickwrap_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !validExecution(body.Execution, clickwrap) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid clickwrap contract request"})
		return
	}
	f.mu.Lock()
	enforcePublishedSchema := f.enforcePublishedExecuteSchema
	f.mu.Unlock()
	switch {
	case enforcePublishedSchema && string(body.ClickwrapID) != strconv.FormatInt(clickwrap.ID, 10):
		writeJSON(w, http.StatusBadRequest, map[string][]string{"clickwrap_id": {"This field is required."}})
		return
	case !enforcePublishedSchema && body.ClickwrapID != nil:
		crashLikeLiveSpotDraftOnClickwrapID(w)
		return
	}
	req := body.Execution
	f.mu.Lock()
	f.markAgreedLocked(clickwrap.PublicID, req.UserIdentifier)
	f.executions = append(f.executions, req)
	contractID := f.id()
	lost, status := f.takeFailureLocked(OpExecute, true)
	f.mu.Unlock()
	if lost {
		writeJSON(w, status.status, map[string]string{"detail": "unavailable"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": contractID, "public_id": "contract-" + clickwrap.PublicID, "contract_kind": "CLICKWRAP"})
}

func validExecution(req Execution, clickwrap spotdraft.Clickwrap) bool {
	if req.ClickwrapPublicID != clickwrap.PublicID || req.UserIdentifier == "" || len(req.Agreements) != len(clickwrap.Agreements) {
		return false
	}
	for i, a := range clickwrap.Agreements {
		if req.Agreements[i].ID != a.ID || req.Agreements[i].VersionID != a.CurrentVersion.ID {
			return false
		}
	}
	return true
}

func (f *Fake) begin(w http.ResponseWriter, r *http.Request, op string) (spotdraft.Clickwrap, bool) {
	publicID := r.PathValue("id")
	if r.Header.Get("clickwrap-id") != publicID || !allowedOrigin(r.Header.Get("Origin")) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "SD_CT_001", "detail": "INVALID_DOMAIN"})
		return spotdraft.Clickwrap{}, false
	}
	f.mu.Lock()
	f.calls[op]++
	clickwrap, known := f.clickwraps[publicID]
	down := f.down
	failed, injected := f.takeFailureLocked(op, false)
	f.mu.Unlock()
	switch {
	case down:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "unavailable"})
	case failed:
		if injected.retryAfter != "" {
			w.Header().Set("Retry-After", injected.retryAfter)
		}
		writeJSON(w, injected.status, map[string]string{"detail": "failed"})
	case !known:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found."})
	default:
		return clickwrap, true
	}
	return spotdraft.Clickwrap{}, false
}

func (f *Fake) takeFailureLocked(op string, afterWork bool) (bool, failure) {
	queue := f.failures[op]
	if len(queue) == 0 || queue[0].afterWork != afterWork {
		return false, failure{}
	}
	f.failures[op] = queue[1:]
	return true, queue[0]
}

func allowedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func crashLikeLiveSpotDraftOnClickwrapID(w http.ResponseWriter) {
	http.Error(w, "Server Error (500)", http.StatusInternalServerError)
}
