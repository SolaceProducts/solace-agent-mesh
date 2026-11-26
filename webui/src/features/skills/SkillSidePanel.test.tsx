// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Skill } from "@/types";

import { SKILL_TOOLS_ANCHOR, SkillSidePanel } from "./SkillSidePanel";

vi.mock("@/hooks", async () => {
    const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
    return {
        ...actual,
        useDescriptionTruncation: () => ({
            isExpanded: false,
            isTruncated: true,
            descriptionRef: { current: null },
            toggleExpanded: vi.fn(),
        }),
    };
});

const skill: Skill = {
    id: "11111111-1111-1111-1111-000000000001",
    name: "test-skill",
    description: "A test skill with multiple bound agents",
    tags: null,
    origin: "user",
    discoveryStatus: "ready",
    hasReferences: false,
    hasAssets: false,
    hasTools: false,
    resourceCount: 0,
    referenceCount: 0,
    assetCount: 0,
    agents: [{ id: "agent-1", name: "Agent One", description: "A long agent description that would normally trigger truncation in production layouts." }],
    createdBy: "alice",
    updatedBy: "alice",
    createdAt: "2026-01-01T00:00:00.000+00:00",
    updatedAt: "2026-01-01T00:00:00.000+00:00",
};

describe("SkillSidePanel AgentCard", () => {
    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
    });
    afterEach(() => {
        vi.clearAllMocks();
    });

    it("renders the expand button on a truncated agent description and the click handler fires", async () => {
        const { findByRole } = renderWithProviders(<SkillSidePanel skill={skill} onClose={vi.fn()} />);

        const expandBtn = await findByRole("button", { name: /Expand description|Collapse description/i });
        expect(expandBtn).toBeInTheDocument();

        fireEvent.click(expandBtn);
    });
});

const skillWithToolsAndResources: Skill = {
    ...skill,
    hasTools: true,
    hasReferences: true,
    hasAssets: true,
    resourceCount: 3,
    referenceCount: 2,
    assetCount: 1,
    bundledTools: [{ tool_name: "render_chart" }, { tool_name: "summarize" }],
};

describe("SkillSidePanel Tools & Resources", () => {
    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
    });
    afterEach(() => {
        vi.clearAllMocks();
    });

    it("shows tool/reference/asset counts and the bundled tool names", () => {
        const { getByTestId, getByText } = renderWithProviders(<SkillSidePanel skill={skillWithToolsAndResources} onClose={vi.fn()} onOpen={vi.fn()} />);

        const section = getByTestId("skillToolsResources");
        expect(section).toHaveTextContent("tools");
        expect(section).toHaveTextContent("references");
        expect(section).toHaveTextContent("asset");
        expect(getByText("render_chart")).toBeInTheDocument();
        expect(getByText("summarize")).toBeInTheDocument();
    });

    it("deep-links to the full page tools section when clicked (custom skill)", () => {
        const onOpen = vi.fn();
        const { getByTestId } = renderWithProviders(<SkillSidePanel skill={skillWithToolsAndResources} onClose={vi.fn()} onOpen={onOpen} />);

        fireEvent.click(getByTestId("skillToolsResources"));
        expect(onOpen).toHaveBeenCalledWith(SKILL_TOOLS_ANCHOR);
    });

    it("renders the section as a non-clickable block when onOpen is absent (built-in)", () => {
        const { getByTestId } = renderWithProviders(<SkillSidePanel skill={skillWithToolsAndResources} onClose={vi.fn()} />);

        expect(getByTestId("skillToolsResources").tagName).not.toBe("BUTTON");
    });

    it("omits the section entirely when the skill has no tools or resources", () => {
        const { queryByTestId } = renderWithProviders(<SkillSidePanel skill={skill} onClose={vi.fn()} onOpen={vi.fn()} />);

        expect(queryByTestId("skillToolsResources")).not.toBeInTheDocument();
    });
});
