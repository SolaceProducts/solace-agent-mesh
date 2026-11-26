// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { type DateRange, presetRange } from "./dashboardFilters";
import { useDashboardUrlState } from "./dashboardUrlState";

function useHookWithLocation(defaultRange?: DateRange) {
    return { state: useDashboardUrlState(defaultRange), search: useLocation().search };
}

const wrapperFor = (initialPath: string) =>
    function Wrapper({ children }: { children: ReactNode }) {
        return <MemoryRouter initialEntries={[initialPath]}>{children}</MemoryRouter>;
    };

describe("useDashboardUrlState", () => {
    it("reads defaults from an empty query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d") });
        expect(result.current.state).toMatchObject({ subjectKey: "all", subjectLabel: undefined, range: presetRange("90d"), hasActiveFilters: false });
    });

    it("reads valid values from the query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?subject=agent:a-1&name=Agent+One&range=7d") });
        expect(result.current.state).toMatchObject({ subjectKey: "agent:a-1", subjectLabel: "Agent One", range: presetRange("7d"), hasActiveFilters: true });
    });

    it("reads an explicit range from the query", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?range=2026-08-19..2026-09-17") });
        expect(result.current.state).toMatchObject({ range: { kind: "custom", from: "2026-08-19", to: "2026-09-17" }, hasActiveFilters: true });
    });

    it("falls back to defaults for a range value the catalog doesn't recognize", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?range=1y") });
        expect(result.current.state.range).toEqual(presetRange("90d"));
    });

    it("writes the subject and its label, and omits name when none is given", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d") });
        act(() => result.current.state.setSubject("agent:a-1", "Agent One"));
        expect(result.current.search).toBe("?subject=agent%3Aa-1&name=Agent+One");

        act(() => result.current.state.setSubject("workflow:w-1"));
        expect(result.current.search).toBe("?subject=workflow%3Aw-1");
    });

    it("clears subject and name together when reset to the all-subjects sentinel", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?subject=agent:a-1&name=Agent+One") });
        act(() => result.current.state.setSubject("all"));
        expect(result.current.search).toBe("");
    });

    it("writes an explicit range to the URL", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d") });
        act(() => result.current.state.setRange({ kind: "custom", from: "2026-08-19", to: "2026-09-17" }));
        expect(result.current.search).toBe("?range=2026-08-19..2026-09-17");
    });

    it("omits range from the URL when it's the default", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?range=7d") });
        act(() => result.current.state.setRange(presetRange("90d")));
        expect(result.current.search).toBe("");
    });

    it("reads an absent range as the retention-clamped default, not the hard-coded one", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/d") });
        expect(result.current.state.range).toEqual(presetRange("30d"));
        expect(result.current.state.hasActiveFilters).toBe(false);
    });

    it("treats the retention-clamped default as no filter", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/d?range=30d") });
        expect(result.current.state.hasActiveFilters).toBe(false);
    });

    it("omits range from the URL when it's the retention-clamped default", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/d?range=7d") });
        act(() => result.current.state.setRange(presetRange("30d")));
        expect(result.current.search).toBe("");
    });

    it("still counts a range shorter than the clamped default as a filter", () => {
        const { result } = renderHook(() => useHookWithLocation(presetRange("30d")), { wrapper: wrapperFor("/d?range=7d") });
        expect(result.current.state.hasActiveFilters).toBe(true);
    });

    it("resetFilters clears subject, name and range together", () => {
        const { result } = renderHook(useHookWithLocation, { wrapper: wrapperFor("/d?subject=agent:a-1&name=Agent+One&range=7d") });
        act(() => result.current.state.resetFilters());
        expect(result.current.search).toBe("");
    });
});
