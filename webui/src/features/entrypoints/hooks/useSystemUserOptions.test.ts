// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

describe("useSystemUserOptions", () => {
    let useSystemUserOptions: typeof import("@/features/entrypoints/hooks/useSystemUserOptions").useSystemUserOptions;
    let pinRunAsValue: typeof import("@/features/entrypoints/hooks/useSystemUserOptions").pinRunAsValue;
    let mockSystemUsers: { name: string; displayName?: string }[] | undefined | null;
    let mockIsError: boolean;

    beforeEach(async () => {
        vi.resetModules();
        mockSystemUsers = [];
        mockIsError = false;
        vi.doMock("@/api/rbac", () => ({
            useSystemUsers: () => ({ data: mockSystemUsers, isError: mockIsError }),
        }));
        const mod = await import("@/features/entrypoints/hooks/useSystemUserOptions");
        useSystemUserOptions = mod.useSystemUserOptions;
        pinRunAsValue = mod.pinRunAsValue;
    });

    test("returns empty options while loading (undefined data)", () => {
        mockSystemUsers = undefined;
        const { result } = renderHook(() => useSystemUserOptions());
        expect(result.current.options).toEqual([]);
        expect(result.current.isError).toBe(false);
    });

    test("does not crash on a null data payload", () => {
        mockSystemUsers = null;
        const { result } = renderHook(() => useSystemUserOptions());
        expect(result.current.options).toEqual([]);
    });

    test("prefers displayName, falling back to name", () => {
        mockSystemUsers = [{ name: "helpdesk", displayName: "Helpdesk" }, { name: "bare" }];
        const { result } = renderHook(() => useSystemUserOptions());
        expect(result.current.options).toEqual([
            { name: "bare", displayName: "bare" },
            { name: "helpdesk", displayName: "Helpdesk" },
        ]);
    });

    test("always sorts the default system user first regardless of display name", () => {
        mockSystemUsers = [
            { name: "alpha", displayName: "Alpha" },
            { name: "default", displayName: "Default" },
        ];
        const { result } = renderHook(() => useSystemUserOptions());
        expect(result.current.options.map(o => o.name)).toEqual(["default", "alpha"]);
    });

    test("surfaces the fetch-error flag (empty options are then 'error', not 'none')", () => {
        mockSystemUsers = [];
        mockIsError = true;
        const { result } = renderHook(() => useSystemUserOptions());
        expect(result.current.options).toEqual([]);
        expect(result.current.isError).toBe(true);
    });

    test("options stay referentially stable across re-renders", () => {
        mockSystemUsers = [{ name: "default", displayName: "Default" }];
        const { result, rerender } = renderHook(() => useSystemUserOptions());
        const first = result.current.options;
        rerender();
        expect(result.current.options).toBe(first);
    });

    describe("pinRunAsValue", () => {
        const options = [{ name: "default", displayName: "Default" }];

        test("appends a configured value that isn't in the fetched list (403 / stale)", () => {
            expect(pinRunAsValue(options, "helpdesk")).toEqual([
                { name: "default", displayName: "Default" },
                { name: "helpdesk", displayName: "helpdesk" },
            ]);
        });

        test("leaves the list unchanged when the value already exists", () => {
            expect(pinRunAsValue(options, "default")).toBe(options);
        });

        test("leaves a blank value alone (placeholder handles 'nothing selected')", () => {
            expect(pinRunAsValue(options, "")).toBe(options);
            expect(pinRunAsValue(options, undefined)).toBe(options);
        });
    });
});
