// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import meta, { Default } from "./ProjectDetailView.stories";

const setMobile = (mobile: boolean) => {
    Object.defineProperty(window, "innerWidth", { value: mobile ? 375 : 1280, configurable: true });
    window.matchMedia = vi
        .fn()
        .mockImplementation((q: string) => ({ matches: mobile && q.includes("max-width"), media: q, onchange: null, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() }));
};

describe("ProjectDetailView mobile layout", () => {
    afterEach(() => vi.restoreAllMocks());

    test("renders Chats/Context tabs on mobile", async () => {
        setMobile(true);
        const Story = composeStory(Default, meta);
        render(<Story />);
        expect(await screen.findByRole("tab", { name: /context/i })).toBeInTheDocument();
    });

    test("does not render the mobile tabs on desktop", () => {
        setMobile(false);
        const Story = composeStory(Default, meta);
        render(<Story />);
        expect(screen.queryByRole("tab", { name: /context/i })).not.toBeInTheDocument();
    });
});

describe("ProjectDetailView actions menu", () => {
    afterEach(() => vi.restoreAllMocks());

    test("shows 'Export Project' in the kebab menu and fires onExport when clicked", async () => {
        setMobile(false);
        const user = userEvent.setup();
        const onExport = vi.fn();
        const Story = composeStory(Default, meta);
        render(<Story onExport={onExport} />);

        await user.click(await screen.findByRole("button", { name: "More options" }));

        const exportItem = await screen.findByTestId("exportProjectButton");
        expect(exportItem).toHaveTextContent("Export Project");

        await user.click(exportItem);
        await waitFor(() => expect(onExport).toHaveBeenCalledTimes(1));
    });

    test("omits 'Export Project' from the kebab menu when onExport is not provided", async () => {
        setMobile(false);
        const user = userEvent.setup();
        const Story = composeStory(Default, meta);
        render(<Story onExport={undefined} />);

        await user.click(await screen.findByRole("button", { name: "More options" }));

        await waitFor(() => expect(screen.getByText("Delete")).toBeInTheDocument());
        expect(screen.queryByTestId("exportProjectButton")).not.toBeInTheDocument();
    });
});
