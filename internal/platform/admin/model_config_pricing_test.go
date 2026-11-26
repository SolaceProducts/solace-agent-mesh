// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func readerCtx() context.Context {
	return auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "user1",
		Scopes: []string{rbac.ScopeModelConfigRead},
	}))
}

func setupPricingSvc(t *testing.T) (*ModelConfigService, *store.ModelConfigRepo) {
	t.Helper()
	return setupPricingSvcOn(t, testOpenDB(t), slog.Default())
}

func setupPricingSvcOn(t *testing.T, db *store.DB, logger *slog.Logger) (*ModelConfigService, *store.ModelConfigRepo) {
	t.Helper()
	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, logger)
	return svc, repo
}

func strp(s string) *string { return &s }

func createModel(t *testing.T, svc *ModelConfigService, alias string, price *platpkg.ModelPricing) *ModelConfigResponse {
	t.Helper()
	resp, err := svc.Create(readerCtx(), &CreateModelConfigRequest{
		Alias:      alias,
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "apiKey": "sk-test"},
		Pricing:    price,
	}, "user1")
	if err != nil {
		t.Fatalf("create %s: %v", alias, err)
	}
	return resp
}

func TestPricingRoundTripsThroughTheStore(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	resp := createModel(t, svc, "priced", &platpkg.ModelPricing{
		Input: strp("3.00"), Output: strp("15"),
		CacheRead: strp("0.3"), CacheWrite: strp("3.75"),
	})

	if resp.Pricing == nil {
		t.Fatal("create returned no pricing")
	}
	for field, got := range map[string]*string{
		"input": resp.Pricing.Input, "output": resp.Pricing.Output,
		"cacheRead": resp.Pricing.CacheRead, "cacheWrite": resp.Pricing.CacheWrite,
	} {
		if got == nil {
			t.Fatalf("%s came back null", field)
		}
	}
	want := map[string]string{"input": "3", "output": "15", "cacheRead": "0.3", "cacheWrite": "3.75"}
	got := map[string]string{
		"input": *resp.Pricing.Input, "output": *resp.Pricing.Output,
		"cacheRead": *resp.Pricing.CacheRead, "cacheWrite": *resp.Pricing.CacheWrite,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	fetched, err := svc.GetByID(readerCtx(), resp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Pricing == nil || *fetched.Pricing.Input != "3" {
		t.Fatalf("stored pricing came back as %+v", fetched.Pricing)
	}
}

func TestCacheRatesLeftOutAreStoredAsAbsent(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	resp := createModel(t, svc, "no-cache", &platpkg.ModelPricing{
		Input: strp("3"), Output: strp("15"),
	})

	if resp.Pricing == nil {
		t.Fatal("create returned no pricing")
	}
	if resp.Pricing.CacheRead != nil || resp.Pricing.CacheWrite != nil {
		t.Errorf("cache rates came back as %v / %v, want null — they bill at the input rate",
			resp.Pricing.CacheRead, resp.Pricing.CacheWrite)
	}
}

func TestAZeroRateIsAPrice(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	resp := createModel(t, svc, "free", &platpkg.ModelPricing{Input: strp("0"), Output: strp("0")})

	if resp.Pricing == nil {
		t.Fatal("a model priced at zero came back unpriced")
	}
	if *resp.Pricing.Input != "0" || *resp.Pricing.Output != "0" {
		t.Errorf("rates are %q / %q, want 0 / 0", *resp.Pricing.Input, *resp.Pricing.Output)
	}
}

func TestAModelWithNoPricingIsUnpriced(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	resp := createModel(t, svc, "unpriced", nil)

	if resp.Pricing != nil {
		t.Errorf("an unpriced model came back with %+v", resp.Pricing)
	}
}

func TestHalfAPriceIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]*platpkg.ModelPricing{
		"output without input": {Output: strp("15")},
		"input without output": {Input: strp("3")},
		"cache read alone":     {Input: strp("3"), Output: strp("15"), CacheRead: strp("0.3")},
		"cache write alone":    {Input: strp("3"), Output: strp("15"), CacheWrite: strp("3.75")},
		"cache with no pair":   {CacheRead: strp("0.3"), CacheWrite: strp("3.75")},
	}

	for name, price := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, _ := setupPricingSvc(t)

			_, err := svc.Create(readerCtx(), &CreateModelConfigRequest{
				Alias:      "refused",
				Provider:   "openai",
				ModelName:  "gpt-4",
				APIBase:    "https://llm.example.com/v1",
				AuthConfig: map[string]any{"type": "apikey", "apiKey": "sk-test"},
				Pricing:    price,
			}, "user1")
			if err == nil {
				t.Fatal("expected the price to be refused")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error was %T (%v), want a validation error", err, err)
			}
			if len(verr.Details) == 0 {
				t.Error("the refusal named no field")
			}
		})
	}
}

