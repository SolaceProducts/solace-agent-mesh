// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BookOpen, BookOpenText, Bot, Cpu, IdCard, Loader2, Plug, Wrench } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { type ModelConfig } from "@/api/models/types";

import { getSectionTitle, PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { toolsetRefLabel } from "@/utils/toolsetRefLabel";

import { MessageBanner } from "@/common";
import type { Agent, AgentCardInfo, Connector, Skill, Toolset } from "@/types";

import { AgentAbilityCard, AgentAttachedSkillCard, AgentConnectorCard, AgentToolsetCard } from "./AgentAbilityCard";
import { AgentCommunicationMode } from "./AgentCommunicationMode";
import { EDIT_MODE, type EditMode } from "./AgentEditPage";
import { AgentInstructionsDialog } from "./AgentInstructionsDialog";
import { AgentModelCard } from "./AgentModelCard";
import { AgentEditSection, AgentEmptySection, DeploymentStatus } from "./common";
import { useConnectorRuntimeToolNames } from "./hooks";
import { builtinSkillRefLabel, customSkillLabel } from "./skillLabels";
import { runtimeToolNamesForConnector } from "./utils/toolRegistrations";

type EditHandler = (mode: EditMode) => (() => void) | undefined;

const sectionWrapperClass = (inEditMode: boolean) => (inEditMode ? "col-start-1 mb-4 py-3" : "mb-4 py-3");

export const AgentDetailsSection = ({
    agent,
    inEditMode,
    agentCardInfo,
    selectedState,
    showSyncStatus,
    getEditHandler,
}: {
    agent: Agent;
    inEditMode: boolean;
    agentCardInfo: AgentCardInfo;
    selectedState: "active" | "inactive" | null;
    showSyncStatus: boolean;
    getEditHandler: EditHandler;
}) => (
    <div className={inEditMode ? "col-start-1 mb-4 pb-3" : "mb-4 pb-3"}>
        {getSectionTitle(IdCard, "Agent Details", getEditHandler(EDIT_MODE.AGENT_INFO))}
        <AgentEditSection>
            <div className="pb-4">These details help people and other agents understand what your agent can do.</div>
            <div className="grid grid-cols-[100px_1fr] gap-2">
                <PageLabel>Name</PageLabel>
                <div data-testid="agentNameDisplay" className="flex items-center">
                    {agent?.name || <div className="text-xs text-(--secondary-text-wMain)">Not set</div>}
                </div>
                <PageLabel>Description</PageLabel>
                <div className="flex items-center">{agent?.description || <div className="text-xs text-(--secondary-text-wMain)">Not set</div>}</div>
                <PageLabel>Status</PageLabel>
                <div className="flex items-center gap-2">{selectedState && <DeploymentStatus agent={agentCardInfo} selectedState={selectedState} showSyncStatus={showSyncStatus} />}</div>
            </div>
        </AgentEditSection>
    </div>
);

export const AgentInstructionsSection = ({
    agent,
    inEditMode,
    showInstructionsError,
    instructionsButton,
    isNewStandardAgent,
    isInstructionsOpen,
    setIsInstructionsOpen,
    getEditHandler,
}: {
    agent: Agent;
    inEditMode: boolean;
    showInstructionsError?: boolean;
    instructionsButton: ReactNode;
    isNewStandardAgent: boolean;
    isInstructionsOpen: boolean;
    setIsInstructionsOpen: (open: boolean) => void;
    getEditHandler: EditHandler;
}) => (
    <div className={sectionWrapperClass(inEditMode)}>
        {getSectionTitle(BookOpenText, "Instructions", getEditHandler(EDIT_MODE.AGENT_INSTRUCTIONS), instructionsButton, isNewStandardAgent)}
        <AgentEditSection>
            <div className="pb-4">Instructions tell your agent how to behave and respond to get the results you expect.</div>
            {agent?.systemPrompt ? (
                <div className="flex flex-row">
                    <div className="w-1 max-w-1 min-w-1 rounded bg-(--secondary-w10)"></div>
                    <div className="min-w-0 p-4">
                        <div className="line-clamp-3 break-words whitespace-pre-wrap">{agent.systemPrompt}</div>
                    </div>
                </div>
            ) : (
                <>
                    <AgentEmptySection message={inEditMode ? "" : "No instructions"} action="Add Instructions" onClick={getEditHandler(EDIT_MODE.AGENT_INSTRUCTIONS)} className="w-[calc(100%-8px)]" />
                    {showInstructionsError && <ErrorLabel className="mt-1" message="Instructions are required" />}
                </>
            )}
        </AgentEditSection>
        <AgentInstructionsDialog agent={agent} isOpen={isInstructionsOpen} setIsOpen={setIsInstructionsOpen} />
    </div>
);

export const AgentToolsetsSection = ({
    agent,
    inEditMode,
    toolsets,
    customToolsets,
    canLinkToToolsetDetail,
    toolsetsCatalogResolved = true,
    getEditHandler,
}: {
    agent: Agent;
    inEditMode: boolean;
    toolsets?: Toolset[];
    customToolsets?: Toolset[];
    canLinkToToolsetDetail: boolean;
    toolsetsCatalogResolved?: boolean;
    getEditHandler: EditHandler;
}) => (
    <div className={sectionWrapperClass(inEditMode)} id="toolsets-section">
        {getSectionTitle(Wrench, `Toolsets (${agent.toolsets?.length ?? 0})`, getEditHandler(EDIT_MODE.AGENT_TOOLS))}
        <AgentEditSection>
            <div className="pb-4">Toolsets give your agent always-on capabilities like data analysis, web search, etc. Choose from built-in toolsets or create custom toolsets with capabilities specific to your team or organization.</div>
            {agent?.toolsets && agent.toolsets.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                    {agent.toolsets.map(toolsetName => {
                        const builtin = toolsets?.find(c => c.id === toolsetName);
                        const custom = customToolsets?.find(p => p.name === toolsetName);
                        const toolset = builtin ?? custom;
                        if (!toolset && !toolsetsCatalogResolved) return null;
                        const card = <AgentToolsetCard toolset={toolset ?? { name: toolsetRefLabel(toolsetName) }} discoveryStatus={toolset?.discoveryStatus} className="w-full" />;
                        return custom && canLinkToToolsetDetail ? (
                            <Link key={toolsetName} to={`/toolsets/${custom.id}`} className="block w-full">
                                {card}
                            </Link>
                        ) : (
                            <div key={toolsetName} className="w-full">
                                {card}
                            </div>
                        );
                    })}
                </div>
            ) : (
                <AgentEmptySection message={inEditMode ? "" : "No toolsets"} action="Add Toolsets" onClick={getEditHandler(EDIT_MODE.AGENT_TOOLS)} className="w-[calc(100%-8px)]" />
            )}
        </AgentEditSection>
    </div>
);

