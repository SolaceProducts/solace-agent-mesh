// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import "testing"

func TestDurationStats(t *testing.T) {
	avg, p50, p95 := durationStats([]int64{100, 200, 300, 400, 500})
	if avg != 300 {
		t.Errorf("avg = %d, want 300", avg)
	}
	if p50 != 300 {
		t.Errorf("p50 = %d, want 300", p50)
	}
	if p95 != 500 {
		t.Errorf("p95 = %d, want 500", p95)
	}

	if a, p, q := durationStats(nil); a != 0 || p != 0 || q != 0 {
		t.Errorf("empty durations = (%d,%d,%d), want all 0", a, p, q)
	}
}

func TestBucketDurations_PopulatedForWindowsWithCompletedTasks(t *testing.T) {
	counts := bucketDurations([]int64{50, 100, 249, 500, 400_000})
	if len(counts) != 12 {
		t.Fatalf("bucket len = %d, want 12", len(counts))
	}
	want := []int{1, 2, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}
	for i, w := range want {
		if counts[i] != w {
			t.Errorf("bucket[%d] = %d, want %d (counts=%v)", i, counts[i], w, counts)
		}
	}

	if got := bucketDurations(nil); got != nil {
		t.Errorf("empty input = %v, want nil", got)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	s := []int64{10, 20, 30, 40}
	if got := percentile(s, 50); got != 20 {
		t.Errorf("p50 = %d, want 20", got)
	}
	if got := percentile(s, 100); got != 40 {
		t.Errorf("p100 = %d, want 40", got)
	}
	if got := percentile([]int64{42}, 95); got != 42 {
		t.Errorf("single-element p95 = %d, want 42", got)
	}
}

func TestRatio(t *testing.T) {
	if r := ratio(3, 4); r != 0.75 {
		t.Errorf("ratio(3,4) = %v, want 0.75", r)
	}
	if r := ratio(1, 0); r != 0 {
		t.Errorf("ratio(1,0) = %v, want 0 (no div-by-zero)", r)
	}
}

func TestPaginateSlice(t *testing.T) {
	all := []int{0, 1, 2, 3, 4}

	p1 := paginateSlice(all, 1, 2)
	if len(p1.Data) != 2 || p1.Data[0] != 0 || p1.Data[1] != 1 {
		t.Errorf("page1 data = %v, want [0 1]", p1.Data)
	}
	if p1.Meta.Pagination.TotalPages != 3 {
		t.Errorf("totalPages = %d, want 3", p1.Meta.Pagination.TotalPages)
	}
	if p1.Meta.Pagination.NextPage == nil || *p1.Meta.Pagination.NextPage != 2 {
		t.Errorf("nextPage = %v, want 2", p1.Meta.Pagination.NextPage)
	}
	if p1.Meta.Pagination.Count != 5 {
		t.Errorf("count = %d, want 5", p1.Meta.Pagination.Count)
	}

	p3 := paginateSlice(all, 3, 2)
	if len(p3.Data) != 1 || p3.Data[0] != 4 {
		t.Errorf("page3 data = %v, want [4]", p3.Data)
	}
	if p3.Meta.Pagination.NextPage != nil {
		t.Errorf("page3 nextPage = %v, want nil", p3.Meta.Pagination.NextPage)
	}

	p9 := paginateSlice(all, 9, 2)
	if p9.Data == nil || len(p9.Data) != 0 {
		t.Errorf("out-of-range page data = %v, want empty non-nil", p9.Data)
	}
}

func TestResourceIDFromSlug(t *testing.T) {
	if got := resourceIDFromSlug("agent_019ea471_5051_73c7_893a_7679460fd074"); got != "019ea471-5051-73c7-893a-7679460fd074" {
		t.Errorf("agent slug = %q, want the dashed UUID", got)
	}
	if got := resourceIDFromSlug("workflow_9f8e7d6c_1234_4321_8888_abcdefabcdef"); got != "9f8e7d6c-1234-4321-8888-abcdefabcdef" {
		t.Errorf("workflow slug = %q, want the dashed UUID", got)
	}
	if got := resourceIDFromSlug("Builder"); got != "Builder" {
		t.Errorf("resourceIDFromSlug(non-slug) = %q, want Builder", got)
	}
}

func TestAgentRef_ResolvesDisplayName(t *testing.T) {
	const slug = "agent_019ea471_5051_73c7_893a_7679460fd074"
	const id = "019ea471-5051-73c7-893a-7679460fd074"

	svc := (&Service{}).WithAgentNameResolver(func(s string) string {
		if s == slug {
			return "Support"
		}
		return ""
	})
	if ref := svc.agentRef(slug); ref.ID != id || ref.Name != "Support" {
		t.Errorf("agentRef = %+v, want {ID:%s Name:Support}", ref, id)
	}

	unknown := (&Service{}).WithAgentNameResolver(func(string) string { return "" }).agentRef(slug)
	if unknown.ID != id || unknown.Name != slug {
		t.Errorf("agentRef(unknown) = %+v, want id=%s name=slug", unknown, id)
	}

	if ref := (&Service{}).agentRef(slug); ref.ID != id || ref.Name != slug {
		t.Errorf("agentRef(nil resolver) = %+v, want id=%s name=slug", ref, id)
	}
}

func TestOptISO(t *testing.T) {
	if optISO(0) != nil {
		t.Error("optISO(0) should be nil")
	}
	got := optISO(1_700_000_000_000)
	if got == nil {
		t.Fatal("optISO(positive) should not be nil")
	}
	if *got != "2023-11-14T22:13:20.000000+00:00" {
		t.Errorf("optISO = %q, want 2023-11-14T22:13:20.000000+00:00", *got)
	}
}
