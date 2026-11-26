// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test } from "vitest";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { FULL_RBAC_SEED, makeManyClaimMappings, SEED_ROLES, seedRbacForStory } from "@/testing/data/rbac";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { ClaimMappingPage } from "./ClaimMappingPage";

const server = setupServer(...rbacHandlers);
const requestedUrls: URL[] = [];

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    server.events.on("request:start", ({ request }) => requestedUrls.push(new URL(request.url)));
});
afterEach(() => {
    server.resetHandlers();
    requestedUrls.length = 0;
});
afterAll(() => server.close());

const config = { claimKey: "groups" } as ConfigContextValue;

function renderPage() {
    return render(
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={new QueryClient()}>
                <ConfigContext.Provider value={config}>
                    <MemoryRouter>
                        <ClaimMappingPage />
                    </MemoryRouter>
                </ConfigContext.Provider>
            </QueryClientProvider>
        </OpenFeatureTestProvider>
    );
}

function lastClaimMappingQuery(): URLSearchParams {
    const url = requestedUrls.filter(u => u.pathname.endsWith("/rbac/claimMappings")).at(-1);
    if (!url) throw new Error("no claim-mappings request was made");
    return url.searchParams;
}

async function openRowMenu(claimValue: string) {
    const row = (await screen.findByText(claimValue)).closest("tr")!;
    await userEvent.click(within(row).getByRole("button", { name: "More" }));
}

describe("ClaimMappingPage", () => {
    describe("with 25 mappings across two pages", () => {
        beforeEach(() => seedRbacForStory({ roles: SEED_ROLES, claimMappings: makeManyClaimMappings(25) }));

        test("renders only the first page and pages to the rest", async () => {
            renderPage();
            await screen.findByText("Mapping 001");

            expect(screen.getAllByRole("row")).toHaveLength(21);
            expect(screen.queryByText("Mapping 024")).not.toBeInTheDocument();
            expect(screen.getByText("Showing 1–20 of 25")).toBeInTheDocument();

            await userEvent.click(screen.getByLabelText("Go to next page"));

            await screen.findByText("Mapping 024");
            expect(screen.queryByText("Mapping 001")).not.toBeInTheDocument();
            expect(lastClaimMappingQuery().get("pageNumber")).toBe("2");
        });

        test("the name-less yaml mapping sorts onto the first page, and the sort header flips direction", async () => {
            renderPage();
            await screen.findByText("Mapping 001");
            expect(screen.getByText("group-025")).toBeInTheDocument();
            expect(lastClaimMappingQuery().get("sort")).toBe("name:asc");

            await userEvent.click(screen.getByRole("button", { name: "Name" }));

            await waitFor(() => expect(lastClaimMappingQuery().get("sort")).toBe("name:desc"));
            expect(await screen.findByText("Mapping 024")).toBeInTheDocument();
        });

        test("a system mapping is typed System and offers no write action, in the row menu or the View dialog", async () => {
            renderPage();
            const row = (await screen.findByText("group-025")).closest("tr")!;
            expect(within(row).getByText("System")).toBeInTheDocument();

            await openRowMenu("group-025");

            expect(await screen.findByRole("menuitem", { name: "View" })).toBeInTheDocument();
            expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
            expect(screen.queryByRole("menuitem", { name: "Delete" })).not.toBeInTheDocument();

            await userEvent.click(screen.getByRole("menuitem", { name: "View" }));

            const dialog = within(await screen.findByRole("dialog"));
            await dialog.findByText("Claim Value");
            expect(dialog.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
        });

        test("a custom mapping is typed Custom and keeps Edit and Delete", async () => {
            renderPage();
            const row = (await screen.findByText("Mapping 001")).closest("tr")!;
            expect(within(row).getByText("Custom")).toBeInTheDocument();

            await openRowMenu("group-001");

            expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
            expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
        });

        test("searching resets to the first page and queries the server", async () => {
            renderPage();
            await screen.findByText("Mapping 001");
            await userEvent.click(screen.getByLabelText("Go to next page"));
            await screen.findByText("Mapping 024");

            await userEvent.type(screen.getByPlaceholderText("Filter by name or claim value"), "Mapping 007");

            await waitFor(() => expect(lastClaimMappingQuery().get("search")).toBe("Mapping 007"));
            expect(lastClaimMappingQuery().get("pageNumber")).toBe("1");
            await screen.findByText("Mapping 007");
        });

        test("searching a claim value finds the name-less yaml mapping", async () => {
            renderPage();
            await screen.findByText("Mapping 001");

            await userEvent.type(screen.getByPlaceholderText("Filter by name or claim value"), "group-025");

            await waitFor(() => expect(lastClaimMappingQuery().get("search")).toBe("group-025"));
            expect(await screen.findByText("group-025")).toBeInTheDocument();
            expect(screen.queryByText("Mapping 001")).not.toBeInTheDocument();
        });
    });

    describe("with the standard seed", () => {
        beforeEach(() => seedRbacForStory(FULL_RBAC_SEED));

        test("the role filter narrows server-side", async () => {
            renderPage();
            await screen.findByText("Engineering");

            await userEvent.click(screen.getByRole("combobox", { name: "Filter by role" }));
            await userEvent.click(await screen.findByRole("option", { name: "Administrator" }));

            await screen.findByText("Platform Admins");
            expect(screen.queryByText("Engineering")).not.toBeInTheDocument();
            expect(lastClaimMappingQuery().get("roleId")).toBe("role-administrator");
        });

        test("role chips resolve names from the full role set", async () => {
            renderPage();
            const row = (await screen.findByText("Engineering")).closest("tr")!;
            expect(within(row).getByText("Editor")).toBeInTheDocument();
        });
    });
});
