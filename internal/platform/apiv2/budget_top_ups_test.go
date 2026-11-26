// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newTopUpAPI(t *testing.T) *budgetAPI {
	t.Helper()
	api := newBudgetAPI(t)
	api.setAmount(store.SeededDefaultUserBudgetID, "250")
	api.setAmount(store.SeededMeshBudgetID, "5000")
	return api
}

func topUpPath(subjectID string) string { return "/budgetSubjects/" + subjectID + "/topUp" }

func (a *budgetAPI) setTopUp(subjectID, amount string, want int) BudgetTopUp {
	a.t.Helper()
	rec := a.call(http.MethodPut, topUpPath(subjectID), map[string]any{"amount": amount})
	expectStatus(a.t, rec, want)
	return decodeData[BudgetTopUp](a.t, rec)
}

var topUpKeys = []string{"amount", "expiresTime", "createdBy", "createdTime", "updatedBy", "updatedTime"}

func expectNoTopUp(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	expectStatus(t, rec, http.StatusOK)
	data := rawData(t, rec)
	expectKeys(t, "top-up", data, topUpKeys...)
	expectNull(t, data, topUpKeys...)
	expectAbsent(t, data, "id", "type")
}

func TestBudgetTopUps_PutCreatesThenReplaces(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	expectNoTopUp(t, api.call(http.MethodGet, topUpPath(jane), nil))

	created := api.setTopUp(jane, "50", http.StatusCreated)
	if created.Amount == nil || *created.Amount != "50.00" || created.CreatedBy == nil || created.ExpiresTime == 0 {
		t.Errorf("created top-up = %+v", created)
	}
	raw := rawData(t, api.call(http.MethodGet, topUpPath(jane), nil))
	expectISOTimes(t, raw, "expiresTime", "createdTime", "updatedTime")
	expectAbsent(t, raw, "id", "type", "subject")

	replaced := api.setTopUp(jane, "80.5", http.StatusOK)
	if *replaced.Amount != "80.50" || replaced.CreatedTime != created.CreatedTime || replaced.ExpiresTime != created.ExpiresTime ||
		replaced.UpdatedTime < created.UpdatedTime {
		t.Errorf("replaced top-up = %+v, want 80.50 created at the same time as %+v", replaced, created)
	}
	if got := decodeData[BudgetTopUp](t, api.call(http.MethodGet, topUpPath(jane), nil)); *got.Amount != "80.50" {
		t.Errorf("GET after the replace = %s, want 80.50", *got.Amount)
	}

	subject := rawData(t, api.call(http.MethodGet, "/budgetSubjects/"+jane, nil))
	topUp, ok := subject["topUp"].(map[string]any)
	if !ok || topUp["amount"] != "80.50" || subject["effectiveAmount"] != "330.50" {
		t.Errorf("jane's subject after the replace = %v", subject)
	} else {
		expectAbsent(t, topUp, "id", "type")
	}
	listed := decodeList[map[string]any](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=jane", nil)).Data
	if len(listed) != 1 {
		t.Fatalf("listed %d subjects, want jane", len(listed))
	}
	if topUp, ok := listed[0]["topUp"].(map[string]any); !ok {
		t.Errorf("listed jane has topUp %v", listed[0]["topUp"])
	} else {
		expectAbsent(t, topUp, "id", "type")
	}
}

func TestBudgetTopUps_ZeroRemoves(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	expectNoTopUp(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "0"}))

	api.setTopUp(jane, "10", http.StatusCreated)
	expectNoTopUp(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "0.00"}))
	expectNoTopUp(t, api.call(http.MethodGet, topUpPath(jane), nil))
	if subject := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/"+jane, nil)); subject.TopUp != nil {
		t.Errorf("jane's subject after the removal has top-up %+v", subject.TopUp)
	}
	api.setTopUp(jane, "20", http.StatusCreated)
}

