// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { skillToV2 } from "@/testing/data/platformWireV2";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Skill } from "@/types";

import { SkillsView } from "./SkillsView";

const makeSkill = (over: Partial<Skill>): Skill => ({
    id: "00000000-0000-0000-0000-000000000001",
    name: "skill",
    description: "",
    tags: null,
    origin: "user",
    discoveryStatus: "ready",
    hasReferences: false,
    hasAssets: false,
    hasTools: false,
    resourceCount: 0,
    referenceCount: 0,
    assetCount: 0,
    createdBy: "alice",
    updatedBy: "alice",
    createdAt: "2026-01-01T00:00:00.000+00:00",
    updatedAt: "2026-01-01T00:00:00.000+00:00",
    ...over,
});

const customSkill = makeSkill({ id: "11111111-1111-1111-1111-111111111111", name: "my-custom-skill", description: "a custom one" });
const builtinKnowledge = makeSkill({ id: "sam-knowledge", name: "sam-knowledge", origin: "builtin", createdBy: "" });
const builtinDocs = makeSkill({ id: "sam-docs", name: "sam-docs", origin: "builtin", createdBy: "" });

const envelope = (data: Skill[]) => {
    const body = JSON.stringify({ data: data.map(skillToV2), meta: { pagination: { pageNumber: 1, count: data.length, pageSize: data.length, nextPage: null, totalPages: 1 } } });
    return {
        ok: true,
        status: 200,
        text: () => Promise.resolve(body),
        json: () => Promise.resolve(JSON.parse(body)),
        headers: new Headers({ "content-type": "application/json" }),
    };
};

const mockFetch = vi.fn().mockImplementation((url: string) => {
    const u = String(url);
    if (u.includes("origin=builtin")) return Promise.resolve(envelope([builtinKnowledge, builtinDocs]));
    if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([customSkill]));
    return Promise.resolve(envelope([]));
});

