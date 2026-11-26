// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

func TestEverySkillAdviceNamesIsInTheSkillsCatalog(t *testing.T) {
	owners := builtinskills.ToolOwningSkills()
	if len(owners) == 0 {
		t.Fatal("no built-in skill declares a tool manifest; the check asserted nothing")
	}
	for _, skill := range owners {
		if !skillCatalogHas(builtinSkillResponses, skill) {
			t.Errorf("toolbind advises listing %q under skills:, but it is not in builtinSkillResponses; add it there or to builtinskills' internal set", skill)
		}
	}
}

func skillCatalogHas(skills []SkillDetailResponse, id string) bool {
	for _, s := range skills {
		if s.ID == id {
			return true
		}
	}
	return false
}

func TestVisibleBuiltinSkills_GatesPythonExecutor(t *testing.T) {
	t.Parallel()

	nilReg := visibleBuiltinSkills(nil)
	if skillCatalogHas(nilReg, "sam-python-executor") {
		t.Error("nil registry: sam-python-executor should be hidden")
	}
	if !skillCatalogHas(nilReg, "sam-docs") {
		t.Error("nil registry: sam-docs should still be visible")
	}

	off := features.NewRegistry()
	off.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: false})
	if skillCatalogHas(visibleBuiltinSkills(off), "sam-python-executor") {
		t.Error("flag off: sam-python-executor should be hidden")
	}

	on := features.NewRegistry()
	on.Register(features.Definition{Key: pythonCodeExecutionFeatureKey, Default: true})
	if !skillCatalogHas(visibleBuiltinSkills(on), "sam-python-executor") {
		t.Error("flag on: sam-python-executor should be visible")
	}

	if !builtinskills.HasName("sam-python-executor") {
		t.Error("sam-python-executor must be an embedded built-in skill (valid skill_ref) regardless of the flag")
	}
}

func TestKeepSkillRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref    string
		hasM2M bool
		want   bool
	}{
		{"sam-docs", true, true},
		{"my-custom-skill", true, false},
		{"my-custom-skill", false, true},
		{"sam-docs", false, true},
	}
	for _, c := range cases {
		if got := keepSkillRef(c.ref, c.hasM2M); got != c.want {
			t.Errorf("keepSkillRef(%q, %v) = %v, want %v", c.ref, c.hasM2M, got, c.want)
		}
	}
}

func TestBuiltinSkillCatalog_Labels(t *testing.T) {
	t.Parallel()
	for _, s := range builtinSkillResponses {
		if s.Label == "" {
			t.Errorf("built-in skill %q missing Label", s.ID)
		}
		if s.ID != s.Name {
			t.Errorf("built-in skill ID %q != Name %q (matching is by slug)", s.ID, s.Name)
		}
	}
}

func newReconcileTestStore() *objectstore.Store {
	return objectstore.NewStore(objectstore.NewMemoryClient())
}

func TestReconcileBuiltinSkills_UploadsAllOnEmptyStore(t *testing.T) {
	t.Parallel()
	store := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	if err := reconcileBuiltinSkills(ctx, store, "test-ns", logger); err != nil {
		t.Fatalf("reconcileBuiltinSkills: %v", err)
	}

	for _, name := range builtinskills.Names() {
		key := skillRuntimeKey("test-ns", name)
		md, headErr := store.Head(ctx, key)
		if headErr != nil {
			t.Fatalf("expected %q to exist after reconcile: %v", key, headErr)
		}
		if md[builtinSkillHashMetadata] == "" {
			t.Errorf("missing %q metadata for %q (have %v)", builtinSkillHashMetadata, key, md)
		}
		rc, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get %q: %v", key, err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if len(data) == 0 {
			t.Errorf("reconciled %q is empty", key)
		}
	}
}

func TestReconcileBuiltinSkills_SkipsWhenHashMatches(t *testing.T) {
	t.Parallel()
	rs := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	if err := reconcileBuiltinSkills(ctx, rs, "ns", logger); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	tracker := &putCountingStore{Store: rs}
	if err := reconcileBuiltinSkills(ctx, tracker, "ns", logger); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if tracker.puts != 0 {
		t.Errorf("expected zero PutWithMetadata calls on no-op reconcile, got %d", tracker.puts)
	}
}