func TestBudgetTopUps_Mesh(t *testing.T) {
	api := newTopUpAPI(t)
	created := api.setTopUp("mesh", "100", http.StatusCreated)
	if *created.Amount != "100.00" {
		t.Errorf("mesh top-up = %+v", created)
	}
	mesh := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/mesh", nil))
	if mesh.TopUp == nil || mesh.TopUp.Amount != "100.00" || mesh.EffectiveAmount == nil || *mesh.EffectiveAmount != "5100.00" {
		t.Errorf("mesh subject = %+v", mesh)
	}
	api.setTopUp("mesh", "150", http.StatusOK)
	expectNoTopUp(t, api.call(http.MethodPut, topUpPath("mesh"), map[string]any{"amount": "0"}))
}

func TestBudgetTopUps_UnknownSubjectIs404(t *testing.T) {
	api := newTopUpAPI(t)
	for _, id := range []string{missingID, "nobody"} {
		expectStatus(t, api.call(http.MethodGet, topUpPath(id), nil), http.StatusNotFound)
		expectStatus(t, api.call(http.MethodPut, topUpPath(id), map[string]any{"amount": "10"}), http.StatusNotFound)
		expectStatus(t, api.call(http.MethodPut, topUpPath(id), map[string]any{"amount": "0"}), http.StatusNotFound)
	}
}

func TestBudgetTopUps_HiddenSystemUserIsAddressable(t *testing.T) {
	hidden := rbac.UserListingHiddenIDs()
	if len(hidden) == 0 {
		t.Skip("no reserved system user is hidden from the user listings")
	}
	api := newTopUpAPI(t)
	api.setTopUp(hidden[0], "10", http.StatusCreated)
	got := decodeData[BudgetTopUp](t, api.call(http.MethodGet, topUpPath(hidden[0]), nil))
	if got.Amount == nil || *got.Amount != "10.00" {
		t.Errorf("hidden user's top-up = %+v, want the 10 just set", got)
	}
	expectStatus(t, api.call(http.MethodPut, topUpPath(hidden[0]), map[string]any{"amount": "0"}), http.StatusOK)
}

func TestBudgetTopUps_AmountValidation(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	for _, amount := range []string{"", "-1", "100000.01", "1.234", "ten"} {
		expectDetail(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": amount}), http.StatusUnprocessableEntity, "amount")
	}
	expectDetail(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{}), http.StatusUnprocessableEntity, "amount")
	expectStatus(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "1", "subjectId": jane}), http.StatusBadRequest)
	api.setTopUp(jane, "100000.00", http.StatusCreated)
}

func TestBudgetTopUps_RefusedWithoutALimit(t *testing.T) {
	api := newBudgetAPI(t)
	unlimited := api.createBudget("Zq Secret Unlimited", "user", nil)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	api.assign(jane, unlimited.ID)
	rec := api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "10"})
	expectMessage(t, rec, http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictTopUpUnlimited])
	if strings.Contains(rec.Body.String(), "Secret") {
		t.Errorf("the refusal names the budget: %s", rec.Body.String())
	}
	expectMessage(t, api.call(http.MethodPut, topUpPath("mesh"), map[string]any{"amount": "10"}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictTopUpUnlimited])

	noDefault := newTopUpAPI(t)
	bob := noDefault.seedUser("zq-bob", "Bob Zq", "")
	if _, err := noDefault.core.DB().ExecContext(t.Context(), `UPDATE budgets SET is_default = ? WHERE id = ?`, 0, store.SeededDefaultUserBudgetID); err != nil {
		t.Fatal(err)
	}
	expectMessage(t, noDefault.call(http.MethodPut, topUpPath(bob), map[string]any{"amount": "10"}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictTopUpNoBudget])
}

func TestBudgetTopUps_Gates(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	expectStatus(t, api.as(rbac.ScopeBudgetRead).call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "1"}), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeBudgetAssign).call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "1"}), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeBudgetAssign, rbac.ScopeBudgetRead).call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "1"}), http.StatusCreated)
	expectStatus(t, api.as(rbac.ScopeBudgetRead).call(http.MethodGet, topUpPath(jane), nil), http.StatusOK)
	expectStatus(t, api.as(rbac.ScopeBudgetAssign).call(http.MethodGet, topUpPath(jane), nil), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeRBACRead).call(http.MethodGet, topUpPath(jane), nil), http.StatusForbidden)
}

