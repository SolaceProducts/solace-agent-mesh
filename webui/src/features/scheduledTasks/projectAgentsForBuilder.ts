// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface AvailableAgentInput {
    name: string;
    displayName?: string;
    description?: string;
    tools?: Array<{ name?: string; description?: string }>;
    isWorkflow?: boolean;
}

export interface AgentForBuilder {
    name: string;
    displayName?: string;
    description?: string;
    skills?: Array<{ name: string; description?: string }>;
}

export const projectAgentsForBuilder = (agents: AvailableAgentInput[]): AgentForBuilder[] =>
    agents.map(a => {
        if (a.isWorkflow || !a.tools?.length) {
            return { name: a.name, displayName: a.displayName, description: a.description };
        }
        const skills = a.tools.filter((t): t is { name: string; description?: string } => typeof t.name === "string" && t.name.length > 0).map(t => ({ name: t.name, description: t.description }));
        return {
            name: a.name,
            displayName: a.displayName,
            description: a.description,
            skills: skills.length === 0 ? undefined : skills,
        };
    });
