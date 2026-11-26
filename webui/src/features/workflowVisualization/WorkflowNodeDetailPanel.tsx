// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import yaml from "js-yaml";
import { Bot, Check, CircleCheckBig, Code, Copy, ExternalLink, FileText, GitBranch, Play, RefreshCw, Repeat2, Signpost, Workflow, Wrench } from "lucide-react";

import type { WorkflowConfig, WorkflowNodeConfig } from "@/utils/agentUtils";
import { getAgentSchemas } from "@/utils/agentUtils";

import { Button } from "@/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/ui/tabs";

import { type JSONValue, JSONViewer } from "@/common";
import type { AgentCardInfo } from "@/types";

import DecisionNodeDetails from "./DecisionNodeDetails";
import InputMappingViewer from "./InputMappingViewer";
import { nonEmptyString, splitToolName, toolNodeLabel } from "./utils/toolName";
import type { LayoutNode } from "./utils/types";
import { buildWorkflowNavigationUrl } from "./utils/workflowNavigation";

interface WorkflowNodeDetailPanelProps {
    node: LayoutNode | null;
    workflowConfig: WorkflowConfig | null;
    agents: AgentCardInfo[];
    onHighlightNodes?: (nodeIds: string[]) => void;
    knownNodeIds?: Set<string>;
    onNavigateToNode?: (nodeId: string) => void;
}

const LOGIC_NODE_DESCRIPTIONS: Record<string, string> = {
    map: "Executes a node for each item in a collection. Items are processed in parallel by default.",
    switch: "Routes execution based on conditions. Cases are evaluated in order; the first match wins.",
    loop: "Repeats a node until a condition becomes false. The first iteration always runs; the condition is checked before subsequent iterations.",
    decision: "Asks a decision model structured questions and routes on one choice answer. A low-confidence or failed decision can escalate to a fallback node.",
};

function isLogicNode(nodeType: LayoutNode["type"]): boolean {
    return Object.hasOwn(LOGIC_NODE_DESCRIPTIONS, nodeType);
}

export function getNodeIcon(nodeType: LayoutNode["type"]) {
    switch (nodeType) {
        case "start":
            return <Play className="h-6 w-6" />;
        case "end":
            return <CircleCheckBig className="h-6 w-6" />;
        case "agent":
            return <Bot className="h-6 w-6 text-(--brand-wMain)" />;
        case "workflow":
            return <Workflow className="h-6 w-6 text-(--brand-wMain)" />;
        case "tool":
            return <Wrench className="h-6 w-6 text-(--brand-wMain)" />;
        case "switch":
            return <GitBranch className="h-6 w-6 text-(--accent-n0-wMain)" />;
        case "decision":
            return <Signpost className="h-6 w-6 text-(--accent-n0-wMain)" />;
        case "map":
            return <Repeat2 className="h-6 w-6 text-(--accent-n0-wMain)" />;
        case "loop":
            return <RefreshCw className="h-6 w-6 text-(--accent-n0-wMain)" />;
        default:
            return null;
    }
}

function getTypeLabel(nodeType: LayoutNode["type"]): string {
    switch (nodeType) {
        case "start":
            return "Start Node";
        case "end":
            return "End Node";
        case "agent":
            return "Agent";
        case "workflow":
            return "Workflow";
        case "tool":
            return "Tool";
        case "switch":
            return "Switch";
        case "decision":
            return "Decision";
        case "map":
            return "Map";
        case "loop":
            return "Loop";
        default:
            return "Node";
    }
}

function CodeField({ label, value }: Readonly<{ label: string; value?: string }>) {
    if (!value) {
        return null;
    }
    return (
        <div className="mb-4">
            <span className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">{label}</span>
            <code className="font-mono text-sm">{value}</code>
        </div>
    );
}

function resolveToolName(node: LayoutNode, nodeConfig: WorkflowNodeConfig | undefined): string | undefined {
    return nonEmptyString(node.data.toolName) ?? nonEmptyString(nodeConfig?.tool_name);
}

function getNodeTitle(node: LayoutNode, agentDisplayName: string | undefined, nodeConfig: WorkflowNodeConfig | undefined): string {
    if (node.type === "agent") {
        return agentDisplayName ?? node.data.agentName ?? node.id;
    }
    if (node.type === "tool") {
        return toolNodeLabel({ connectorName: node.data.connectorName, toolName: resolveToolName(node, nodeConfig) }, node.id);
    }
    if (isLogicNode(node.type)) {
        return getTypeLabel(node.type);
    }
    return node.data.workflowName ?? node.id;
}

