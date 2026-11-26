// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { isWailsRuntime, newTabProps, openExternalLink } from "./openExternalLink";

describe("openExternalLink", () => {
    afterEach(() => {
        delete (window as unknown as { runtime?: unknown }).runtime;
        vi.restoreAllMocks();
    });

    test("uses Wails BrowserOpenURL when available", () => {
        const browserOpenURL = vi.fn();
        (window as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: browserOpenURL };
        const windowOpen = vi.spyOn(window, "open").mockImplementation(() => null);

        openExternalLink("http://localhost:8001/resource");

        expect(browserOpenURL).toHaveBeenCalledWith("http://localhost:8001/resource");
        expect(windowOpen).not.toHaveBeenCalled();
    });

    test("falls back to window.open in a normal browser", () => {
        const windowOpen = vi.spyOn(window, "open").mockImplementation(() => null);

        openExternalLink("https://example.com/spec.yaml");

        expect(windowOpen).toHaveBeenCalledWith("https://example.com/spec.yaml", "_blank", "noopener,noreferrer");
    });

    test("isWailsRuntime reflects the injected runtime", () => {
        expect(isWailsRuntime()).toBe(false);
        (window as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: vi.fn() };
        expect(isWailsRuntime()).toBe(true);
    });

    test("newTabProps opens a new tab in the browser", () => {
        expect(newTabProps()).toEqual({ target: "_blank", rel: "noopener noreferrer" });
    });

    test("newTabProps stays in-app (no target) in the desktop shell", () => {
        (window as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: vi.fn() };
        expect(newTabProps()).toEqual({});
    });
});
