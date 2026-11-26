// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { FeedbackFilterMemoryProvider } from "./feedbackFilterMemory";
import { type DateRange, DEFAULT_DATE_PRESET, presetRange } from "./feedbackFilters";
import { ANY_RATING, ANY_SUBJECT, useFeedbackHistoryUrlState } from "./feedbackHistoryUrlState";

function useHookWithLocation(defaultRange?: DateRange) {
    return { state: useFeedbackHistoryUrlState(defaultRange), search: useLocation().search };
}

const wrapperFor = (initialPath: string) =>
    function Wrapper({ children }: { children: ReactNode }) {
        return (
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={[initialPath]}>{children}</MemoryRouter>
            </FeedbackFilterMemoryProvider>
        );
    };

describe("useFeedbackHistoryUrlState", () => {
    it("reads defaults from an empty query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h") });
        expect(result.current.state).toMatchObject({ subjectKey: ANY_SUBJECT, rating: ANY_RATING, range: presetRange(DEFAULT_DATE_PRESET), page: 1, selectedId: null, hasActiveFilters: false });
    });

    it("reads valid values from the query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?subject=agent:a-1&rating=down&range=30d&page=3&selected=f9") });
        expect(result.current.state).toMatchObject({ subjectKey: "agent:a-1", rating: "down", range: { kind: "preset", preset: "30d" }, page: 3, selectedId: "f9", hasActiveFilters: true });
    });

    it("accepts the 24-hour preset the merged catalog adds", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?range=24h") });
        expect(result.current.state.range).toEqual({ kind: "preset", preset: "24h" });
    });

    it("falls back to defaults for values the API would reject", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?rating=sideways&range=1y&page=0") });
        expect(result.current.state).toMatchObject({ rating: ANY_RATING, range: presetRange(DEFAULT_DATE_PRESET), page: 1 });
        const { result: alpha } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=abc") });
        expect(alpha.current.state.page).toBe(1);
        const { result: badKind } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?subject=foo") });
        expect(badKind.current.state.subjectKey).toBe(ANY_SUBJECT);
        const { result: noId } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?subject=agent:") });
        expect(noId.current.state.subjectKey).toBe(ANY_SUBJECT);
    });

    it("writes a filter, drops the page, and omits default values", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=4&rating=up") });
        act(() => result.current.state.setRating("down"));
        expect(result.current.search).toBe("?rating=down");
        act(() => result.current.state.setRating(ANY_RATING));
        expect(result.current.search).toBe("");
    });

    it("reads an explicit range from the query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?range=2026-08-19..2026-09-17") });
        expect(result.current.state.range).toEqual({ kind: "custom", from: "2026-08-19", to: "2026-09-17" });
        expect(result.current.state.hasActiveFilters).toBe(true);
    });

    it("falls back to the default for an explicit range that is impossible or reversed", () => {
        const { result: impossible } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?range=2026-02-30..2026-03-05") });
        expect(impossible.current.state.range).toEqual(presetRange(DEFAULT_DATE_PRESET));
        const { result: reversed } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?range=2026-09-17..2026-08-19") });
        expect(reversed.current.state.range).toEqual(presetRange(DEFAULT_DATE_PRESET));
        expect(reversed.current.state.hasActiveFilters).toBe(false);
    });

    it("drops the page when the range changes, so page 3 of an old window is never requested", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=3") });
        act(() => result.current.state.setRange({ kind: "custom", from: "2026-08-19", to: "2026-09-17" }));
        expect(result.current.search).toBe("?range=2026-08-19..2026-09-17");
    });

    it("drops the page for a replacing write too, so a clamp cannot strand the reviewer on a window that is gone", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=3&range=90d") });
        act(() => result.current.state.setRange(presetRange("30d"), { replace: true }));
        expect(result.current.search).toBe("?range=30d");
    });

    it("writes a preset range by name and omits the default", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h") });
        act(() => result.current.state.setRange(presetRange("7d")));
        expect(result.current.search).toBe("?range=7d");
        act(() => result.current.state.setRange(presetRange(DEFAULT_DATE_PRESET)));
        expect(result.current.search).toBe("");
    });

    it("clearFilters drops an explicit range too", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?range=2026-08-19..2026-09-17&selected=f1") });
        act(() => result.current.state.clearFilters());
        expect(result.current.search).toBe("?selected=f1");
    });

    it("keeps the page when only the selection changes", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=2") });
        act(() => result.current.state.setSelectedId("f1"));
        expect(result.current.search).toBe("?page=2&selected=f1");
        act(() => result.current.state.setSelectedId(null));
        expect(result.current.search).toBe("?page=2");
    });

    it("clearFilters resets filters and page but keeps the selection", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?subject=agent:a-1&rating=down&range=7d&page=3&selected=f1") });
        act(() => result.current.state.clearFilters());
        expect(result.current.search).toBe("?selected=f1");
    });

    it("persists the subject's label alongside its key, and clears it with the subject", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h") });
        act(() => result.current.state.setSubjectKey("agent:a-1", "Agent One"));
        expect(result.current.search).toBe("?subject=agent%3Aa-1&name=Agent+One");

        act(() => result.current.state.setSubjectKey(ANY_SUBJECT));
        expect(result.current.search).toBe("");
    });

    it("omits name when no label is given for a real subject", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h") });
        act(() => result.current.state.setSubjectKey("workflow:w-1"));
        expect(result.current.search).toBe("?subject=workflow%3Aw-1");
    });

    it("reads the user filter from the query and writes it back", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?user=olive%40example.com") });
        expect(result.current.state.userId).toBe("olive@example.com");
        expect(result.current.state.hasActiveFilters).toBe(true);

        act(() => result.current.state.setUserId(null));
        expect(result.current.search).toBe("");
        expect(result.current.state.userId).toBeNull();
    });

    it("has no user filter by default", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h") });
        expect(result.current.state.userId).toBeNull();
        expect(result.current.state.hasActiveFilters).toBe(false);
    });

    it("drops the page when the user filter changes", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?page=3") });
        act(() => result.current.state.setUserId("olive@example.com"));
        expect(result.current.search).toBe("?user=olive%40example.com");
    });

    it("clears the user filter alongside the others", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/h?user=olive%40example.com&rating=down") });
        act(() => result.current.state.clearFilters());
        expect(result.current.search).toBe("");
    });

    it("reads an absent range as the retention-clamped default, not the hard-coded one", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/h") });
        expect(result.current.state.range).toEqual(presetRange("30d"));
        expect(result.current.state.hasActiveFilters).toBe(false);
    });

    it("treats the retention-clamped default as no filter", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/h?range=30d") });
        expect(result.current.state.hasActiveFilters).toBe(false);
    });

    it("omits range from the URL when it's the retention-clamped default", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/h?range=7d") });
        act(() => result.current.state.setRange(presetRange("30d")));
        expect(result.current.search).toBe("");
    });
});
