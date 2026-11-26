// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import meta, { Default } from "./VersionHistoryPage.stories";

const setViewport = (mobile: boolean) => {
    Object.defineProperty(window, "innerWidth", { value: mobile ? 375 : 1280, configurable: true });
    window.matchMedia = vi
        .fn()
        .mockImplementation((q: string) => ({ matches: mobile && q.includes("max-width"), media: q, onchange: null, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() }));
};

describe("VersionHistoryPage mobile layout", () => {
    afterEach(() => vi.restoreAllMocks());

    test("shows the versions list toggle on mobile", async () => {
        setViewport(true);
        const Story = composeStory(Default, meta);
        render(<Story />);
        expect(await screen.findByRole("button", { name: /show versions list/i })).toBeInTheDocument();
    });
});

describe("VersionHistoryPage loading state", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    test("shows the standard circular Spinner while versions load", async () => {
        setViewport(false);
        vi.stubGlobal("fetch", vi.fn().mockReturnValue(new Promise(() => {})));
        const Story = composeStory(Default, meta);
        const { container } = render(<Story />);
        await waitFor(() => expect(container.querySelector("svg.animate-spin")).toBeInTheDocument());
    });
});
