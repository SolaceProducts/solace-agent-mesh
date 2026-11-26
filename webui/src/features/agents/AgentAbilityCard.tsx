// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { BookOpen, ChevronUp, ExternalLink, Sparkles, Wrench } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { cn } from "@/utils/cnTailwind";

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/ui/accordion";
import { Button } from "@/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { ToolCard } from "@/features/connectors/components/ToolCard";
import { ConnectorIcon } from "@/features/connectors/ConnectorIcon";

import { useDescriptionTruncation } from "@/hooks";
import type { AgentSkillFE, Connector, ResolvedAgentSkill } from "@/types";

import { useConnectorTools } from "./hooks";

const DISCOVERY_PILL_CLASSES: Record<string, string> = {
    created: "bg-(--warning-w10) text-(--warning-wMain)",
    pending: "bg-(--warning-w10) text-(--warning-wMain)",
    ready: "bg-(--success-w10) text-(--success-wMain)",
    failed: "bg-(--error-w10) text-(--error-wMain)",
    removed: "bg-(--secondary-w10) text-(--secondary-text-wMain)",
};

const DISCOVERY_PILL_LABELS: Record<string, string> = {
    created: "Pending",
    pending: "Pending",
    ready: "Ready",
    failed: "Discovery Failed",
    removed: "Removed",
};

const DiscoveryStatusPill = ({ status }: { status: string }) => {
    if (status === "ready") return null;
    const classes = DISCOVERY_PILL_CLASSES[status] ?? "bg-(--secondary-w10) text-(--secondary-text-wMain)";
    const label = DISCOVERY_PILL_LABELS[status] ?? status;
    return <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", classes)}>{label}</span>;
};

const Card = ({
    Icon,
    ability,
    className,
    descriptionOverride,
    allowExpandDescription = false,
    actionButton,
}: {
    Icon: React.ReactNode;
    ability: { name: string; description?: string };
    className?: string;
    descriptionOverride?: string;
    allowExpandDescription?: boolean;
    actionButton?: React.ReactNode;
}) => {
    const { isExpanded, isTruncated, descriptionRef, toggleExpanded } = useDescriptionTruncation(ability.description ?? "", allowExpandDescription);

    return (
        <div className={cn("flex min-w-0 items-center gap-2 overflow-hidden rounded-sm border p-4", className)} id={ability.name}>
            <div className="flex-shrink-0">{Icon}</div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-center justify-between gap-2">
                    <div className="truncate font-medium" title={ability.name}>
                        {ability.name}
                    </div>
                    {allowExpandDescription && isTruncated && (
                        <Button
                            variant="ghost"
                            onClick={e => {
                                e.stopPropagation();
                                toggleExpanded();
                            }}
                            tooltip={isExpanded ? "Collapse description" : "Expand description"}
                            className="-mt-1 h-6 w-6"
                        >
                            {isExpanded ? <ChevronUp className="size-4" /> : <ChevronUp className="size-4 rotate-180 transform" />}
                        </Button>
                    )}
                </div>
                <div ref={descriptionRef} className={`text-small mt-1 text-(--secondary-text-wMain) ${allowExpandDescription && isExpanded ? "" : "truncate"}`}>
                    {descriptionOverride ?? ability.description}
                </div>
            </div>
            {actionButton && <div className="flex items-center">{actionButton}</div>}
        </div>
    );
};

export const AgentAbilityCard = ({ ability, className, allowExpandDescription, actionButton }: { ability: AgentSkillFE; className?: string; allowExpandDescription?: boolean; actionButton?: React.ReactNode }) => {
    const icon = (
        <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
            <Sparkles className="size-5" />
        </div>
    );

    return <Card Icon={icon} ability={ability} className={className} allowExpandDescription={allowExpandDescription} actionButton={actionButton} />;
};

export const AgentAttachedSkillCard = ({ skill, className, allowExpandDescription, actionButton }: { skill: ResolvedAgentSkill; className?: string; allowExpandDescription?: boolean; actionButton?: React.ReactNode }) => {
    const icon = (
        <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
            <BookOpen className="size-5" />
        </div>
    );

    return <Card Icon={icon} ability={skill} className={className} allowExpandDescription={allowExpandDescription} actionButton={actionButton} />;
};

export const AgentToolsetCard = ({
    toolset,
    className,
    allowExpandDescription,
    actionButton,
    discoveryStatus,
    configSummary,
}: {
    toolset: { name: string; description?: string; id?: string };
    className?: string;
    allowExpandDescription?: boolean;
    actionButton?: React.ReactNode;
    discoveryStatus?: string;
    configSummary?: string;
}) => {
    const icon = (
        <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
            <Wrench className="size-5" />
        </div>
    );

    const { isExpanded, isTruncated, descriptionRef, toggleExpanded } = useDescriptionTruncation(toolset.description ?? "", allowExpandDescription);

    return (
        <div className={cn("flex items-center gap-2 rounded-sm border p-4", className)} id={toolset.name}>
            <div className="flex-shrink-0">{icon}</div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-center justify-between gap-2">
                    <div className="truncate font-medium" title={toolset.name}>
                        {toolset.name}
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                        {discoveryStatus && <DiscoveryStatusPill status={discoveryStatus} />}
                        {allowExpandDescription && isTruncated && (
                            <Button
                                variant="ghost"
                                onClick={e => {
                                    e.stopPropagation();
                                    toggleExpanded();
                                }}
                                tooltip={isExpanded ? "Collapse description" : "Expand description"}
                                className="-mt-1 h-6 w-6"
                            >
                                {isExpanded ? <ChevronUp className="size-4" /> : <ChevronUp className="size-4 rotate-180 transform" />}
                            </Button>
                        )}
                    </div>
                </div>
                <div ref={descriptionRef} className={`text-small mt-1 text-(--secondary-text-wMain) ${allowExpandDescription && isExpanded ? "" : "truncate"}`}>
                    {toolset.description}
                </div>
                {configSummary && <div className="mt-1 text-xs text-(--secondary-text-wMain)">{configSummary}</div>}
            </div>
            {actionButton && <div className="flex items-center">{actionButton}</div>}
        </div>
    );
};