const WorkflowNodeDetailPanel: React.FC<WorkflowNodeDetailPanelProps> = ({ node, workflowConfig, agents, onHighlightNodes, knownNodeIds, onNavigateToNode }) => {
    const [showCodeView, setShowCodeView] = useState(false);
    const [isCopied, setIsCopied] = useState(false);
    const [activeTab, setActiveTab] = useState<"input" | "output">("input");

    const agentInfo = useMemo(() => {
        if (!node?.data.agentName) return null;
        return agents.find(a => a.name === node.data.agentName) || null;
    }, [node?.data.agentName, agents]);

    const agentSchemas = useMemo(() => {
        if (!agentInfo) return { inputSchema: undefined, outputSchema: undefined };
        return getAgentSchemas(agentInfo);
    }, [agentInfo]);

    const nodeConfig = node?.data.originalConfig;

    const handleCopy = useCallback(() => {
        if (!nodeConfig) return;
        try {
            const yamlStr = yaml.dump(nodeConfig, { indent: 2, lineWidth: -1 });
            navigator.clipboard.writeText(yamlStr);
            setIsCopied(true);
            setTimeout(() => setIsCopied(false), 2000);
        } catch (err) {
            console.error("Failed to copy:", err);
        }
    }, [nodeConfig]);

    const handleOpenWorkflow = useCallback(() => {
        if (node?.data.workflowName) {
            window.open("/#" + buildWorkflowNavigationUrl(node.data.workflowName), "_blank");
        }
    }, [node?.data.workflowName]);

    const getNodeDisplayName = useCallback(
        (nodeId: string) => {
            if (!workflowConfig?.nodes) return nodeId;

            const nodeConfigById = workflowConfig.nodes.find(n => n.id === nodeId);
            if (!nodeConfigById) return nodeId;

            if (nodeConfigById.type === "agent" && nodeConfigById.agent_name) {
                const agent = agents.find(a => {
                    const agentWithName = a as unknown as { name?: string };
                    return agentWithName.name === nodeConfigById.agent_name;
                });
                if (agent) {
                    const agentWithProps = agent as unknown as { displayName?: string; display_name?: string };
                    return agent.displayName || agentWithProps.display_name || nodeConfigById.agent_name || nodeId;
                }
                return nodeConfigById.agent_name || nodeId;
            }

            if (nodeConfigById.type === "workflow" && nodeConfigById.workflow_name) {
                return nodeConfigById.workflow_name;
            }

            return nodeId;
        },
        [workflowConfig?.nodes, agents]
    );

    if (!node) {
        return null;
    }

    const renderStatusBadge = () => {
        const isOnline = !!agentInfo;
        return (
            <span className="inline-flex items-center gap-1 text-sm font-medium">
                <span className={`h-1.5 w-1.5 rounded-full ${isOnline ? "bg-(--brand-wMain)" : "bg-(--secondary-w40)"}`} />
                {isOnline ? "Running" : "Offline"}
            </span>
        );
    };

    const renderCodeView = () => {
        if (!nodeConfig) {
            return <div className="flex h-full items-center justify-center">There is no code associated with this node.</div>;
        }
        try {
            const yamlStr = yaml.dump(nodeConfig, { indent: 2, lineWidth: -1 });
            return (
                <div className="relative h-full">
                    <Button variant="ghost" size="icon" onClick={handleCopy} tooltip={isCopied ? "Copied!" : "Copy"} className="absolute top-2 right-2 z-10 h-8 w-8">
                        {isCopied ? <Check className="h-4 w-4 text-(--success-wMain)" /> : <Copy className="h-4 w-4" />}
                    </Button>
                    <pre className="scrollbar-themed h-full overflow-auto rounded-lg p-3 font-mono text-sm">{yamlStr}</pre>
                </div>
            );
        } catch {
            return <div className="text-sm text-(--secondary-text-wMain)">Unable to display YAML</div>;
        }
    };

    const hasSchemas = node.type === "agent" || node.type === "workflow" || node.type === "tool";

    const getInputMapping = () => {
        return nodeConfig?.input || null;
    };

    const getInputSchema = () => {
        return nodeConfig?.input_schema_override ?? nodeConfig?.input_schema ?? agentSchemas.inputSchema ?? null;
    };

    const getOutputSchema = () => {
        return nodeConfig?.output_schema_override ?? nodeConfig?.output_schema ?? agentSchemas.outputSchema ?? null;
    };

    const isInputSchemaFromAgent = !nodeConfig?.input_schema_override && !!agentSchemas.inputSchema;
    const isOutputSchemaFromAgent = !nodeConfig?.output_schema_override && !!agentSchemas.outputSchema;

    const hasInputData = getInputMapping() || getInputSchema();

    const agentDisplayName = agentInfo?.displayName || agentInfo?.display_name || agentInfo?.name;

    const agentDescription = agentInfo?.description;

    const title = getNodeTitle(node, agentDisplayName, nodeConfig);

    const toolName = resolveToolName(node, nodeConfig);
    const { toolset, tool } = splitToolName(toolName ?? "");
    const connector = node.data.connectorName;

    return (
        <div className="flex h-full flex-col bg-(--background-w10)" role="complementary" aria-label="Node details panel">
            <div className="flex items-center justify-between border-b p-4">
                <div className="flex min-w-0 flex-1 items-center gap-2.5">
                    {getNodeIcon(node.type)}
                    <span className="truncate pr-2 text-[20px] font-semibold" title={title}>
                        {title}
                    </span>
                </div>
                <div className="flex items-center gap-2">
                    <Tabs value={showCodeView ? "code" : "details"} onValueChange={v => setShowCodeView(v === "code")}>
                        <TabsList className="bg-transparent p-0">
                            <TabsTrigger value="details" title="Details view" className="rounded-l-md rounded-r-none border-r-0">
                                <FileText className="h-4 w-4" />
                            </TabsTrigger>
                            <TabsTrigger value="code" title="Code view" className="rounded-l-none rounded-r-md">
                                <Code className="h-4 w-4" />
                            </TabsTrigger>
                        </TabsList>
                    </Tabs>
                </div>
            </div>

            <div className="scrollbar-themed flex-1 overflow-auto">
                {showCodeView ? (
                    <div className="h-full p-4">{renderCodeView()}</div>
                ) : (
                    <div className="p-4">
                        <div className="mb-4">
                            <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Node ID</label>
                            <code className="font-mono text-sm">{node.id}</code>
                        </div>

                        {node.type === "agent" && (
                            <div className="mb-4 grid grid-cols-2 gap-4">
                                <div>
                                    <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Status</label>
                                    {renderStatusBadge()}
                                </div>
                                <div>
                                    <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Node Type</label>
                                    <div className="text-sm">{getTypeLabel(node.type)}</div>
                                </div>
                            </div>
                        )}

                        {node.type !== "agent" && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Node Type</label>
                                <div className="text-sm">{getTypeLabel(node.type)}</div>
                            </div>
                        )}

                        {node.type === "tool" && (
                            <>
                                <CodeField label="Connector" value={connector} />
                                <CodeField label="Toolset" value={toolset} />
                                <CodeField label="Tool" value={tool} />
                            </>
                        )}

                        {isLogicNode(node.type) && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Description</label>
                                <div className="text-sm">{LOGIC_NODE_DESCRIPTIONS[node.type]}</div>
                            </div>
                        )}

                        {agentDescription && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Description</label>
                                <div className="text-sm">{agentDescription}</div>
                            </div>
                        )}

                        {nodeConfig?.instruction && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Instruction</label>
                                <div className="text-sm">{nodeConfig.instruction}</div>
                            </div>
                        )}

                        {node.type === "workflow" && node.data.workflowName && (
                            <div className="mb-4">
                                <Button variant="outline" size="sm" onClick={handleOpenWorkflow} className="w-full">
                                    Open Workflow
                                    <ExternalLink className="ml-2 h-4 w-4" />
                                </Button>
                            </div>
                        )}

                        {node.data.maxIterations && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Max Iterations</label>
                                <div className="text-sm">{node.data.maxIterations}</div>
                            </div>
                        )}

                        {node.data.delay && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Delay</label>
                                <div className="text-sm">{node.data.delay}</div>
                            </div>
                        )}

                        {node.data.condition && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Condition</label>
                                <div className="rounded bg-(--secondary-w10) p-2 font-mono text-xs">{node.data.condition}</div>
                            </div>
                        )}

                        {node.data.cases && node.data.cases.length > 0 && (
                            <div className="mb-4">
                                <label className="mb-2 block text-sm font-normal text-(--secondary-text-wMain)">Cases</label>
                                <div className="space-y-2">
                                    {node.data.cases.map((caseItem, index) => (
                                        <div key={index} className="grid grid-cols-[auto_1fr] gap-3">
                                            <div className="flex h-8 w-[30px] items-center justify-center rounded border text-sm">{index + 1}</div>
                                            <div className="min-h-[32px] rounded bg-(--secondary-w10) p-2 font-mono text-xs text-(--secondary-text-wMain)">{caseItem.condition}</div>
                                        </div>
                                    ))}
                                    {node.data.defaultCase && (
                                        <div className="grid grid-cols-[auto_1fr] gap-3">
                                            <div className="flex h-8 w-[30px] items-center justify-center rounded border">{node.data.cases.length + 1}</div>
                                            <div className="flex min-h-[32px] items-center rounded bg-(--secondary-w10) p-2 text-(--secondary-text-wMain)">
                                                <span className="text-sm">default → {getNodeDisplayName(node.data.defaultCase)}</span>
                                            </div>
                                        </div>
                                    )}
                                </div>
                            </div>
                        )}

                        {node.type === "decision" && nodeConfig && <DecisionNodeDetails config={nodeConfig} />}

                        {node.data.items && (
                            <div className="mb-4">
                                <label className="mb-1 block text-sm font-medium text-(--secondary-text-wMain)">Items</label>
                                <div className="rounded bg-(--secondary-w10) p-2 font-mono text-xs">{node.data.items}</div>
                            </div>
                        )}

                        {hasSchemas && (
                            <div className="mt-8">
                                <div className="mb-4 flex border-b border-(--secondary-w20)" role="tablist">
                                    <button
                                        role="tab"
                                        aria-selected={activeTab === "input"}
                                        onClick={() => setActiveTab("input")}
                                        className={`px-4 pb-2 font-medium transition-colors ${
                                            activeTab === "input" ? "border-b-2 border-(--brand-wMain) font-semibold text-(--primary-text-wMain)" : "text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)"
                                        }`}
                                    >
                                        Input
                                    </button>
                                    <button
                                        role="tab"
                                        aria-selected={activeTab === "output"}
                                        onClick={() => setActiveTab("output")}
                                        className={`ml-6 px-4 pb-2 font-medium transition-colors ${
                                            activeTab === "output" ? "border-b-2 border-(--brand-wMain) font-semibold text-(--primary-text-wMain)" : "text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)"
                                        }`}
                                    >
                                        Output
                                    </button>
                                </div>

                                {activeTab === "input" && (
                                    <div className="space-y-4">
                                        {hasInputData ? (
                                            <>
                                                {getInputMapping() && (
                                                    <div>
                                                        <label className="mb-4 block text-sm font-medium text-(--secondary-text-wMain)">Mapping</label>
                                                        <InputMappingViewer mapping={getInputMapping() as Record<string, unknown>} onHighlightNodes={onHighlightNodes} knownNodeIds={knownNodeIds} onNavigateToNode={onNavigateToNode} />
                                                    </div>
                                                )}

                                                {getInputSchema() && (
                                                    <div>
                                                        <label className="mb-2 block text-sm font-medium text-(--secondary-text-wMain)">
                                                            Schema
                                                            {isInputSchemaFromAgent && <span className="ml-2 font-normal text-(--secondary-text-wMain)">(from agent)</span>}
                                                        </label>
                                                        <div className="max-h-48 overflow-auto rounded-lg border">
                                                            <JSONViewer data={getInputSchema() as JSONValue} maxDepth={3} className="border-none text-xs" />
                                                        </div>
                                                    </div>
                                                )}
                                            </>
                                        ) : (
                                            <div className="rounded-lg border border-dashed p-4 text-center text-sm text-(--secondary-text-wMain)">No input defined</div>
                                        )}
                                    </div>
                                )}

                                {activeTab === "output" && (
                                    <div>
                                        {getOutputSchema() ? (
                                            <div>
                                                <label className="mb-2 block text-sm font-medium text-(--secondary-text-wMain)">
                                                    Schema
                                                    {isOutputSchemaFromAgent && <span className="ml-2 font-normal text-(--secondary-text-wMain)">(from agent)</span>}
                                                </label>
                                                <div className="max-h-64 overflow-auto rounded-lg border">
                                                    <JSONViewer data={getOutputSchema() as JSONValue} maxDepth={3} className="border-none text-xs" />
                                                </div>
                                            </div>
                                        ) : (
                                            <div className="rounded-lg border border-dashed p-4 text-center text-sm text-(--secondary-text-wMain)">No output schema defined</div>
                                        )}
                                    </div>
                                )}
                            </div>
                        )}
                    </div>
                )}
            </div>
        </div>
    );
};

export default WorkflowNodeDetailPanel;
