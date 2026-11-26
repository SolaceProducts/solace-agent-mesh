// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { ArrowLeft, Info } from "lucide-react";
import z from "zod";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";
import { HelpPopover } from "@/common/HelpPopover";
import { EmptySection, PAGE_COMMON_CLASSES, PageFooter } from "@/common/layout";
import { HealthProblemsIndicator, STATUS_COLOUR_MAP, STATUS_LABEL_MAP, SyncStatusTooltip as SharedSyncStatusTooltip } from "@/common/status";

import { Button } from "@/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import type { Agent, AgentCardInfo, AgentState } from "@/types";

import { AgentCardComparisonDialog } from "./AgentCardComparisonDialog";

export const AgentEditSection = ({ children, className = "", fullHeight = false }: { children: React.ReactNode; className?: string; fullHeight?: boolean }) => {
    return <div className={`w-full pt-2 pb-4 ${fullHeight ? "flex h-full flex-col" : ""} ${className}`}>{children}</div>;
};

export const AgentEditWrapper = ({
    sectionTitle,
    agent,
    onApply,
    onDiscard,
    onSubmit,
    children,
    hints = null,
    className = "",
}: {
    sectionTitle: React.ReactNode;
    agent: Agent;
    onApply?: (agent: Agent) => void;
    onDiscard: () => void;
    onSubmit?: () => void;
    children: React.ReactNode;
    hints?: React.ReactNode;
    className?: string;
}) => {
    const handleApply = () => {
        if (onSubmit) {
            onSubmit();
        } else if (onApply) {
            onApply(agent);
        }
    };

    return (
        <>
            <div className={`flex flex-1 overflow-y-scroll ${className}`} style={{ scrollbarGutter: "stable" }}>
                <div className={`flex h-full flex-row py-8 pr-8 pl-4 ${hints ? "w-full" : PAGE_COMMON_CLASSES}`}>
                    <Button variant="ghost" tooltip="Discard and Go Back" onClick={onDiscard} className="-mt-1.5 mr-4">
                        <ArrowLeft />
                    </Button>
                    <div className="flex h-full w-full flex-col">
                        <div className="mb-4 w-full">{sectionTitle}</div>
                        <div className={`flex ${hints ? "gap-6" : ""} min-h-0 w-full flex-1`}>
                            <div className={`${hints ? "min-w-0 flex-1" : "w-full"} h-full`}>{children}</div>
                            {hints && <div className="w-88 flex-shrink-0">{hints}</div>}
                        </div>
                    </div>
                </div>
            </div>
            <PageFooter>
                <Button variant="ghost" testid="agentDiscardButton" title="Discard Changes" onClick={onDiscard}>
                    Discard Changes
                </Button>
                <Button type="submit" testid="agentApplyButton" title="Apply" onClick={handleApply}>
                    Apply
                </Button>
            </PageFooter>
        </>
    );
};

export const AgentEmptySection = ({ message = "", action, onClick, className }: { message?: string; action?: string; onClick?: () => void; className?: string }) => {
    return <EmptySection message={message} action={action} onClick={onClick} className={className} />;
};

export const agentInfoSchema = z.object({
    name: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(3, "Name must be at least 3 characters")
                .max(40, "Name must be 40 characters or less")
                .refine(val => SAFE_NAME_PATTERN.test(val), {
                    message: SAFE_NAME_DESCRIPTION,
                })
        ),
    description: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(10, "Value must be at least 10 characters")
                .max(1000, "Value must be 1000 characters or less")
                .refine(val => SAFE_TEXT_PATTERN.test(val), {
                    message: SAFE_TEXT_DESCRIPTION,
                })
        ),
});

export const getAgentToDisplay = (agent: AgentCardInfo, selectedState: AgentState): AgentCardInfo => {
    return selectedState === "active" ? { ...(agent.deployed ?? agent), syncStatus: agent.syncStatus } : agent;
};

type StatusDisplay = { displayedStatus: string; tooltipContent: string | null };

