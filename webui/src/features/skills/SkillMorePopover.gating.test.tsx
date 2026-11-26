// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockCustomSkills } from "@/testing/data/skills";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { SkillMorePopover } from "./SkillMorePopover";

expect.extend(matchers);

const renderWithCapabilities = (granted: string[]) => {
    const held = new Set(granted);
    renderWithProviders(
        <SkillMorePopover skill={mockCustomSkills[0]}>
            <button data-testid="moreTrigger">More</button>
        </SkillMorePopover>,
        { userContextValues: { userHasCapability: (cap: string) => held.has(cap) } }
    );
};

describe("SkillMorePopover zero-item gating", () => {
    test("renders nothing when the user can neither update nor delete", () => {
        renderWithCapabilities([CAPABILITIES.readSkills]);
        expect(screen.queryByTestId("moreTrigger")).not.toBeInTheDocument();
    });

    test("renders the trigger when the user can update", () => {
        renderWithCapabilities([CAPABILITIES.updateSkills]);
        expect(screen.getByTestId("moreTrigger")).toBeInTheDocument();
    });

    test("renders the trigger when the user can delete", () => {
        renderWithCapabilities([CAPABILITIES.deleteSkills]);
        expect(screen.getByTestId("moreTrigger")).toBeInTheDocument();
    });
});

describe("SkillMorePopover per-verb menu items", () => {
    const openMenu = async () => {
        await userEvent.click(screen.getByTestId("moreTrigger"));
        return screen.findByRole("dialog");
    };

    test("update alone exposes Re-upload without Delete", async () => {
        renderWithCapabilities([CAPABILITIES.updateSkills]);
        const menu = await openMenu();

        expect(await screen.findByText("Re-upload")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Delete");
    });

    test("delete alone exposes Delete without Re-upload", async () => {
        renderWithCapabilities([CAPABILITIES.deleteSkills]);
        const menu = await openMenu();

        expect(await screen.findByText("Delete")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Re-upload");
    });
});
