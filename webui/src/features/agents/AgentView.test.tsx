// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { mockConnectors, mockGetConnectorSchema } from "@/testing/data";
import { connectorToV2, skillToV2 } from "@/testing/data/platformWireV2";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, Skill } from "@/types";

import { AgentView } from "./AgentView";

const skill: Skill = {
    id: "skill-1",
    name: "My Skill",
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
};

const envelope = (data: unknown[]) => {
    const body = JSON.stringify({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: data.length, nextPage: null, totalPages: 1 } } });
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
    if (u.includes("/platform/connectors")) return Promise.resolve(envelope(mockConnectors.map(connectorToV2)));
    if (u.includes("/platform/skills")) return Promise.resolve(envelope([skillToV2(skill)]));
    return Promise.resolve(envelope([]));
});

const agent: Agent = {
    id: "agent-1",
    name: "Test Agent",
    description: "Agent with skills and connectors for the view render paths",
    type: "standard",
    systemPrompt: "You are a helpful assistant.",
    skillIds: ["skill-1"],
    connectors: ["mock-connector-1"],
    toolsets: [],
    modelProvider: [],
    inputModes: ["text"],
    outputModes: ["text"],
    skills: [],
};

describe("AgentView populated sections", () => {
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

    it("renders the skill (skillIds) and connector entries when both are populated", async () => {
        const { findByText, findAllByText } = renderWithProviders(<AgentView agent={agent} />, {
            chatContextValues: { getConnectorSchema: mockGetConnectorSchema },
        });

        expect(await findByText("My Skill")).toBeInTheDocument();

        expect((await findAllByText("PostgreSQL Connector")).length).toBeGreaterThan(0);
    });

    it("skips non-string connector entries", async () => {
        const mixed: Agent = { ...agent, connectors: [{ id: "mock-connector-1" } as unknown as string, "mock-connector-1"] };
        const { findAllByText } = renderWithProviders(<AgentView agent={mixed} />, {
            chatContextValues: { getConnectorSchema: mockGetConnectorSchema },
        });

        expect((await findAllByText("PostgreSQL Connector")).length).toBe(1);
    });

    it("lists built-in skill refs alongside custom skills and counts both", async () => {
        const withBuiltin: Agent = { ...agent, skillRefs: ["sam-knowledge"] };
        const { findByText, getByText } = renderWithProviders(<AgentView agent={withBuiltin} />, {
            chatContextValues: { getConnectorSchema: mockGetConnectorSchema },
        });

        expect(await findByText("Knowledge")).toBeInTheDocument();
        expect(await findByText("My Skill")).toBeInTheDocument();
        expect(getByText("Skills (2)")).toBeInTheDocument();
    });
});