export const AgentConnectorCard = ({
    connector,
    className,
    allowExpandDescription,
    actionButton,
    showTools = false,
    iconSize = "md",
    runtimeToolNames,
}: {
    connector: Connector;
    className?: string;
    allowExpandDescription?: boolean;
    actionButton?: React.ReactNode;
    showTools?: boolean;
    iconSize?: "sm" | "md";
    runtimeToolNames?: string[];
}) => {
    const { getConnectorSchema } = useChatContext();
    const schema = getConnectorSchema(connector.type, connector.subtype);
    const schemaName = schema?.name || "N/A";

    const {
        tools: loadedTools,
        isLoading: isLoadingTools,
        error: toolsError,
    } = useConnectorTools({
        connector,
        enabled: showTools,
        runtimeToolNames,
    });

    const { isExpanded, isTruncated, descriptionRef, toggleExpanded } = useDescriptionTruncation(connector.description, true);

    const connectorUrl = connector.id ? `#/connectors/${connector.id}` : undefined;

    return (
        <div className={cn("flex items-start rounded-sm border p-4", iconSize === "md" ? "gap-2" : "gap-1", className)} id={connector.name} data-testid={`agent-connector-card-${connector.id || "unknown"}`}>
            <div className={cn("flex-shrink-0", iconSize === "md" && "mt-1")}>
                <ConnectorIcon icon={schema?.icon} name={schemaName} size={iconSize} />
            </div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-start justify-between">
                    <div className="flex min-w-0 flex-1 flex-col">
                        <div className="flex min-w-0 items-center">
                            {connectorUrl ? (
                                <button
                                    type="button"
                                    onClick={e => {
                                        e.stopPropagation();
                                        window.open(connectorUrl, "_blank", "noopener,noreferrer");
                                    }}
                                    className="flex min-w-0 cursor-pointer items-center gap-1.5 truncate border-0 bg-transparent p-0 font-medium text-(--primary-wMain) hover:underline"
                                    title={connector.name}
                                >
                                    <span className="truncate">{connector.name}</span>
                                    <ExternalLink className="h-3.5 w-3.5 flex-shrink-0" />
                                </button>
                            ) : (
                                <div className="truncate font-medium" title={connector.name}>
                                    {connector.name}
                                </div>
                            )}
                        </div>
                        <Tooltip open={isTruncated && !isExpanded ? undefined : false}>
                            <TooltipTrigger asChild>
                                <div ref={descriptionRef} className={`text-small mt-1 min-w-0 text-(--secondary-text-wMain) ${allowExpandDescription && isExpanded ? "" : "truncate"}`}>
                                    {connector.description}
                                </div>
                            </TooltipTrigger>
                            <TooltipContent className="max-w-md whitespace-normal">{connector.description}</TooltipContent>
                        </Tooltip>
                    </div>
                    {allowExpandDescription && isTruncated && (
                        <Button
                            variant="ghost"
                            onClick={e => {
                                e.stopPropagation();
                                toggleExpanded();
                            }}
                            tooltip={isExpanded ? "Collapse description" : "Expand description"}
                            className="-mt-1 h-6 w-6 flex-shrink-0"
                        >
                            {isExpanded ? <ChevronUp className="size-4" /> : <ChevronUp className="size-4 rotate-180 transform" />}
                        </Button>
                    )}
                </div>
                {showTools && (connector.type === "mcp" || connector.type === "api") && (
                    <div className="mt-2 w-full min-w-0" data-testid="connector-tools-section">
                        <Accordion type="single" collapsible className="w-full">
                            <AccordionItem value="tools-section" className="border-none">
                                <AccordionTrigger className="flex-row-reverse justify-end gap-2 px-0 py-2 text-sm font-medium hover:no-underline [&>svg]:-rotate-90 [&[data-state=open]>svg]:rotate-0" data-testid="connector-tools-trigger">
                                    {isLoadingTools ? "Loading tools..." : `Tools (${loadedTools.length})`}
                                </AccordionTrigger>
                                <AccordionContent data-testid="connector-tools-content">
                                    {toolsError ? (
                                        <div className="text-sm text-(--error-wMain)">{toolsError}</div>
                                    ) : !isLoadingTools && loadedTools.length > 0 ? (
                                        <div className="flex w-full flex-col gap-2 pt-1">
                                            {loadedTools.map((tool, index) => (
                                                <div key={tool.operationId || tool.name || index} className="w-full min-w-0">
                                                    <ToolCard tool={tool} clickable={false} />
                                                </div>
                                            ))}
                                        </div>
                                    ) : !isLoadingTools ? (
                                        <div className="text-sm text-(--secondary-text-wMain)">No tools available</div>
                                    ) : null}
                                </AccordionContent>
                            </AccordionItem>
                        </Accordion>
                    </div>
                )}
            </div>
            {actionButton && <div className="flex items-center">{actionButton}</div>}
        </div>
    );
};