func TestBudgetTopUps_ExpiresAtTheEndOfThePeriod(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	before := Instant(budget.PeriodEnd(time.Now()).UnixMilli())
	created := api.setTopUp(jane, "50", http.StatusCreated)
	after := Instant(budget.PeriodEnd(time.Now()).UnixMilli())
	if created.ExpiresTime != before && created.ExpiresTime != after {
		t.Errorf("expiresTime = %v, want the start of next month, %v", created.ExpiresTime, after)
	}
}

func TestBudgetTopUps_RefusesAnExpiryFromTheClient(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	expectDetail(t, api.call(http.MethodPut, topUpPath(jane), `{"amount":"5","expiresTime":"2027-01-01T00:00:00.000Z"}`), http.StatusBadRequest, "expiresTime")
	expectNoTopUp(t, api.call(http.MethodGet, topUpPath(jane), nil))
}

func TestBudgetTopUps_MalformedSubjectIDIs400(t *testing.T) {
	api := newTopUpAPI(t)
	expectDetail(t, api.call(http.MethodGet, topUpPath("Not%20An%20Id"), nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPut, topUpPath("Not%20An%20Id"), map[string]any{"amount": "5"}), http.StatusBadRequest, "id")
}

func TestBudgetTopUps_TopUpOnABudgetMadeUnlimited(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	api.setTopUp(jane, "50", http.StatusCreated)
	expectStatus(t, api.call(http.MethodPatch, "/budgets/"+store.SeededDefaultUserBudgetID, map[string]any{"amount": nil}), http.StatusOK)

	if got := decodeData[BudgetTopUp](t, api.call(http.MethodGet, topUpPath(jane), nil)); got.Amount == nil || *got.Amount != "50.00" {
		t.Errorf("top-up on the unlimited budget = %+v, want 50.00 kept", got)
	}
	subject := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/"+jane, nil))
	if subject.TopUp == nil || subject.TopUp.Amount != "50.00" || subject.EffectiveAmount != nil {
		t.Errorf("jane's subject = %+v, want the top-up kept and no effective amount", subject)
	}
	expectMessage(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "80"}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictTopUpUnlimited])
	expectNoTopUp(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "0"}))
	expectNoTopUp(t, api.call(http.MethodGet, topUpPath(jane), nil))
}

func TestBudgetTopUps_RefusesASetOvertakenByTheNextPeriod(t *testing.T) {
	api := newTopUpAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	nextPeriodEnd := budget.PeriodEnd(budget.PeriodEnd(time.Now())).UnixMilli()
	writtenLater := time.Now().Add(time.Hour).UnixMilli()
	seventy, err := budget.ParseAmount("70")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.core.DB().ExecContext(t.Context(),
		`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, 'other', 'other', ?, ?)`,
		idgen.New(), store.BudgetSubjectUser, jane, seventy, nextPeriodEnd, writtenLater, writtenLater); err != nil {
		t.Fatal(err)
	}

	expectMessage(t, api.call(http.MethodPut, topUpPath(jane), map[string]any{"amount": "80"}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictTopUpPeriodChanged])
	kept := decodeData[BudgetTopUp](t, api.call(http.MethodGet, topUpPath(jane), nil))
	if kept.Amount == nil || *kept.Amount != "70.00" || kept.ExpiresTime != Instant(nextPeriodEnd) {
		t.Errorf("next period's top-up = %+v, want 70.00 until %v, untouched", kept, Instant(nextPeriodEnd))
	}
}

func TestBudgetTopUps_HiddenUser(t *testing.T) {
	hidden := rbac.UserListingHiddenIDs()
	if len(hidden) == 0 {
		t.Skip("no reserved system user is hidden from the user listings")
	}
	api := newTopUpAPI(t)
	api.setTopUp(hidden[0], "5", http.StatusCreated)
	if got := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/"+hidden[0], nil)); got.TopUp == nil || got.TopUp.Amount != "5.00" {
		t.Errorf("the hidden user's subject = %+v, want its 5.00 top-up", got)
	}
	listed := decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&pageSize=100", nil)).Data
	if slices.ContainsFunc(listed, func(s BudgetSubject) bool { return s.ID == hidden[0] }) {
		t.Errorf("the hidden user is listed")
	}
}
