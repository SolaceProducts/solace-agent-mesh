// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, within } from "@testing-library/react";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { disableWailsRuntime, enableWailsRuntime, withDefaults } from "@/testing/helpers";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { usageHandlers } from "@/testing/msw/usageHandlers";

import { UsagePageContainer } from "./UsagePageContainer";

const server = setupServer(...withDefaults(...usageHandlers()));

beforeAll(() => server.listen());
afterEach(() => {
    server.resetHandlers();
    disableWailsRuntime();
});
afterAll(() => server.close());

describe("UsagePageContainer users", () => {
    test.each([
        { app: "the desktop app", desktop: true, tabs: ["Agents", "Workflows", "Models"], opensOn: "Agents", description: /by agent, workflow, and model\./ },
        { app: "a deployment", desktop: false, tabs: ["Agents", "Workflows", "Users", "Models"], opensOn: "Users", description: /by agent, workflow, user, and model\./ },
    ])("in $app, a link to Users opens on $opensOn", async ({ desktop, tabs, opensOn, description }) => {
        if (desktop) enableWailsRuntime();
        renderWithProviders(<UsagePageContainer />, { initialPath: "/usage?type=users" });

        const tablist = within(await screen.findByRole("tablist", { name: "Usage type" }));
        expect(tablist.getAllByRole("tab").map(tab => tab.textContent)).toEqual(tabs);
        expect(tablist.getByRole("tab", { name: opensOn })).toHaveAttribute("aria-selected", "true");
        expect(screen.getByText(description)).toBeInTheDocument();
    });
});
