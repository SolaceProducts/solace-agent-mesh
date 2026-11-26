// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { transformAgentCard } from "@/api/agent-cards";

import type { AgentCard } from "@/types/a2a";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Skill } from "@/types";

import { AgentAttachedSkillsSection } from "./AgentAttachedSkillsSection";

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

describe("AgentAttachedSkillsSection", () => {
    it("renders 'No skills.' empty state when both arrays are empty", () => {
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection />);
        expect(getByText("Skills (0)")).toBeInTheDocument();
        expect(getByText("No skills.")).toBeInTheDocument();
    });

    it("renders built-in skillRefs resolved via builtinSkills + humanizes the ref when missing", () => {
        const builtin = makeSkill({ id: "sam-knowledge", name: "sam-knowledge", origin: "builtin", description: "Built-in knowledge skill" });
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection skillRefs={["sam-knowledge", "orphan-ref"]} builtinSkills={[builtin]} />);
        expect(getByText("Skills (2)")).toBeInTheDocument();
        expect(getByText("sam-knowledge")).toBeInTheDocument();
        expect(getByText("Built-in knowledge skill")).toBeInTheDocument();
        expect(getByText("Orphan Ref")).toBeInTheDocument();
    });

    it("prefers the catalog label over the ref slug for built-in skillRefs", () => {
        const builtin = makeSkill({ id: "sam-docs", name: "sam-docs", origin: "builtin", label: "Solace Agent Mesh Documentation", description: "Docs" });
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection skillRefs={["sam-docs"]} builtinSkills={[builtin]} />);
        expect(getByText("Solace Agent Mesh Documentation")).toBeInTheDocument();
    });

    it("renders custom skillIds resolved via customSkills + falls back to the id when missing", () => {
        const custom = makeSkill({ id: "id-1", name: "my-skill", description: "Custom skill" });
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection skillIds={["id-1", "id-missing"]} customSkills={[custom]} />);
        expect(getByText("Skills (2)")).toBeInTheDocument();
        expect(getByText("my-skill")).toBeInTheDocument();
        expect(getByText("Custom skill")).toBeInTheDocument();
        expect(getByText("id-missing")).toBeInTheDocument();
    });

    it("counts both skillIds + skillRefs in the section header", () => {
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection skillIds={["a", "b"]} skillRefs={["c"]} />);
        expect(getByText("Skills (3)")).toBeInTheDocument();
    });

    it("renders pre-resolved resolvedSkills (discovery-card path) without any catalog", () => {
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection resolvedSkills={[{ name: "Web Research", description: "Search the web" }, { name: "Diagrams" }]} />);
        expect(getByText("Skills (2)")).toBeInTheDocument();
        expect(getByText("Web Research")).toBeInTheDocument();
        expect(getByText("Search the web")).toBeInTheDocument();
        expect(getByText("Diagrams")).toBeInTheDocument();
    });

    it("includes resolvedSkills alongside skillIds/skillRefs in the count", () => {
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection skillIds={["a"]} skillRefs={["b"]} resolvedSkills={[{ name: "c" }]} />);
        expect(getByText("Skills (3)")).toBeInTheDocument();
    });

    it("renders skills from transformAgentCard's own output on the discovered-card path, not a hand-set prop", () => {
        const card = {
            name: "agent_discovered",
            description: "d",
            url: "solace:t",
            version: "1.0",
            protocolVersion: "0.3.0",
            defaultInputModes: ["text"],
            defaultOutputModes: ["text"],
            skills: [],
            capabilities: {
                extensions: [{ uri: "https://solace.com/a2a/extensions/sam/skills", params: { skills: [{ name: "Docs Lookup", description: "Read the docs" }] } }],
            },
        } as AgentCard;
        const info = transformAgentCard(card);
        const { getByText } = renderWithProviders(<AgentAttachedSkillsSection resolvedSkills={info.resolvedSkills} />);
        expect(getByText("Skills (1)")).toBeInTheDocument();
        expect(getByText("Docs Lookup")).toBeInTheDocument();
        expect(getByText("Read the docs")).toBeInTheDocument();
    });
});