func TestAnUnparseableRateIsRefusedWithItsField(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	_, err := svc.Create(readerCtx(), &CreateModelConfigRequest{
		Alias:      "bad-rate",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "apiKey": "sk-test"},
		Pricing:    &platpkg.ModelPricing{Input: strp("0.000001"), Output: strp("free")},
	}, "user1")
	if err == nil {
		t.Fatal("expected the price to be refused")
	}

	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error was %T (%v), want a validation error", err, err)
	}
	for _, field := range []string{"pricing.input", "pricing.output"} {
		if len(verr.Details[field]) == 0 {
			t.Errorf("details %v do not name %s — an operator fixing a form sees one fault per round trip",
				verr.Details, field)
		}
	}
}

func TestUpdateLeavesPricingAloneWhenItIsNotAddressed(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	created := createModel(t, svc, "keep", &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")})

	desc := "a new description"
	updated, err := svc.Update(readerCtx(), created.ID, &UpdateModelConfigRequest{
		Description: &desc,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Pricing == nil || *updated.Pricing.Input != "3" {
		t.Fatalf("an update that said nothing about pricing changed it to %+v", updated.Pricing)
	}
}

func TestUpdateClearsPricingWhenItIsExplicitlyNull(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	created := createModel(t, svc, "clear-me", &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")})

	updated, err := svc.Update(readerCtx(), created.ID, &UpdateModelConfigRequest{
		Pricing: nil, PricingSet: true,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Pricing != nil {
		t.Fatalf("pricing survived a clear as %+v", updated.Pricing)
	}

	fetched, err := svc.GetByID(readerCtx(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Pricing != nil {
		t.Fatalf("the cleared price came back from the store as %+v", fetched.Pricing)
	}
}

func TestUpdateClearsPricingForAnObjectOfNulls(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	created := createModel(t, svc, "clear-empty", &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")})

	updated, err := svc.Update(readerCtx(), created.ID, &UpdateModelConfigRequest{
		Pricing: &platpkg.ModelPricing{},
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Pricing != nil {
		t.Fatalf("pricing survived an empty object as %+v", updated.Pricing)
	}
}

func TestUpdateDropsACacheBreakdownAndNullsCacheColumns(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	created := createModel(t, svc, "drop-cache", &platpkg.ModelPricing{
		Input: strp("3"), Output: strp("15"),
		CacheRead: strp("0.3"), CacheWrite: strp("3.75"),
	})

	updated, err := svc.Update(readerCtx(), created.ID, &UpdateModelConfigRequest{
		Pricing: &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")}, PricingSet: true,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Pricing == nil {
		t.Fatal("the model came back unpriced")
	}
	if updated.Pricing.CacheRead != nil || updated.Pricing.CacheWrite != nil {
		t.Errorf("cache rates survived as %v / %v", updated.Pricing.CacheRead, updated.Pricing.CacheWrite)
	}
}

func TestPricingIsWithheldFromACallerWithoutTheReadScope(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)

	created := createModel(t, svc, "priced", &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")})
	if created.Pricing == nil {
		t.Fatal("setup: the model was created unpriced")
	}

	callers := map[string]context.Context{
		"no identity at all": context.Background(),
		"identity with no scopes": auth.ContextWithSubject(context.Background(),
			auth.NewTestSubject(auth.SubjectFields{UserID: "chat-user"})),
		"identity with an unrelated scope": auth.ContextWithSubject(context.Background(),
			auth.NewTestSubject(auth.SubjectFields{UserID: "chat-user", Scopes: []string{"agent_builder:*:read"}})),
	}

	for name, ctx := range callers {
		t.Run(name, func(t *testing.T) {
			fetched, err := svc.GetByID(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if fetched.Pricing != nil {
				t.Errorf("rates reached an unentitled caller: %+v", *fetched.Pricing)
			}
			if fetched.Alias != "priced" {
				t.Errorf("the rest of the response was withheld too: alias %q", fetched.Alias)
			}
		})
	}
}

func TestPricingIsVisibleToTheAuthOffWildcardIdentity(t *testing.T) {
	t.Parallel()
	svc, _ := setupPricingSvc(t)
	created := createModel(t, svc, "priced", &platpkg.ModelPricing{Input: strp("3"), Output: strp("15")})

	ctx := auth.ContextWithSubject(context.Background(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "dev", Scopes: []string{"*"}}))
	fetched, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Pricing == nil {
		t.Fatal("the auth-off wildcard identity could not see rates")
	}
}

func TestADamagedStoredPriceReadsAsUnpricedAndIsLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	db := testOpenDB(t)
	svc, _ := setupPricingSvcOn(t, db, slog.New(slog.NewTextHandler(&logs, nil)))
	model := createModel(t, svc, "damaged", &platpkg.ModelPricing{
		Input: strp("3"), Output: strp("15"), CacheRead: strp("0.3"), CacheWrite: strp("3.75"),
	})

	damage := db.Rebind(`UPDATE model_configurations SET price_cache_read = ? WHERE id = ?`)
	if _, err := db.ExecContext(t.Context(), damage, int64(1)<<62, model.ID); err != nil {
		t.Fatalf("damage the row: %v", err)
	}

	got, err := svc.GetByID(readerCtx(), model.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Pricing != nil {
		t.Errorf("a damaged row read as %+v, want unpriced", *got.Pricing)
	}
	if line := logs.String(); !strings.Contains(line, "stored model price is invalid") || !strings.Contains(line, model.ID) {
		t.Errorf("no WARN naming the model; logs: %q", line)
	}
}
