// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { CapabilityInfo } from "@/api/capabilities";
import { useCapabilities } from "@/api/capabilities";

import meta, { OpenDialog } from "./GatewaysAvailableDialog.stories";

vi.mock("@/api/capabilities", async importActual => ({
    ...(await importActual<typeof import("@/api/capabilities")>()),
    useCapabilities: vi.fn(),
}));

const Dialog = composeStory(OpenDialog, meta);
const mockUseCapabilities = vi.mocked(useCapabilities);

function withCapabilities(caps: CapabilityInfo[]) {
    mockUseCapabilities.mockReturnValue({ data: caps } as unknown as ReturnType<typeof useCapabilities>);
}

async function openDialog() {
    render(<Dialog />);
    await userEvent.click(await screen.findByText("Create Entrypoint"));
    return within(await screen.findByRole("dialog"));
}

describe("GatewaysAvailableDialog capability badge", () => {
    beforeEach(() => vi.clearAllMocks());

    test("renders a BETA badge on the Event Mesh card when the capability resolves to beta", async () => {
        withCapabilities([{ key: "event_mesh_gateway", name: "Event Mesh Gateway", description: "", status: "beta", platform: "windows", hasEnvOverride: false }]);
        const dialog = await openDialog();
        await dialog.findByText("Event Mesh");
        expect(dialog.getByText("BETA")).toBeInTheDocument();
    });

    test("renders no maturity badge when the capability resolves to stable (GA)", async () => {
        withCapabilities([{ key: "event_mesh_gateway", name: "Event Mesh Gateway", description: "", status: "stable", platform: "linux", hasEnvOverride: false }]);
        const dialog = await openDialog();
        await dialog.findByText("Event Mesh");
        expect(dialog.queryByText("BETA")).not.toBeInTheDocument();
        expect(dialog.queryByText("EXPERIMENTAL")).not.toBeInTheDocument();
    });
});
