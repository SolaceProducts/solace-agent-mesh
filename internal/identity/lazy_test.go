// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"testing"
)

type fakeDelegate struct {
	resolveCalls   int
	searchCalls    int
	profileCalls   int
	timeOffCalls   int
	pictureCalls   int
	wantResolveErr error
}

func (f *fakeDelegate) Resolve(_ context.Context, _ map[string]any) (*Resolution, error) {
	f.resolveCalls++
	if f.wantResolveErr != nil {
		return nil, f.wantResolveErr
	}
	return &Resolution{EnrichmentStatus: EnrichmentOK, Profile: &Profile{ID: "u1"}}, nil
}
func (f *fakeDelegate) SearchUsers(_ context.Context, _ string, _ int) ([]*Profile, error) {
	f.searchCalls++
	return []*Profile{{ID: "u1"}}, nil
}
func (f *fakeDelegate) GetUserProfile(_ context.Context, _ string) (map[string]any, error) {
	f.profileCalls++
	return map[string]any{"id": "u1"}, nil
}
func (f *fakeDelegate) GetTimeOff(_ context.Context, _, _, _ string) ([]TimeOffEntry, error) {
	f.timeOffCalls++
	return []TimeOffEntry{{Type: "Vacation"}}, nil
}
func (f *fakeDelegate) GetUserProfilePicture(_ context.Context, _ string) ([]byte, string, error) {
	f.pictureCalls++
	return []byte("png"), "image/png", nil
}

func TestLazyService_NoDelegate_DegradesGracefully(t *testing.T) {
	t.Parallel()
	l := NewLazyService()

	res, err := l.Resolve(t.Context(), nil)
	if err != nil {
		t.Fatalf("Resolve: unexpected error %v", err)
	}
	if res == nil || res.EnrichmentStatus != EnrichmentNotConfigured {
		t.Errorf("Resolve before SetDelegate: status = %v, want %v", res, EnrichmentNotConfigured)
	}

	profiles, err := l.SearchUsers(t.Context(), "q", 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("SearchUsers: got %d profiles, want 0", len(profiles))
	}

	prof, err := l.GetUserProfile(t.Context(), "id")
	if err != nil || prof != nil {
		t.Errorf("GetUserProfile: prof=%v err=%v, want nil/nil", prof, err)
	}
	entries, err := l.GetTimeOff(t.Context(), "id", "", "")
	if err != nil || entries != nil {
		t.Errorf("GetTimeOff: entries=%v err=%v, want nil/nil", entries, err)
	}
	img, mime, err := l.GetUserProfilePicture(t.Context(), "id")
	if err != nil || img != nil || mime != "" {
		t.Errorf("GetUserProfilePicture: img=%v mime=%q err=%v", img, mime, err)
	}
}

func TestLazyService_SetDelegate_RoutesToDelegate(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	d := &fakeDelegate{}
	l.SetDelegate(d)

	if _, err := l.Resolve(t.Context(), nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := l.SearchUsers(t.Context(), "q", 10); err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if _, err := l.GetUserProfile(t.Context(), "id"); err != nil {
		t.Fatalf("GetUserProfile: %v", err)
	}
	if _, err := l.GetTimeOff(t.Context(), "id", "", ""); err != nil {
		t.Fatalf("GetTimeOff: %v", err)
	}
	if _, _, err := l.GetUserProfilePicture(t.Context(), "id"); err != nil {
		t.Fatalf("GetUserProfilePicture: %v", err)
	}

	if d.resolveCalls != 1 || d.searchCalls != 1 || d.profileCalls != 1 || d.timeOffCalls != 1 || d.pictureCalls != 1 {
		t.Errorf("delegate calls: resolve=%d search=%d profile=%d timeOff=%d picture=%d, want 1 each",
			d.resolveCalls, d.searchCalls, d.profileCalls, d.timeOffCalls, d.pictureCalls)
	}
}

func TestLazyService_SetDelegate_PropagatesError(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	wantErr := errors.New("upstream is down")
	l.SetDelegate(&fakeDelegate{wantResolveErr: wantErr})

	_, err := l.Resolve(t.Context(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resolve err = %v, want %v", err, wantErr)
	}
}

func TestLazyService_SetDelegate_ReplacesExisting(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	first := &fakeDelegate{}
	second := &fakeDelegate{}

	l.SetDelegate(first)
	l.SetDelegate(second)
	if l.Delegate() != second {
		t.Errorf("Delegate() = %v, want %v after replace", l.Delegate(), second)
	}

	if _, err := l.Resolve(t.Context(), nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.resolveCalls != 0 {
		t.Errorf("first delegate Resolve calls = %d, want 0 after replace", first.resolveCalls)
	}
	if second.resolveCalls != 1 {
		t.Errorf("second delegate Resolve calls = %d, want 1", second.resolveCalls)
	}
}

func TestLazyService_Delegate_ReturnsCurrent(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	if l.Delegate() != nil {
		t.Errorf("Delegate before SetDelegate: want nil")
	}
	d := &fakeDelegate{}
	l.SetDelegate(d)
	if l.Delegate() != d {
		t.Errorf("Delegate after SetDelegate: got %v, want %v", l.Delegate(), d)
	}
}

func TestLazyService_SearchConfigured_NoDelegate(t *testing.T) {
	t.Parallel()
	ok, err := NewLazyService().SearchConfigured(context.Background())
	if err != nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestLazyService_SearchConfigured_Forwards(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	l.SetDelegate(New(&fakeConfig{configured: true, toolsetID: "ts1"}, nil, &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}, nil))

	ok, err := l.SearchConfigured(context.Background())
	if err != nil || !ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestLazyService_SearchConfigured_DelegateWithoutMethod(t *testing.T) {
	t.Parallel()
	l := NewLazyService()
	l.SetDelegate(&fakeDelegate{})

	ok, err := l.SearchConfigured(context.Background())
	if err != nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, nil)", ok, err)
	}
}