export const AgentSkillsSection = ({ agent, inEditMode, allSkills, builtinSkills, getEditHandler }: { agent: Agent; inEditMode: boolean; allSkills?: Skill[]; builtinSkills?: Skill[]; getEditHandler: EditHandler }) => {
    const skillCount = (agent.skillIds?.length ?? 0) + (agent.skillRefs?.length ?? 0);
    return (
        <div className={sectionWrapperClass(inEditMode)}>
            {getSectionTitle(BookOpen, `Skills (${skillCount})`, getEditHandler(EDIT_MODE.AGENT_SKILLS))}
            <AgentEditSection>
                <div className="pb-4">
                    Skills are specialized bundles of tools and context that your agent loads on demand. They are ideal for less frequent, complex tasks where detailed instructions and reference material are needed alongside the tools.
                </div>
                {skillCount > 0 ? (
                    <div className="flex flex-wrap gap-2">
                        {agent.skillRefs?.map(ref => {
                            const builtin = builtinSkills?.find(s => s.name === ref);
                            return <AgentAttachedSkillCard key={ref} skill={{ name: builtinSkillRefLabel(ref, builtin), description: builtin?.description }} className="w-full" />;
                        })}
                        {agent.skillIds?.map(skillId => {
                            const skill = allSkills?.find(s => s.id === skillId);
                            return <AgentAttachedSkillCard key={skillId} skill={{ name: customSkillLabel(skillId, skill), description: skill?.description }} className="w-full" />;
                        })}
                    </div>
                ) : (
                    <AgentEmptySection message={inEditMode ? "" : "No skills"} action="Add Skills" onClick={getEditHandler(EDIT_MODE.AGENT_SKILLS)} className="w-[calc(100%-8px)]" />
                )}
            </AgentEditSection>
        </div>
    );
};