func TestReconcileBuiltinSkills_ReuploadsWhenHashStale(t *testing.T) {
	t.Parallel()
	rs := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	if err := reconcileBuiltinSkills(ctx, rs, "ns", logger); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	for _, name := range builtinskills.Names() {
		key := skillRuntimeKey("ns", name)
		if err := rs.PutWithMetadata(ctx, key, strings.NewReader("stale"), map[string]string{builtinSkillHashMetadata: "stale-hash"}); err != nil {
			t.Fatalf("seed stale: %v", err)
		}
	}

	tracker := &putCountingStore{Store: rs}
	if err := reconcileBuiltinSkills(ctx, tracker, "ns", logger); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if tracker.puts != len(builtinskills.Names()) {
		t.Errorf("expected %d re-uploads, got %d", len(builtinskills.Names()), tracker.puts)
	}

	for _, name := range builtinskills.Names() {
		key := skillRuntimeKey("ns", name)
		md, headErr := rs.Head(ctx, key)
		if headErr != nil {
			t.Fatalf("Head %q: %v", key, headErr)
		}
		_, want, _ := builtinskills.HashZip(name)
		if md[builtinSkillHashMetadata] != want {
			t.Errorf("hash on %q = %q, want %q", key, md[builtinSkillHashMetadata], want)
		}
	}
}

func TestReconcileBuiltinSkills_NilStoreRejected(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	if err := reconcileBuiltinSkills(context.Background(), nil, "ns", logger); err == nil {
		t.Error("expected error from nil store")
	}
}

func TestSkillService_Create_RejectsSamPrefix(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))

	for _, name := range []string{"sam-anything", "sam-knowledge", "sam-"} {
		_, err := svc.Create(context.Background(), CreateSkillRequest{Name: name, Description: "x"}, "user-1")
		if err == nil {
			t.Errorf("Create(%q) returned nil error, expected rejection", name)
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("Create(%q) returned non-ValidationError: %T %v", name, err, err)
			continue
		}
		if !strings.Contains(ve.Error(), "reserved") {
			t.Errorf("Create(%q) error %q does not mention 'reserved'", name, ve.Error())
		}
	}
}

func TestSkillService_Create_AcceptsNonSamPrefix(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))

	resp, err := svc.Create(context.Background(), CreateSkillRequest{Name: "my-skill", Description: "ok"}, "user-1")
	if err != nil {
		t.Fatalf("Create(my-skill): %v", err)
	}
	if resp.Name != "my-skill" {
		t.Errorf("got name %q, want %q", resp.Name, "my-skill")
	}
}

type putCountingStore struct {
	*objectstore.Store
	puts int
}

func (p *putCountingStore) PutWithMetadata(ctx context.Context, key string, r io.Reader, md map[string]string) error {
	p.puts++
	return p.Store.PutWithMetadata(ctx, key, r, md)
}

type flakyHeadStore struct {
	*objectstore.Store
}

var errFakeHeadFailure = errors.New("fake transient head failure")

func (f *flakyHeadStore) Head(_ context.Context, _ string) (map[string]string, error) {
	return nil, errFakeHeadFailure
}

func TestReconcileBuiltinSkills_FallsThroughOnTransientHeadError(t *testing.T) {
	t.Parallel()
	rs := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	tracker := &putCountingStore{Store: rs}
	flaky := &flakyHeadStoreWithTracking{flakyHeadStore: flakyHeadStore{Store: rs}, tracker: tracker}
	if err := reconcileBuiltinSkills(ctx, flaky, "ns", logger); err != nil {
		t.Fatalf("reconcile under flaky Head: %v", err)
	}
	if tracker.puts != len(builtinskills.Names()) {
		t.Errorf("expected upload of every skill (%d) under flaky Head, got %d", len(builtinskills.Names()), tracker.puts)
	}
}