describe("SkillsView built-in skills", () => {
    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });

    beforeEach(() => {
        vi.stubGlobal("fetch", mockFetch);
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("merges built-in skills into the list with a Built-in label and no actions popover", async () => {
        const { findByText, getByText, getByRole, getByTestId, queryByTestId, getAllByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        expect(await findByText("sam-knowledge")).toBeInTheDocument();
        expect(getByText("sam-docs")).toBeInTheDocument();
        expect(getByText("my-custom-skill")).toBeInTheDocument();

        const typeCells = getAllByTestId(/^skillTypeCell-/);
        const builtinCells = typeCells.filter(cell => cell.textContent === "Built-in");
        const customCells = typeCells.filter(cell => cell.textContent === "Custom");
        expect(builtinCells).toHaveLength(2);
        expect(customCells).toHaveLength(1);

        expect(getAllByTestId("skillMorePopoverTrigger")).toHaveLength(1);

        fireEvent.click(getByRole("button", { name: "sam-knowledge" }));
        expect(getByTestId("skillSidePanelCloseButton")).toBeInTheDocument();
        expect(queryByTestId("openSkillButton")).not.toBeInTheDocument();

        fireEvent.click(getByRole("button", { name: "my-custom-skill" }));
    });

    it("filters to built-ins only under ?type=builtin and clears back to all", async () => {
        const { findByText, queryByText, getByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills?type=builtin" });

        expect(await findByText("sam-knowledge")).toBeInTheDocument();
        expect(queryByText("my-custom-skill")).not.toBeInTheDocument();

        fireEvent.click(getByTestId("skillsClearFilters"));
        expect(await findByText("my-custom-skill")).toBeInTheDocument();
    });

    it("filters to custom only under ?type=custom", async () => {
        const { findByText, queryByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills?type=custom" });

        expect(await findByText("my-custom-skill")).toBeInTheDocument();
        expect(queryByText("sam-knowledge")).not.toBeInTheDocument();
    });

    it("filters via the type-filter dropdown", async () => {
        const user = userEvent.setup();
        const { findByText, getByTestId, queryByText, findByRole } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        expect(await findByText("my-custom-skill")).toBeInTheDocument();

        await user.click(getByTestId("skillsTypeFilter"));
        await user.click(await findByRole("option", { name: "Custom" }));

        expect(await findByText("my-custom-skill")).toBeInTheDocument();
        expect(queryByText("sam-knowledge")).not.toBeInTheDocument();
    });

    it("hides the Open Skill button in the side panel for a built-in", async () => {
        const { findByTestId, queryByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        fireEvent.click(await findByTestId("skillRow-sam-knowledge"));
        expect(queryByTestId("openSkillButton")).not.toBeInTheDocument();
    });

    it("shows and triggers Open Skill in the side panel for a custom skill", async () => {
        const { findByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        fireEvent.click(await findByTestId("skillRow-11111111-1111-1111-1111-111111111111"));
        const openButton = await findByTestId("openSkillButton");
        fireEvent.click(openButton);
    });
});

describe("SkillsView agent usage column", () => {
    const aaaSkill = makeSkill({
        id: "22222222-2222-2222-2222-000000000002",
        name: "aaa-skill",
        createdBy: "zoe",
        agents: [
            { id: "ag-1", name: "Bot One" },
            { id: "ag-2", name: "Bot Two" },
        ],
    });
    const zzzSkill = makeSkill({ id: "33333333-3333-3333-3333-000000000003", name: "zzz-skill", createdBy: "alice", agents: [] });

    const fetchWithAgents = vi.fn().mockImplementation((url: string) => {
        const u = String(url);
        if (u.includes("origin=builtin")) return Promise.resolve(envelope([]));
        if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([aaaSkill, zzzSkill]));
        return Promise.resolve(envelope([]));
    });

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });
    beforeEach(() => {
        vi.stubGlobal("fetch", fetchWithAgents);
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("shows the number of agents using each skill", async () => {
        const { findByTestId, getByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        expect(await findByTestId("agentCountCell-22222222-2222-2222-2222-000000000002")).toHaveTextContent("2");
        expect(getByTestId("agentCountCell-33333333-3333-3333-3333-000000000003")).toHaveTextContent("0");
    });

    it("sorts numerically when the Agent Usage header is clicked", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("aaa-skill");

        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["2", "0"]);

        fireEvent.click(getByText("Agent Usage"));
        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["0", "2"]);

        fireEvent.click(getByText("Agent Usage"));
        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["2", "0"]);
    });

    it("sorts alphabetically when the Name header is clicked", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("aaa-skill");

        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["2", "0"]);
        fireEvent.click(getByText("Name"));
        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["0", "2"]);
    });

    it("sorts by Created By when that header is clicked", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("aaa-skill");

        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["2", "0"]);

        fireEvent.click(getByText("Created By"));
        expect(getAllByTestId(/^agentCountCell-/).map(c => c.textContent)).toEqual(["0", "2"]);
    });
});