const DEFAULT_ACTIVE: StatusDisplay = { displayedStatus: "active", tooltipContent: null };
const REGISTERED: StatusDisplay = { displayedStatus: "registered", tooltipContent: null };

function resolveStatusDisplay(agent: AgentCardInfo, selectedState: AgentState): StatusDisplay {
    if (agent.status === "deploy_failed") return { displayedStatus: "deploy_failed", tooltipContent: null };
    if (agent.status === "disconnected") return { displayedStatus: "disconnected", tooltipContent: null };
    if (agent.status === "stopped") return { displayedStatus: "stopped", tooltipContent: null };
    if (agent.status === "degraded") return { displayedStatus: "degraded", tooltipContent: null };

    if (selectedState === "active") {
        if (agent.type === "external" && agent.agentCardLocation === "none" && agent.status === "deployed") {
            return REGISTERED;
        }
        return DEFAULT_ACTIVE;
    }

    return { displayedStatus: agent.status || "not_deployed", tooltipContent: null };
}

export const DeploymentStatus = ({ agent, selectedState, showSyncStatus = false }: { agent: AgentCardInfo; selectedState: AgentState; showSyncStatus?: boolean }) => {
    return useMemo(() => {
        const { displayedStatus, tooltipContent } = resolveStatusDisplay(agent, selectedState);

        const hasSyncTooltip = showSyncStatus && agent.syncStatus === "out_of_sync";

        const tooltipLines = [tooltipContent, hasSyncTooltip ? "Undeployed changes" : null].filter(Boolean);

        const statusContent = (
            <div className="flex items-center gap-2">
                <div className={`h-2 w-2 rounded-full ${STATUS_COLOUR_MAP[displayedStatus]}`}></div>
                <div>{STATUS_LABEL_MAP[displayedStatus]}</div>
                <HealthProblemsIndicator problems={agent.runtimeProblems} testIdPrefix="agentHealth" />
            </div>
        );

        if (tooltipLines.length > 0) {
            return (
                <div className="flex items-center gap-1">
                    {statusContent}
                    <Tooltip>
                        <TooltipTrigger asChild className="text-(--secondary-text-wMain)">
                            <Info className="size-5" data-testid="deploymentStatusInfo" />
                        </TooltipTrigger>
                        <TooltipContent>{tooltipLines.join(" · ")}</TooltipContent>
                    </Tooltip>
                </div>
            );
        }

        return statusContent;
    }, [agent.status, agent.type, agent.agentCardLocation, agent.syncStatus, agent.runtimeProblems, selectedState, showSyncStatus]);
};

export const SyncStatus = ({ draftAgent, deployedAgent, userAgent }: { draftAgent: AgentCardInfo; deployedAgent?: AgentCardInfo; userAgent?: Agent }) => {
    const [isComparisonOpen, setIsComparisonOpen] = useState(false);

    const handleClose = (e?: React.MouseEvent) => {
        if (e) {
            e.stopPropagation();
        }
        setIsComparisonOpen(false);
    };

    const badge = useMemo(() => {
        if (!deployedAgent || draftAgent.syncStatus !== "out_of_sync") return null;

        return <div>Undeployed changes</div>;
    }, [draftAgent, deployedAgent]);

    return (
        <>
            {badge}
            {isComparisonOpen ? <AgentCardComparisonDialog deployedAgent={deployedAgent} draftAgent={draftAgent} userAgent={userAgent} open={isComparisonOpen} onClose={handleClose} /> : null}
        </>
    );
};

export const SyncStatusTooltip = ({ deployedAgent }: { deployedAgent?: AgentCardInfo }) => {
    return <SharedSyncStatusTooltip entity={deployedAgent} />;
};

export const RegisteredStatusWithTooltip = () => {
    return (
        <div className="flex items-center gap-2">
            <div className="flex items-center gap-2">
                <div className={`h-2 w-2 rounded-full ${STATUS_COLOUR_MAP["registered"]}`}></div>
                <div>{STATUS_LABEL_MAP["registered"]}</div>
            </div>
            <HelpPopover content="This external agent has no agent card. Connection status is unavailable." />
        </div>
    );
};
