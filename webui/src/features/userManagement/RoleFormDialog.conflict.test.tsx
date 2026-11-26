// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { RoleFormDialog } from "./RoleFormDialog";

vi.mock("@/api/agents", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/agents")>()),
    useAgents: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/api/workflows", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/workflows")>()),
    useWorkflowsQuery: () => ({ data: [], isLoading: false }),
}));

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
});
afterAll(() => server.close());

test.each(["A role with this name already exists", 'A system role named "Operators" already exists; pick a different name'])("a 409 (%s) shows on the Name field", async sentence => {
    server.use(http.post("*/api/v2/platform/rbac/roles", () => HttpResponse.json({ message: sentence, errorId: "e1" }, { status: 409 })));
    const user = userEvent.setup();
    renderWithProviders(<RoleFormDialog open onOpenChange={() => {}} />);

    const name = screen.getByRole("textbox", { name: /^Name/ });
    await user.type(name, "Operators");
    await user.click(screen.getByRole("button", { name: "Create Role" }));

    expect(await within(name.parentElement!).findByText(sentence)).toBeInTheDocument();
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(screen.getAllByText(sentence)).toHaveLength(1);
});
