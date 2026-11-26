// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentSkillFE, Connector, ToolRegistration } from "@/types";

import { AgentAbilityCard, AgentConnectorCard, AgentToolsetCard } from "./AgentAbilityCard";
import { runtimeToolNamesForConnector } from "./utils/toolRegistrations";

type ToolsetEntry = { id?: string; name: string; description?: string };

const EmptyState = ({ text }: Readonly<{ text: string }>) => <div className="bg-(--secondary-w10) py-2 text-center text-(--secondary-text-wMain)">{text}</div>;

const SectionHeader = ({ text }: Readonly<{ text: string }>) => <div className="py-2 font-semibold">{text}</div>;

export function AgentAbilitiesSection({ skills }: Readonly<{ skills?: AgentSkillFE[] }>) {
    const list = skills ?? [];
    return (
        <>
            <SectionHeader text={`Abilities (${list.length})`} />
            <div className="flex flex-col gap-2">
                {list.map(skill => (
                    <AgentAbilityCard key={skill.name} ability={skill} allowExpandDescription={true} />
                ))}
                {list.length === 0 && <EmptyState text="No abilities." />}
            </div>
        </>
    );
}

export function AgentToolsetsSection({ tools, agentType }: Readonly<{ tools?: ToolsetEntry[]; agentType?: string }>) {
    const list = tools ?? [];
    return (
        <>
            <SectionHeader text={`${agentType === "user" ? "Toolsets" : "Tools"} (${list.length})`} />
            <div className="flex flex-col gap-2">
                {list.map(tool => (
                    <AgentToolsetCard key={tool.name} toolset={tool} allowExpandDescription={true} />
                ))}
                {list.length === 0 && <EmptyState text={agentType === "user" ? "No toolsets." : "No tools."} />}
            </div>
        </>
    );
}

export function AgentConnectorsSection({ connectors, showTools = false, toolRegistrations }: Readonly<{ connectors?: Connector[]; showTools?: boolean; toolRegistrations?: ToolRegistration[] }>) {
    const list = connectors ?? [];
    return (
        <>
            <SectionHeader text={`Connectors (${list.length})`} />
            <div className="flex flex-col gap-2">
                {list.map(connector => (
                    <AgentConnectorCard key={connector.id} connector={connector} allowExpandDescription={true} showTools={showTools} iconSize="sm" runtimeToolNames={runtimeToolNamesForConnector(toolRegistrations, connector.id)} />
                ))}
                {list.length === 0 && <EmptyState text="No connectors." />}
            </div>
        </>
    );
}
