// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import type { RBACRole, RoleOrigin, UserRoleAssignment, UserRoleSource } from "@/types";

import { UserRoleChips } from "./UserRoleChips";

const makeRole = (id: string, name: string, origin: RoleOrigin = "custom"): RBACRole => ({
    id,
    name,
    description: "",
    origin,
    scopes: [],
    createdBy: "",
    updatedBy: "",
    createdAt: "",
    updatedAt: "",
});

const direct = (roleId: string): UserRoleAssignment => ({ roleId, source: "direct" });
const inherited = (roleId: string, source: Exclude<UserRoleSource, "direct">): UserRoleAssignment => ({ roleId, source });

const roleById = new Map<string, RBACRole>([
    ["r1", makeRole("r1", "Administrator", "yaml")],
    ["r2", makeRole("r2", "Event Portal Manager")],
    ["r3", makeRole("r3", "Mission Control Viewer")],
    ["r4", makeRole("r4", "Evaluations Analyst")],
]);

describe("UserRoleChips", () => {
    test("renders 'None' when the user has no roles", () => {
        render(<UserRoleChips roles={[]} roleById={roleById} />);
        expect(screen.getByText("None")).toBeInTheDocument();
    });

    test("shows every role as a chip when at or under the max", () => {
        render(<UserRoleChips roles={[direct("r1"), direct("r2")]} roleById={roleById} max={2} />);
        expect(screen.getByText("Administrator")).toBeInTheDocument();
        expect(screen.getByText("Event Portal Manager")).toBeInTheDocument();
        expect(screen.queryByText(/^\+/)).not.toBeInTheDocument();
    });

    test("collapses the overflow into a +N indicator carrying the remaining names", () => {
        render(<UserRoleChips roles={[direct("r1"), direct("r2"), direct("r3"), direct("r4")]} roleById={roleById} max={2} />);
        expect(screen.getByText("Administrator")).toBeInTheDocument();
        expect(screen.getByText("Event Portal Manager")).toBeInTheDocument();
        expect(screen.queryByText("Mission Control Viewer")).not.toBeInTheDocument();

        const overflow = screen.getByText("+ 2");
        expect(overflow).toBeInTheDocument();
        expect(overflow).toHaveAttribute("title", "Mission Control Viewer, Evaluations Analyst");
    });

    test("falls back to the raw id when a role name is unknown", () => {
        render(<UserRoleChips roles={[direct("unknown-role")]} roleById={roleById} />);
        expect(screen.getByText("unknown-role")).toBeInTheDocument();
    });

    test("names a UUID no role has 'Unknown role' with the id's prefix, and the full id in its tooltip", async () => {
        const id = "3f2a6c1e-8b7d-5e4f-9a0b-1c2d3e4f5a6b";
        render(<UserRoleChips roles={[inherited(id, "yaml")]} roleById={roleById} />);

        const chip = screen.getByText("Unknown role 3f2a6c1e");
        expect(screen.queryByText(id)).not.toBeInTheDocument();
        await userEvent.hover(chip);
        expect((await screen.findAllByText(`Granted through Agent Mesh configuration. No role has the id ${id}`)).length).toBeGreaterThan(0);
    });

    test("while the roles load, shows a placeholder rather than naming any role", () => {
        render(<UserRoleChips roles={[direct("r1")]} roleById="pending" />);
        expect(screen.getByRole("status", { name: "Loading roles" })).toBeInTheDocument();
        expect(screen.queryByText("r1")).not.toBeInTheDocument();
    });

    test("when the roles failed to load, a UUID reads as its bare id rather than 'Unknown role'", () => {
        const id = "3f2a6c1e-8b7d-5e4f-9a0b-1c2d3e4f5a6b";
        render(<UserRoleChips roles={[direct(id)]} roleById={undefined} />);
        expect(screen.getByText(id)).toBeInTheDocument();
        expect(screen.queryByText(/Unknown role/)).not.toBeInTheDocument();
    });

    test("two unknown roles read apart", () => {
        render(<UserRoleChips roles={[direct("3f2a6c1e-8b7d-5e4f-9a0b-1c2d3e4f5a6b"), direct("9c0d1e2f-3a4b-5c6d-8e7f-0a1b2c3d4e5f")]} roleById={roleById} />);
        expect(screen.getByText("Unknown role 3f2a6c1e")).toBeInTheDocument();
        expect(screen.getByText("Unknown role 9c0d1e2f")).toBeInTheDocument();
    });

    test("dedupes a role held via multiple sources into a single chip (preferring the direct, icon-less entry)", () => {
        render(<UserRoleChips roles={[direct("r2"), inherited("r2", "claim")]} roleById={roleById} max={2} />);
        expect(screen.getAllByText("Event Portal Manager")).toHaveLength(1);
        expect(screen.getByText("Event Portal Manager").closest("[data-slot=badge]")!.querySelector("svg")).toBeNull();
    });

    test("prefers the direct entry regardless of order (inherited seen first)", () => {
        render(<UserRoleChips roles={[inherited("r2", "claim"), direct("r2")]} roleById={roleById} max={2} />);
        expect(screen.getAllByText("Event Portal Manager")).toHaveLength(1);
        expect(screen.getByText("Event Portal Manager").closest("[data-slot=badge]")!.querySelector("svg")).toBeNull();
    });

    test("marks inherited roles with a source icon; direct roles carry none", () => {
        render(<UserRoleChips roles={[direct("r2"), inherited("r1", "yaml")]} roleById={roleById} max={2} />);
        expect(screen.getByText("Event Portal Manager").closest("[data-slot=badge]")!.querySelector("svg")).toBeNull();
        expect(screen.getByText("Administrator").closest("[data-slot=badge]")!.querySelector("svg")).not.toBeNull();
    });
});
