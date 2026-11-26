// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { RBACRole } from "@/types";

import { RoleMultiSelect } from "./RoleMultiSelect";

const role = (id: string, name: string): RBACRole => ({ id, name, description: "", origin: "custom", scopes: [], createdBy: "", updatedBy: "", createdAt: "", updatedAt: "" });
const ROLES = [role("r-editor", "Editor"), role("r-viewer", "Viewer")];

describe("RoleMultiSelect (id-keyed picker over a name-keyed MultiSelect)", () => {
    test("renders a chip per resolvable id and silently drops an unresolvable one", () => {
        render(<RoleMultiSelect roles={ROLES} value={["r-editor", "r-deleted"]} onChange={vi.fn()} />);
        expect(screen.getByText("Editor")).toBeInTheDocument();
        expect(screen.queryByText("r-deleted")).not.toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: /^Remove / })).toHaveLength(1);
    });

    test("selecting an option emits the role id, not its name", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<RoleMultiSelect roles={ROLES} value={[]} onChange={onChange} />);
        await user.click(screen.getByRole("button", { expanded: false }));
        await user.click(await screen.findByRole("button", { name: "Viewer" }));
        expect(onChange).toHaveBeenCalledWith(["r-viewer"]);
    });
});
