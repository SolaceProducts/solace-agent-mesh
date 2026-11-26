// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Agent, Connector, Toolset } from "@/types";

const runtimeMap = new Map<string, string[]>();
vi.mock("./hooks", () => ({
    useConnectorRuntimeToolNames: () => runtimeMap,
}));

const capturedRuntimeNames: Record<string, string[] | undefined> = {};
const capturedToolsetCardNames: string[] = [];
vi.mock("./AgentAbilityCard", () => ({
    AgentConnectorCard: ({ connector, runtimeToolNames }: { connector: Connector; runtimeToolNames?: string[] }) => {
        capturedRuntimeNames[connector.id ?? ""] = runtimeToolNames;
        return null;
    },
    AgentAbilityCard: () => null,
    AgentAttachedSkillCard: () => null,
    AgentToolsetCard: ({ toolset }: { toolset: { name: string } }) => {
        capturedToolsetCardNames.push(toolset.name);
        return null;
    },
}));

import { AgentConnectorsSection, AgentToolsetsSection } from "./AgentViewSections";

const connector = { id: "conn-api-1", name: "API", type: "api", subtype: "http", values: {} } as unknown as Connector;
const getEditHandler = () => () => {};

describe("AgentConnectorsSection — runtime tool-name precedence", () => {
    beforeEach(() => {
        runtimeMap.clear();
        for (const key of Object.keys(capturedRuntimeNames)) delete capturedRuntimeNames[key];
    });

    test("prefers the agent's own tool registrations over the cross-agent map", () => {
        runtimeMap.set("conn-api-1", ["from_map"]);
        const agent = {
            connectors: ["conn-api-1"],
            toolRegistrations: [{ toolType: "openapi", connectorId: "conn-api-1", status: "ready", toolCount: 2, toolNames: ["own_a", "own_b"] }],
        } as unknown as Agent;

        render(<AgentConnectorsSection agent={agent} inEditMode={false} connectors={[connector]} getEditHandler={getEditHandler} />);

        expect(capturedRuntimeNames["conn-api-1"]).toEqual(["own_a", "own_b"]);
    });

    test("falls back to the cross-agent map when the agent has no own registration for the connector", () => {
        runtimeMap.set("conn-api-1", ["from_map"]);
        const agent = { connectors: ["conn-api-1"], toolRegistrations: [] } as unknown as Agent;

        render(<AgentConnectorsSection agent={agent} inEditMode={false} connectors={[connector]} getEditHandler={getEditHandler} />);

        expect(capturedRuntimeNames["conn-api-1"]).toEqual(["from_map"]);
    });
});

describe("AgentToolsetsSection — toolset refs the catalog has no entry for", () => {
    beforeEach(() => {
        capturedToolsetCardNames.length = 0;
    });

    test("renders a fallback card instead of dropping the toolset", () => {
        const agent = { toolsets: ["activity_reporting"] } as unknown as Agent;

        render(<AgentToolsetsSection agent={agent} inEditMode={false} toolsets={[]} customToolsets={[]} canLinkToToolsetDetail={false} getEditHandler={getEditHandler} />);

        expect(capturedToolsetCardNames).toEqual(["Activity Reporting"]);
    });

    test("still resolves a toolset the catalog does carry", () => {
        const webToolset = { id: "builtin_web_request_tools", name: "Web Request Tools" } as Toolset;
        const agent = { toolsets: ["builtin_web_request_tools"] } as unknown as Agent;

        render(<AgentToolsetsSection agent={agent} inEditMode={false} toolsets={[webToolset]} customToolsets={[]} canLinkToToolsetDetail={false} getEditHandler={getEditHandler} />);

        expect(capturedToolsetCardNames).toEqual(["Web Request Tools"]);
    });
});
