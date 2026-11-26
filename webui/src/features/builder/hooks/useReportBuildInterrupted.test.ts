// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, Fragment, useLayoutEffect } from "react";
import { render, renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { useReportBuildInterrupted } from "./useReportBuildInterrupted";

describe("useReportBuildInterrupted", () => {
    test("reports the verdict on mount, whichever way it reads", () => {
        for (const verdict of [true, false]) {
            const report = vi.fn();
            renderHook(() => useReportBuildInterrupted(verdict, report));
            expect(report).toHaveBeenLastCalledWith(verdict);
        }
    });

    test("reports every change of the verdict, in order, ending on the current one", () => {
        const seen: boolean[] = [];
        const report = (v: boolean) => void seen.push(v);
        const { rerender } = renderHook(({ v }) => useReportBuildInterrupted(v, report), { initialProps: { v: false } });

        rerender({ v: true });
        rerender({ v: false });

        expect(seen.at(-1)).toBe(false);
        expect(seen.filter((v, i) => v !== seen[i - 1]).at(-2)).toBe(true);
    });

    test("withdraws the verdict when the panel goes away", () => {
        const report = vi.fn();
        const { unmount } = renderHook(() => useReportBuildInterrupted(true, report));
        expect(report).toHaveBeenLastCalledWith(true);

        unmount();

        expect(report).toHaveBeenLastCalledWith(false);
    });

    test("reports in the commit itself, before any passive effect or paint: a sibling's layout effect already sees it", () => {
        const report = vi.fn();
        let seenBySibling = -1;
        function Reporter() {
            useReportBuildInterrupted(true, report);
            return null;
        }
        function Sibling() {
            useLayoutEffect(() => {
                seenBySibling = report.mock.calls.length;
            });
            return null;
        }

        render(createElement(Fragment, null, createElement(Reporter), createElement(Sibling)));

        expect(seenBySibling).toBe(1);
    });

    test("is a no-op without a receiver", () => {
        expect(() => renderHook(() => useReportBuildInterrupted(true, undefined)).unmount()).not.toThrow();
    });

    test("a new receiver is told the current verdict, and the old one is told it is withdrawn", () => {
        const first = vi.fn();
        const second = vi.fn();
        const { rerender } = renderHook(({ report }) => useReportBuildInterrupted(true, report), { initialProps: { report: first } });

        rerender({ report: second });

        expect(first).toHaveBeenLastCalledWith(false);
        expect(second).toHaveBeenLastCalledWith(true);
    });
});
