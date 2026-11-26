// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { AgentSkillFE, Connector } from "@/types";

import { AgentAbilitiesSection, AgentConnectorsSection, AgentToolsetsSection } from "./AgentCardSections";

const makeConnector = (over: Partial<Connector>): Connector => ({
    id: "conn-1",
    name: "PostgreSQL Connector",
    description: "A connector for PostgreSQL.",
    type: "sql",
    subtype: "postgres",
    values: {},
    ...over,
});

describe("AgentAbilitiesSection", () => {
    it("renders the empty state when skills is undefined", () => {
        const { getByText } = renderWithProviders(<AgentAbilitiesSection />);
        expect(getByText("Abilities (0)")).toBeInTheDocument();
        expect(getByText("No abilities.")).toBeInTheDocument();
    });

    it("renders the empty state when skills is an empty array", () => {
        const { getByText } = renderWithProviders(<AgentAbilitiesSection skills={[]} />);
        expect(getByText("Abilities (0)")).toBeInTheDocument();
        expect(getByText("No abilities.")).toBeInTheDocument();
    });

    it("renders the abilities and not the empty state when populated", () => {
        const skills: AgentSkillFE[] = [
            { name: "Summarize", description: "Summarizes text." },
            { name: "Translate", description: "Translates text." },
        ];
        const { getByText, queryByText } = renderWithProviders(<AgentAbilitiesSection skills={skills} />);
        expect(getByText("Abilities (2)")).toBeInTheDocument();
        expect(getByText("Summarize")).toBeInTheDocument();
        expect(getByText("Translate")).toBeInTheDocument();
        expect(queryByText("No abilities.")).not.toBeInTheDocument();
    });
});

describe("AgentToolsetsSection", () => {
    it("renders 'No tools.' empty state when tools is undefined and agentType is not 'user'", () => {
        const { getByText } = renderWithProviders(<AgentToolsetsSection />);
        expect(getByText("Tools (0)")).toBeInTheDocument();
        expect(getByText("No tools.")).toBeInTheDocument();
    });

    it("renders 'No tools.' empty state when tools is an empty array and agentType is not 'user'", () => {
        const { getByText } = renderWithProviders(<AgentToolsetsSection tools={[]} />);
        expect(getByText("Tools (0)")).toBeInTheDocument();
        expect(getByText("No tools.")).toBeInTheDocument();
    });

    it("renders 'No toolsets.' empty state when agentType is 'user' and the list is empty", () => {
        const { getByText, queryByText } = renderWithProviders(<AgentToolsetsSection tools={[]} agentType="user" />);
        expect(getByText("Toolsets (0)")).toBeInTheDocument();
        expect(getByText("No toolsets.")).toBeInTheDocument();
        expect(queryByText("No tools.")).not.toBeInTheDocument();
    });

    it("renders the tools and not the empty state when populated", () => {
        const tools = [
            { id: "t1", name: "web_request", description: "Makes HTTP requests." },
            { id: "t2", name: "time", description: "Returns the current time." },
        ];
        const { getByText, queryByText } = renderWithProviders(<AgentToolsetsSection tools={tools} />);
        expect(getByText("Tools (2)")).toBeInTheDocument();
        expect(getByText("web_request")).toBeInTheDocument();
        expect(getByText("time")).toBeInTheDocument();
        expect(queryByText("No tools.")).not.toBeInTheDocument();
        expect(queryByText("No toolsets.")).not.toBeInTheDocument();
    });
});

describe("AgentConnectorsSection", () => {
    it("renders the empty state when connectors is undefined", () => {
        const { getByText } = renderWithProviders(<AgentConnectorsSection />);
        expect(getByText("Connectors (0)")).toBeInTheDocument();
        expect(getByText("No connectors.")).toBeInTheDocument();
    });

    it("renders the empty state when connectors is an empty array", () => {
        const { getByText } = renderWithProviders(<AgentConnectorsSection connectors={[]} />);
        expect(getByText("Connectors (0)")).toBeInTheDocument();
        expect(getByText("No connectors.")).toBeInTheDocument();
    });

    it("renders the connectors and not the empty state when populated", () => {
        const connectors: Connector[] = [makeConnector({ id: "conn-1", name: "PostgreSQL Connector" }), makeConnector({ id: "conn-2", name: "MySQL Connector", subtype: "mysql" })];
        const { getByText, queryByText } = renderWithProviders(<AgentConnectorsSection connectors={connectors} />);
        expect(getByText("Connectors (2)")).toBeInTheDocument();
        expect(getByText("PostgreSQL Connector")).toBeInTheDocument();
        expect(getByText("MySQL Connector")).toBeInTheDocument();
        expect(queryByText("No connectors.")).not.toBeInTheDocument();
    });
});
