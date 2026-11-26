// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { RBACRole } from "@/types";

import { RoleViewDialog } from "./RoleViewDialog";

vi.mock("@/api/agents", () => ({ useAgents: () => ({ data: [] }) }));
vi.mock("@/api/workflows", () => ({ useWorkflowsQuery: () => ({ data: [] }) }));
vi.mock("./scopeBundles/ScopeBundleEditor", () => ({ ScopeBundleEditor: () => null }));

function makeRole(origin: RBACRole["origin"]): RBACRole {
    return { id: "r", name: "Role", description: "", origin, scopes: [], createdBy: "", updatedBy: "", createdAt: "", updatedAt: "" };
}

function renderDialog(role: RBACRole) {
    const client = new QueryClient();
    return render(
        <QueryClientProvider client={client}>
            <RoleViewDialog role={role} onOpenChange={vi.fn()} onEdit={vi.fn()} />
        </QueryClientProvider>
    );
}

describe("RoleViewDialog", () => {
    test("shows the Edit action for a custom role", () => {
        renderDialog(makeRole("custom"));
        expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    });

    test("hides the Edit action for a builtin role", () => {
        renderDialog(makeRole("builtin"));
        expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    });

    test("hides the Edit action for a yaml role", () => {
        renderDialog(makeRole("yaml"));
        expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    });
});