describe("SkillsView tools/resources columns", () => {
    const richSkill = makeSkill({
        id: "44444444-4444-4444-4444-000000000004",
        name: "rich-skill",
        hasTools: true,
        hasReferences: true,
        hasAssets: true,
        resourceCount: 7,
        referenceCount: 5,
        assetCount: 2,
        bundledTools: [{ tool_name: "a" }, { tool_name: "b" }, { tool_name: "c" }],
    });
    const leanSkill = makeSkill({ id: "55555555-5555-5555-5555-000000000005", name: "lean-skill" });

    const fetchWithCounts = vi.fn().mockImplementation((url: string) => {
        const u = String(url);
        if (u.includes("origin=builtin")) return Promise.resolve(envelope([]));
        if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([richSkill, leanSkill]));
        return Promise.resolve(envelope([]));
    });

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });
    beforeEach(() => {
        vi.stubGlobal("fetch", fetchWithCounts);
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("renders bundled-tool, reference, and asset counts per skill", async () => {
        const { findByTestId, getByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        expect(await findByTestId("toolCountCell-44444444-4444-4444-4444-000000000004")).toHaveTextContent("3");
        expect(getByTestId("referenceCountCell-44444444-4444-4444-4444-000000000004")).toHaveTextContent("5");
        expect(getByTestId("assetCountCell-44444444-4444-4444-4444-000000000004")).toHaveTextContent("2");
        expect(getByTestId("toolCountCell-55555555-5555-5555-5555-000000000005")).toHaveTextContent("0");
        expect(getByTestId("referenceCountCell-55555555-5555-5555-5555-000000000005")).toHaveTextContent("0");
        expect(getByTestId("assetCountCell-55555555-5555-5555-5555-000000000005")).toHaveTextContent("0");
    });

    it("sorts numerically by the Tools column", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("rich-skill");

        expect(getAllByTestId(/^toolCountCell-/).map(c => c.textContent)).toEqual(["0", "3"]);
        fireEvent.click(getByText("Tools"));
        fireEvent.click(getByText("Tools"));
        expect(getAllByTestId(/^toolCountCell-/).map(c => c.textContent)).toEqual(["3", "0"]);
    });

    it("sorts numerically by the References column", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("rich-skill");

        expect(getAllByTestId(/^referenceCountCell-/).map(c => c.textContent)).toEqual(["0", "5"]);
        fireEvent.click(getByText("References"));
        fireEvent.click(getByText("References"));
        expect(getAllByTestId(/^referenceCountCell-/).map(c => c.textContent)).toEqual(["5", "0"]);
    });

    it("sorts numerically by the Assets column", async () => {
        const { findByText, getAllByTestId, getByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("rich-skill");

        expect(getAllByTestId(/^assetCountCell-/).map(c => c.textContent)).toEqual(["0", "2"]);
        fireEvent.click(getByText("Assets"));
        fireEvent.click(getByText("Assets"));
        expect(getAllByTestId(/^assetCountCell-/).map(c => c.textContent)).toEqual(["2", "0"]);
    });
});

describe("SkillsView row health indicator", () => {
    const pendingSkill = makeSkill({ id: "66666666-6666-6666-6666-000000000006", name: "pending-skill", discoveryStatus: "pending" });

    const fetchPending = vi.fn().mockImplementation((url: string) => {
        const u = String(url);
        if (u.includes("origin=builtin")) return Promise.resolve(envelope([]));
        if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([pendingSkill]));
        return Promise.resolve(envelope([]));
    });

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });
    beforeEach(() => {
        vi.stubGlobal("fetch", fetchPending);
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("renders a pending skill's row indicator as 'Processing' (skills-scoped relabel, not 'In Progress')", async () => {
        const { findByText, getByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });
        await findByText("pending-skill");

        const indicator = getByTestId("skillStatus-inProgress");
        expect(indicator).toHaveTextContent("Processing");
        expect(indicator).not.toHaveTextContent("In Progress");
    });
});

describe("SkillsView deferPanelClose on skill removal", () => {
    const skillA = makeSkill({ id: "aaaa-skill-id", name: "alpha-skill" });

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });

    it("calls deferPanelClose when the selected skill disappears from the refreshed list", async () => {
        const fetchMock = vi.fn().mockImplementation((url: string) => {
            const u = String(url);
            if (u.includes("origin=builtin")) return Promise.resolve(envelope([]));
            if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([skillA]));
            return Promise.resolve(envelope([]));
        });
        vi.stubGlobal("fetch", fetchMock);

        const rafSpy = vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation(cb => {
            cb(0);
            return 1;
        });

        const { findByTestId } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        fireEvent.click(await findByTestId(`skillRow-${skillA.id}`));

        fetchMock.mockImplementation((url: string) => {
            const u = String(url);
            if (u.includes("origin=builtin")) return Promise.resolve(envelope([]));
            if (u.includes("/api/v2/platform/skills")) return Promise.resolve(envelope([]));
            return Promise.resolve(envelope([]));
        });

        await act(async () => {
            window.dispatchEvent(new Event("skills-refetch"));
        });

        expect(rafSpy).toHaveBeenCalled();

        rafSpy.mockRestore();
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });
});
