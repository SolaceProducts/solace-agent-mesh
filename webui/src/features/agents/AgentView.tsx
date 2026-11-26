// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards";
import { transformAgent } from "@/api/agents";
import { useConnectors } from "@/api/connectors";
import { useModelConfigs } from "@/api/models/hooks";
import { type ModelConfig } from "@/api/models/types";
import { useBuiltinSkills, useSkills } from "@/api/skills";
import { useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import { useUserContext } from "@/providers/contexts";

import { useInvokeScope } from "@/hooks/useInvokeScope";

import { PAGE_COMMON_CLASSES } from "@/common/layout";

import { Button } from "@/ui/button";

import { LearningBanner, MessageBanner, Metadata } from "@/common";
import type { Agent } from "@/types";
import { DOC_LINKS } from "@/utils";

import { type EditMode } from "./AgentEditPage";
import { AgentCardSection, AgentConnectorsSection, AgentDetailsSection, AgentInstructionsSection, AgentModelSection, AgentSkillsSection, AgentToolsetsSection } from "./AgentViewSections";

export const AgentView = ({ agent, setEditMode, showInstructionsError, showModelError }: { agent: Agent; setEditMode?: (mode: EditMode) => void; showInstructionsError?: boolean; showModelError?: boolean }) => {
    const { data: yamlAgentCards = [], isLoading: yamlAgentCardsLoading } = useAgentCards();
    const { data: connectors = [] } = useConnectors();
    const { data: toolsets = [], isSuccess: toolsetsResolved } = useBuiltinToolsets();
    const { data: customToolsets = [], isSuccess: customToolsetsResolved } = useCustomToolsets();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [isAgentInstructionsOpen, setIsAgentInstructionsOpen] = useState(false);
    const invokeScope = useInvokeScope("agent", agent.id);

    const agentCardInfo = useMemo(() => transformAgent(agent, toolsets, connectors, customToolsets), [agent, toolsets, connectors, customToolsets]);

    const selectedState = useMemo(() => {
        if (yamlAgentCardsLoading) return null;
        return (yamlAgentCards || []).some(a => a.displayName === agent.name) ? "active" : "inactive";
    }, [yamlAgentCards, agent.name, yamlAgentCardsLoading]);
    const agentInstructionsButton = useMemo(() => {
        return agent.systemPrompt ? (
            <Button data-testid="viewFullInstructionsButton" variant="ghost" title="View Full Instructions" onClick={() => setIsAgentInstructionsOpen(true)}>
                View Full Instructions
            </Button>
        ) : null;
    }, [setIsAgentInstructionsOpen]);

    const { data: models, isLoading: modelsLoading } = useModelConfigs();
    const { data: allSkills } = useSkills();
    const { data: builtinSkills } = useBuiltinSkills();

    const selectedModel = useMemo(() => {
        if (!models) return null;
        const ref = agent.modelProvider?.[0];
        if (ref) {
            return models.find((m: ModelConfig) => m.id === ref || m.alias === ref) ?? null;
        }
        return models.find((m: ModelConfig) => m.alias === "general") ?? null;
    }, [agent.modelProvider, models]);

    const getEditHandler = useMemo(() => {
        return (mode: EditMode) => {
            return setEditMode ? () => setEditMode(mode) : undefined;
        };
    }, [setEditMode]);

    const isNewStandardAgent = !!setEditMode && !agent.id && (!agent.type || agent.type === "standard");
    const inEditMode = !!setEditMode;
    const showSyncStatus = userHasCapability(CAPABILITIES.readAgents);
    const canLinkToToolsetDetail = userHasCapability(CAPABILITIES.readToolsets);
    const toolsetsCatalogResolved = toolsetsResolved && customToolsetsResolved;

    return (
        <div className={setEditMode ? "w-full" : PAGE_COMMON_CLASSES}>
            {setEditMode && !!agent.syncStatus && <MessageBanner variant="info" className="mb-6" message="After you save changes, you must deploy them so that they appear in the running agent." />}
            {!setEditMode && agent.syncStatus === "out_of_sync" && <MessageBanner variant="info" className="mb-6" message="There are undeployed changes for this agent. Please review and deploy these updates." />}
            <div className={setEditMode ? "grid grid-cols-1 gap-6 md:grid-cols-[minmax(0,1fr)_352px] md:items-start" : ""}>
                <AgentDetailsSection agent={agent} inEditMode={inEditMode} agentCardInfo={agentCardInfo} selectedState={selectedState} showSyncStatus={showSyncStatus} getEditHandler={getEditHandler} />
                <AgentInstructionsSection
                    agent={agent}
                    inEditMode={inEditMode}
                    showInstructionsError={showInstructionsError}
                    instructionsButton={agentInstructionsButton}
                    isNewStandardAgent={isNewStandardAgent}
                    isInstructionsOpen={isAgentInstructionsOpen}
                    setIsInstructionsOpen={setIsAgentInstructionsOpen}
                    getEditHandler={getEditHandler}
                />
                <AgentSkillsSection agent={agent} inEditMode={inEditMode} allSkills={allSkills} builtinSkills={builtinSkills} getEditHandler={getEditHandler} />
                <AgentToolsetsSection
                    agent={agent}
                    inEditMode={inEditMode}
                    toolsets={toolsets}
                    customToolsets={customToolsets}
                    canLinkToToolsetDetail={canLinkToToolsetDetail}
                    toolsetsCatalogResolved={toolsetsCatalogResolved}
                    getEditHandler={getEditHandler}
                />
                <AgentConnectorsSection agent={agent} inEditMode={inEditMode} connectors={connectors} getEditHandler={getEditHandler} />
                <AgentModelSection
                    agent={agent}
                    inEditMode={inEditMode}
                    selectedModel={selectedModel}
                    isNewStandardAgent={isNewStandardAgent}
                    models={models}
                    modelsLoading={modelsLoading}
                    showModelError={showModelError}
                    getEditHandler={getEditHandler}
                />
                <AgentCardSection agent={agent} inEditMode={inEditMode} getEditHandler={getEditHandler} />
                {!setEditMode && agent?.id ? <Metadata metadata={agent} scope={invokeScope} /> : null}
                {setEditMode && (
                    <div className="md:col-start-2 md:row-start-4">
                        <LearningBanner title="Extend Agent with Custom Toolsets" storageKey="sam-custom-tools-learning-1-dismissed">
                            Add custom toolsets to extend your agent's capabilities.
                            <br />
                            <br />
                            Create custom toolsets in your development environment and upload them to the Agent Mesh.
                            <br />
                            <br />
                            <Link to={DOC_LINKS.buildingToolsets} target="_blank" rel="noopener noreferrer">
                                <Button variant="link" className="p-0 has-[>svg]:px-0" title="Learn about Custom Tools">
                                    <div className="truncate">Learn about Custom Tools</div>
                                    <ExternalLink />
                                </Button>
                            </Link>
                        </LearningBanner>
                    </div>
                )}
            </div>
        </div>
    );
};
