// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { rbacApi } from "@/api/rbac/versioned";

import type { DefaultRolesResponse, RBACRole } from "@/types";

import { RolesTable } from "./RolesTable";

function makeRole(partial: Partial<RBACRole>): RBACRole {
    return {
        id: "r",
        name: "Role",
        description: "",
        origin: "custom",
        scopes: [],
        createdBy: "",
        updatedBy: "",
        createdAt: "",
        updatedAt: "",
        ...partial,
    };
}

const roles: RBACRole[] = [
    makeRole({ id: "b", name: "Administrator", origin: "yaml", scopes: ["*:*:*"] }),
    makeRole({ id: "d", name: "Member", origin: "custom", scopes: ["agent:*:invoke"], createdBy: "amy@example.com" }),
    makeRole({ id: "c", name: "Agent Editor", origin: "custom", scopes: ["agent_builder:*:read"], createdBy: "eve@example.com" }),
    makeRole({ id: "e", name: "SAM Manager", origin: "builtin", scopes: ["*:*:*"], createdBy: "system" }),
];

const savedDefaults = (...roleIds: string[]): DefaultRolesResponse => ({ roleIds, origin: "custom" });
const MEMBER_IS_DEFAULT = savedDefaults("d");

afterEach(() => vi.restoreAllMocks());

function renderTable(defaults: DefaultRolesResponse | undefined) {
    const client = new QueryClient();
    return render(
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={client}>
                <RolesTable roles={roles} defaults={defaults} onView={vi.fn()} onEdit={vi.fn()} sortKey="name" sortDir="asc" onSort={vi.fn()} />
            </QueryClientProvider>
        </OpenFeatureTestProvider>
    );
}

function openRowMenu(name: string) {
    const row = screen.getByText(name).closest("tr");
    if (!row) throw new Error(`Row for "${name}" not found`);
    fireEvent.click(within(row).getByRole("button", { name: "More" }));
}

describe("RolesTable", () => {
    test("renders the creator only for custom roles", () => {
        renderTable(MEMBER_IS_DEFAULT);
        expect(screen.getByText("eve@example.com")).toBeInTheDocument();
    });

    test("badges each origin: System for yaml, Built-in for builtin, Custom for custom", () => {
        renderTable(MEMBER_IS_DEFAULT);
        expect(screen.getByText("System")).toBeInTheDocument();
        expect(screen.getByText("Built-in")).toBeInTheDocument();
        expect(screen.getAllByText("Custom")).toHaveLength(2);
    });

    test("shows a Default badge for each role in the default set", () => {
        renderTable(savedDefaults("d", "c"));
        expect(screen.getAllByText("Default")).toHaveLength(2);
    });

    test("yaml roles offer only View, never a default toggle / Edit / Delete", () => {
        renderTable(MEMBER_IS_DEFAULT);
        openRowMenu("Administrator");
        expect(screen.getByText("View")).toBeInTheDocument();
        expect(screen.queryByText(/default roles/)).not.toBeInTheDocument();
        expect(screen.queryByText("Edit")).not.toBeInTheDocument();
        expect(screen.queryByText("Delete")).not.toBeInTheDocument();
    });

    test("builtin roles offer only View, never a default toggle / Edit / Delete", () => {
        renderTable(MEMBER_IS_DEFAULT);
        openRowMenu("SAM Manager");
        expect(screen.getByText("View")).toBeInTheDocument();
        expect(screen.queryByText(/default roles/)).not.toBeInTheDocument();
        expect(screen.queryByText("Edit")).not.toBeInTheDocument();
        expect(screen.queryByText("Delete")).not.toBeInTheDocument();
    });

    test("a non-default custom role offers Add to default roles; a default role offers Remove", () => {
        renderTable(MEMBER_IS_DEFAULT);
        openRowMenu("Agent Editor");
        expect(screen.getByText("Add to Default Roles")).toBeInTheDocument();

        openRowMenu("Member");
        expect(screen.getByText("Remove from Default Roles")).toBeInTheDocument();
    });

    test("deleting a default role shows an error dialog, not the confirmation", () => {
        renderTable(MEMBER_IS_DEFAULT);
        openRowMenu("Member");
        fireEvent.click(screen.getByText("Delete").closest('[role="menuitem"]')!);
        expect(screen.getByText(/it is a default role/)).toBeInTheDocument();
        expect(screen.queryByText(/will be permanently deleted/)).not.toBeInTheDocument();
    });

    test("deleting a non-default role opens the confirmation dialog", () => {
        renderTable(MEMBER_IS_DEFAULT);
        openRowMenu("Agent Editor");
        fireEvent.click(screen.getByText("Delete").closest('[role="menuitem"]')!);
        expect(screen.getByText(/will be permanently deleted/)).toBeInTheDocument();
    });
});

test.each([
    ["Member", "Remove from Default Roles"],
    ["Agent Editor", "Add to Default Roles"],
])("while the configuration sets the defaults, %s's %s explains the override instead of saving", (name, action) => {
    const setDefaultRoles = vi.spyOn(rbacApi, "setDefaultRoles");
    renderTable({ roleIds: ["d"], origin: "yaml" });
    openRowMenu(name);
    fireEvent.click(screen.getByText(action).closest('[role="menuitem"]')!);

    expect(screen.getByText(/come from the Agent Mesh configuration/)).toBeInTheDocument();
    expect(setDefaultRoles).not.toHaveBeenCalled();
});

test.each([
    ["Agent Editor", "Add to Default Roles", ["d", "c"]],
    ["Member", "Remove from Default Roles", []],
])("with a saved set, %s's %s saves the whole set with that one change", async (name, action, expected) => {
    const setDefaultRoles = vi.spyOn(rbacApi, "setDefaultRoles").mockResolvedValue(savedDefaults(...expected));
    renderTable(MEMBER_IS_DEFAULT);
    openRowMenu(name);
    fireEvent.click(screen.getByText(action).closest('[role="menuitem"]')!);

    await waitFor(() => expect(setDefaultRoles).toHaveBeenCalledWith(expected, expect.anything()));
    expect(screen.queryByText(/come from the Agent Mesh configuration/)).not.toBeInTheDocument();
});

test("when the configuration names no defaults, Add saves the role as the only default", async () => {
    const setDefaultRoles = vi.spyOn(rbacApi, "setDefaultRoles").mockResolvedValue(savedDefaults("c"));
    renderTable({ roleIds: [], origin: "yaml" });
    openRowMenu("Agent Editor");
    fireEvent.click(screen.getByText("Add to Default Roles").closest('[role="menuitem"]')!);

    await waitFor(() => expect(setDefaultRoles).toHaveBeenCalledWith(["c"], expect.anything()));
    expect(screen.queryByText(/come from the Agent Mesh configuration/)).not.toBeInTheDocument();
});

test("until the default roles load, the toggle is disabled, since saving would replace the unseen set", () => {
    const setDefaultRoles = vi.spyOn(rbacApi, "setDefaultRoles");
    renderTable(undefined);
    openRowMenu("Agent Editor");
    const toggle = screen.getByText("Add to Default Roles").closest('[role="menuitem"]')!;
    expect(toggle).toHaveAttribute("aria-disabled", "true");

    fireEvent.click(toggle);
    expect(setDefaultRoles).not.toHaveBeenCalled();
});