type flakyHeadStoreWithTracking struct {
	flakyHeadStore
	tracker *putCountingStore
}

func (f *flakyHeadStoreWithTracking) PutWithMetadata(ctx context.Context, key string, r io.Reader, md map[string]string) error {
	return f.tracker.PutWithMetadata(ctx, key, r, md)
}

type failingPutStore struct {
	*objectstore.Store
	failKey string
	puts    int
}

func (f *failingPutStore) PutWithMetadata(ctx context.Context, key string, r io.Reader, md map[string]string) error {
	f.puts++
	if key == f.failKey {
		return errors.New("simulated upload failure")
	}
	return f.Store.PutWithMetadata(ctx, key, r, md)
}

func TestReconcileBuiltinSkills_ContinuesAfterPerSkillFailure(t *testing.T) {
	t.Parallel()
	rs := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	names := builtinskills.Names()
	if len(names) < 2 {
		t.Skipf("need >=2 builtin skills, have %d", len(names))
	}
	failKey := skillRuntimeKey("ns", names[0])
	store := &failingPutStore{Store: rs, failKey: failKey}

	err := reconcileBuiltinSkills(ctx, store, "ns", logger)
	if err == nil {
		t.Error("expected error from reconcile when one skill fails")
	}
	if store.puts != len(names) {
		t.Errorf("expected %d upload attempts, got %d", len(names), store.puts)
	}
	for i := 1; i < len(names); i++ {
		if _, headErr := rs.Head(ctx, skillRuntimeKey("ns", names[i])); headErr != nil {
			t.Errorf("expected %q to be uploaded after sibling failure: %v", names[i], headErr)
		}
	}
}

func TestPruneOrphanedBundledSkills_DeletesOnlySamPrefixedOrphans(t *testing.T) {
	t.Parallel()
	rs := newReconcileTestStore()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	const ns = "ns"

	seed := func(name string) string {
		key := skillRuntimeKey(ns, name)
		if err := rs.Put(ctx, key, bytes.NewReader([]byte("stale"))); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
		return key
	}
	orphanKey := seed("sam-gone")
	userKey := seed("my-skill")
	legacyKey := seed("test_harness")

	for _, name := range builtinskills.Names() {
		seed(name)
	}
	designNames := design.Skills()
	if len(designNames) == 0 {
		t.Fatal("expected at least one design skill to guard the keep set")
	}
	for _, sk := range designNames {
		seed(sk.Name)
	}

	pruneOrphanedBundledSkills(ctx, rs, ns, logger)

	if _, err := rs.Head(ctx, orphanKey); err == nil {
		t.Errorf("expected sam-prefixed orphan %q to be pruned, but it survived", orphanKey)
	}
	if _, err := rs.Head(ctx, userKey); err != nil {
		t.Errorf("user skill %q must not be pruned: %v", userKey, err)
	}
	if _, err := rs.Head(ctx, legacyKey); err != nil {
		t.Errorf("legacy non-prefixed object %q must not be pruned: %v", legacyKey, err)
	}
	for _, name := range builtinskills.Names() {
		if _, err := rs.Head(ctx, skillRuntimeKey(ns, name)); err != nil {
			t.Errorf("current built-in %q must survive prune: %v", name, err)
		}
	}
	for _, sk := range designNames {
		if _, err := rs.Head(ctx, skillRuntimeKey(ns, sk.Name)); err != nil {
			t.Errorf("sam-prefixed design skill %q must survive prune: %v", sk.Name, err)
		}
	}
}

func TestPruneOrphanedBundledSkills_NoListerIsNoOp(t *testing.T) {
	t.Parallel()
	pruneOrphanedBundledSkills(context.Background(), nonListingStore{}, "ns", slog.New(slog.DiscardHandler))
}

type nonListingStore struct{}

func (nonListingStore) Put(context.Context, string, io.Reader) error { return nil }
func (nonListingStore) PutWithMetadata(context.Context, string, io.Reader, map[string]string) error {
	return nil
}
func (nonListingStore) Get(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (nonListingStore) Head(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (nonListingStore) Delete(context.Context, string) error { return nil }