export const AgentConnectorsSection = ({ agent, inEditMode, connectors, getEditHandler }: { agent: Agent; inEditMode: boolean; connectors?: Connector[]; getEditHandler: EditHandler }) => {
    const runtimeToolNamesByConnector = useConnectorRuntimeToolNames();
    return (
        <div className={sectionWrapperClass(inEditMode)}>
            {getSectionTitle(Plug, `Connectors (${agent.connectors?.length ?? 0})`, getEditHandler(EDIT_MODE.AGENT_CONNECTORS))}
            <AgentEditSection>
                <div className="pb-4">Connectors allow your agent to interact with external systems.</div>
                {agent?.connectors && agent.connectors.length > 0 ? (
                    <div className="flex flex-wrap gap-2">
                        {agent.connectors.map(connector => {
                            if (typeof connector !== "string") return null;
                            const connectorObject = connectors?.find(c => c.id === connector);
                            return connectorObject ? (
                                <AgentConnectorCard
                                    key={connector}
                                    connector={connectorObject}
                                    className="w-full"
                                    showTools={true}
                                    iconSize="sm"
                                    runtimeToolNames={runtimeToolNamesForConnector(agent.toolRegistrations, connectorObject.id) ?? (connectorObject.id ? runtimeToolNamesByConnector.get(connectorObject.id) : undefined)}
                                />
                            ) : null;
                        })}
                    </div>
                ) : (
                    <AgentEmptySection message={inEditMode ? "" : "No connectors"} action="Add Connectors" onClick={getEditHandler(EDIT_MODE.AGENT_CONNECTORS)} className="w-[calc(100%-8px)]" />
                )}
            </AgentEditSection>
        </div>
    );
};

export const AgentCardSection = ({ agent, inEditMode, getEditHandler }: { agent: Agent; inEditMode: boolean; getEditHandler: EditHandler }) => (
    <div className={sectionWrapperClass(inEditMode)}>
        {getSectionTitle(Bot, "Agent Card", getEditHandler(EDIT_MODE.AGENT_CARD))}
        <AgentEditSection>
            <div>The agent card describes its expertise and supported data formats, helping other agents find it.</div>
            <div className="mt-6">
                <div className="text-sm font-semibold">Abilities</div>
                <div className="mt-1 text-sm text-(--secondary-text-wMain)">Use cases the agent advertises on its agent card so other agents know what it can do.</div>
                {agent?.skills && agent.skills.length > 0 ? (
                    <div className="mt-4 flex flex-wrap gap-2">
                        {agent.skills.map(skill => (
                            <AgentAbilityCard key={skill.name} ability={skill} className="w-full" />
                        ))}
                    </div>
                ) : (
                    <AgentEmptySection className="mt-4 w-[calc(100%-8px)]" message={inEditMode ? "" : "No abilities"} action="Add Abilities" onClick={getEditHandler(EDIT_MODE.AGENT_CARD)} />
                )}
            </div>
            <div className="mt-6">
                <div className="text-sm font-semibold">Communication Modes</div>
                <div className="mt-4 grid grid-cols-[100px_1fr] gap-4">
                    <AgentCommunicationMode label="Input Modes" modes={agent?.inputModes ?? []} />
                    <AgentCommunicationMode label="Output Modes" modes={agent?.outputModes ?? []} />
                </div>
            </div>
        </AgentEditSection>
    </div>
);

export const AgentModelSection = ({
    agent,
    inEditMode,
    selectedModel,
    models,
    modelsLoading,
    showModelError,
    isNewStandardAgent,
    getEditHandler,
}: {
    agent: Agent;
    inEditMode: boolean;
    selectedModel: ModelConfig | null;
    models?: ModelConfig[];
    modelsLoading: boolean;
    showModelError?: boolean;
    isNewStandardAgent: boolean;
    getEditHandler: EditHandler;
}) => {
    let modelBody: ReactNode;
    if (modelsLoading) {
        modelBody = (
            <div className="flex h-25 items-center justify-center">
                <Loader2 className="size-6 animate-spin text-(--brand-wMain)" />
            </div>
        );
    } else if (selectedModel) {
        modelBody = <AgentModelCard model={selectedModel} className="w-full" />;
    } else {
        modelBody = (
            <>
                <AgentEmptySection message={inEditMode ? "" : "No model selected"} action="Add Model" onClick={getEditHandler(EDIT_MODE.AGENT_MODEL)} className="w-[calc(100%-8px)]" />
                {showModelError && <ErrorLabel className="mt-1" message="A model is required" />}
            </>
        );
    }
    return (
        <div className={sectionWrapperClass(inEditMode)}>
            {getSectionTitle(Cpu, "Model", getEditHandler(EDIT_MODE.AGENT_MODEL), undefined, isNewStandardAgent)}
            <AgentEditSection>
                {agent.modelProvider?.[0] && !modelsLoading && models && !selectedModel && <MessageBanner variant="warning" className="mb-4" message="Previously selected model is no longer available. Please select a new model." />}

                <div className="pb-4">Select the model configuration your agent will use.</div>
                {modelBody}
            </AgentEditSection>
        </div>
    );
};
