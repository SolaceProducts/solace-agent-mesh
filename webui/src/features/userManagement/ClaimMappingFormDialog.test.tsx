// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ClaimMapping } from "@/types";

import { ClaimMappingFormDialog } from "./ClaimMappingFormDialog";

const updateMutateAsync = vi.fn().mockResolvedValue(undefined);
const createMutateAsync = vi.fn().mockResolvedValue(undefined);
let configValue = { claimKey: "groups" };

vi.mock("@/api/rbac", () => ({
    useAssignableRoles: () => ({ data: [{ id: "role-editor", name: "Editor" }], isSuccess: true }),
    useCreateClaimMappingMutation: () => ({ mutateAsync: createMutateAsync, isPending: false, reset: vi.fn() }),
    useUpdateClaimMappingMutation: () => ({ mutateAsync: updateMutateAsync, isPending: false, reset: vi.fn() }),
}));

vi.mock("@/providers/contexts", () => ({ useConfigContext: () => configValue }));

vi.mock("react-router-dom", () => ({ useNavigate: () => vi.fn() }));

function makeMapping(partial: Partial<ClaimMapping>): ClaimMapping {
    return {
        id: "claim-1",
        name: "Engineering",
        oidcProvider: "okta",
        claimKey: "roles",
        claimValue: "eng",
        roleIds: ["role-editor"],
        origin: "custom",
        createdBy: "",
        updatedBy: "",
        createdAt: "",
        updatedAt: "",
        ...partial,
    };
}

describe("ClaimMappingFormDialog", () => {
    beforeEach(() => {
        updateMutateAsync.mockClear();
        createMutateAsync.mockClear();
        configValue = { claimKey: "groups" };
    });

    test("create omits oidcProvider so the server applies the deployment provider", async () => {
        const user = userEvent.setup();
        render(<ClaimMappingFormDialog open onOpenChange={vi.fn()} />);

        await user.type(screen.getByLabelText(/^Name/), "Engineering");
        await user.type(screen.getByLabelText(/^Claim Value/), "eng-group");
        await user.click(screen.getByRole("button", { expanded: false }));
        await user.click(await screen.findByRole("button", { name: "Editor" }));
        await user.click(screen.getByRole("button", { name: "Create Claim Mapping" }));

        await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(1));
        const payload = createMutateAsync.mock.calls[0][0];
        expect(payload).not.toHaveProperty("oidcProvider");
        expect(payload.claimKey).toBe("groups");
    });

    test("edit preserves the per-mapping oidcProvider but sends the deployment-global claimKey", async () => {
        const mapping = makeMapping({ oidcProvider: "okta", claimKey: "roles" });
        render(<ClaimMappingFormDialog open onOpenChange={vi.fn()} mapping={mapping} />);

        fireEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1));
        const { changes } = updateMutateAsync.mock.calls[0][0];
        expect(changes.oidcProvider).toBe("okta");
        expect(changes.claimKey).toBe("groups");
    });

    test("edit resends an empty stored oidcProvider rather than dropping it", async () => {
        const mapping = makeMapping({ oidcProvider: "" });
        render(<ClaimMappingFormDialog open onOpenChange={vi.fn()} mapping={mapping} />);

        fireEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1));
        const { changes } = updateMutateAsync.mock.calls[0][0];
        expect(changes).toHaveProperty("oidcProvider");
        expect(changes.oidcProvider).toBe("");
    });
});
