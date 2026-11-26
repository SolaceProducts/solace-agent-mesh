// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { skillToV2 } from "@/testing/data/platformWireV2";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, Skill } from "@/types";

import { AgentSkillsEdit } from "./AgentSkillsEdit";

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

const CUSTOM_ID = "11111111-1111-1111-1111-111111111111";
const customSkill = makeSkill({ id: CUSTOM_ID, name: "my-custom-skill", description: "a custom one" });
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

const baseAgent: Agent = {
    name: "test-agent",
    description: "an agent for testing skill attachment",
    skillIds: [CUSTOM_ID],
    skillRefs: ["sam-knowledge", "legacy-fs-skill"],
};

const checkboxFor = (getByText: (t: string) => HTMLElement, name: string): HTMLElement => {
    const row = getByText(name).closest(".border") as HTMLElement;
    return within(row).getByRole("checkbox");
};

describe("AgentSkillsEdit", () => {
    beforeEach(() => {
        vi.stubGlobal("fetch", mockFetch);
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("renders built-in and custom sections with current selection reflected", async () => {
        const { findByText, getByText } = renderWithProviders(<AgentSkillsEdit agent={baseAgent} onApply={vi.fn()} onDiscard={vi.fn()} />, { initialPath: "/agent-mesh" });

        expect(await findByText("Built-in Skills")).toBeInTheDocument();
        expect(getByText("Custom Skills")).toBeInTheDocument();

        expect(checkboxFor(getByText, "sam-knowledge")).toBeChecked();
        expect(checkboxFor(getByText, "sam-docs")).not.toBeChecked();
        expect(checkboxFor(getByText, "my-custom-skill")).toBeChecked();
    });

    it("routes built-in selections to skillRefs and custom to skillIds, preserving legacy refs", async () => {
        const onApply = vi.fn();
        const { findByText, getByText, getByTestId } = renderWithProviders(<AgentSkillsEdit agent={baseAgent} onApply={onApply} onDiscard={vi.fn()} />, { initialPath: "/agent-mesh" });

        await findByText("Built-in Skills");

        fireEvent.click(checkboxFor(getByText, "sam-docs"));
        fireEvent.click(checkboxFor(getByText, "sam-knowledge"));
        fireEvent.click(checkboxFor(getByText, "my-custom-skill"));

        fireEvent.click(getByTestId("agentApplyButton"));

        expect(onApply).toHaveBeenCalledTimes(1);
        const applied = onApply.mock.calls[0][0] as Agent;
        expect(applied.skillIds).toEqual([]);
        expect([...(applied.skillRefs ?? [])].sort()).toEqual(["legacy-fs-skill", "sam-docs"]);
    });
});
