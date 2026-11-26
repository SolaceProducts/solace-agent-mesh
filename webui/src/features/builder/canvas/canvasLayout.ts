// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Edge, Node } from "@xyflow/react";
import { MarkerType } from "@xyflow/react";

import { type WorkflowConfig } from "@/utils/agentUtils";
import type { DecisionBranchKind } from "@/utils/decisionNode";

import { processWorkflowConfig } from "@/features/workflowVisualization/utils/layoutEngine";
import { escalationAnswerHandles } from "@/features/workflowVisualization/utils/types";

type WfLayoutResult = ReturnType<typeof processWorkflowConfig>;

import { humanizeComponentName, skillToolNames } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";
import { areWorkflowChildAgentsDone } from "../workflowChildStatus";

import { lookupConnectorIconByKey } from "./connectorIconInference";

function resolveWorkflowConfig(comp: NonNullable<BuildManifest["components"]>[number], data: ComponentCanvasData | undefined): WorkflowConfig | null {
    if (comp.nodes && comp.nodes.length > 0) {
        const idSet = new Set(comp.nodes.map(n => n.id));
        const agentNameToId = new Map<string, string>();
        for (const n of comp.nodes) {
            if (n.agent_name && !idSet.has(n.agent_name)) agentNameToId.set(n.agent_name, n.id);
        }
        const dependsOnResolvedToNodeIds = comp.nodes.map(n => ({
            ...n,
            depends_on: n.depends_on?.map(d => (idSet.has(d) ? d : (agentNameToId.get(d) ?? d))),
        }));
        return { nodes: dependsOnResolvedToNodeIds as WorkflowConfig["nodes"] };
    }
    if (data?.workflowConfig) return data.workflowConfig;
    return null;
}

const MAJOR_GRID = 120;
const MINOR_GRID = 24;

function snapToMajor(v: number): number {
    return Math.ceil(v / MAJOR_GRID) * MAJOR_GRID;
}

function snapToMinor(v: number): number {
    return Math.ceil(v / MINOR_GRID) * MINOR_GRID;
}

const CARD_WIDTHS: Record<string, number> = {
    workflow: 360,
    agent: 360,
    gateway: 360,
    connector: 280,
    skill: 280,
    loading: 240,
};

const START_CARD_CHROME_WIDTH = 188;
const START_CARD_NAME_CHAR_WIDTH = 8;
const START_CARD_MAX_WIDTH = 560;

export function startCardWidth(workflowName: string, pillWidth: number): number {
    const forName = START_CARD_CHROME_WIDTH + humanizeComponentName(workflowName).length * START_CARD_NAME_CHAR_WIDTH;
    return Math.max(pillWidth, Math.min(forName, START_CARD_MAX_WIDTH));
}

function panelContentWidth(wfLayout: WfLayoutResult, workflowName: string, diagramWidth: number): number {
    const start = wfLayout.nodes.find((n: { id: string; width: number }) => n.id === "__start__");
    return start ? Math.max(diagramWidth, startCardWidth(workflowName, start.width)) : diagramWidth;
}

function estimateCardHeight(type: string, data: ComponentCanvasData | null, connectorCount = 0, skillCount = 0, expanded = false, hasInstruction = false, paramCount = 0): number {
    const BASE: Record<string, number> = {
        workflow: 144,
        agent: 96,
        gateway: 72,
        connector: 120,
        skill: 96,
        loading: 96,
    };

    if (!data || data.isLoading) return BASE.loading ?? 96;

    let height = BASE[type] ?? 96;

    if (type === "gateway" && expanded) {
        height += 48;
        if (data.gatewayMetadata) {
            height += data.gatewayMetadata.eventHandlers.length * 24;
        }
    }

    if (type === "agent" && expanded) {
        if (hasInstruction) height += 24 + 136;
        if (connectorCount > 0) height += 24 + connectorCount * 40;
        if (skillCount > 0) height += 24 + skillCount * 40;
    }

    if (type === "connector" && expanded) {
        if (paramCount > 0) {
            const shown = Math.min(paramCount, 5);
            height += 24 + shown * 32 + (paramCount > shown ? 24 : 0);
        }
    }

    if (data.isBuildingDeps && data.dependencyProgress.length > 0) {
        height += 8 + data.dependencyProgress.length * 22;
    }

    return snapToMinor(height);
}

const COLUMN_ORDER: string[] = ["gateway", "workflow", "agent", "connector", "skill"];

function typeToColumnType(type: string): string | null {
    if (type === "toolset") return null;
    return COLUMN_ORDER.includes(type) ? type : "agent";
}

const MIN_COLUMN_GAP = 48;
const VERTICAL_GAP = 48;
const HEADER_HEIGHT = 24;
const HEADER_GAP = 24;

export interface CanvasLayout {
    nodes: Node[];
    edges: Edge[];
}

export function computeCanvasLayout(manifest: BuildManifest | null, componentData: Map<string, ComponentCanvasData>): CanvasLayout {
    if (!manifest) return { nodes: [], edges: [] };

    const nodes: Node[] = [];
    const edges: Edge[] = [];

    const sessionComponents = (manifest.components ?? []).filter(c => c.origin !== "platform");
    const nonSkillSessionComps = sessionComponents.filter(c => c.type !== "skill");
    const renderableSessionComps = sessionComponents.filter(c => c.type !== "skill" && c.type !== "connector");
    const agentComps = sessionComponents.filter(c => c.type === "agent");
    const gatewayComps = sessionComponents.filter(c => c.type === "gateway");
    const connectorComps = sessionComponents.filter(c => c.type === "connector");
    const isSingleAgent = agentComps.length === 1 && renderableSessionComps.length === 1 && renderableSessionComps[0].type === "agent";
    const isSingleGateway = gatewayComps.length === 1 && renderableSessionComps.length === 1 && renderableSessionComps[0].type === "gateway";
    const isSingleConnector = connectorComps.length === 1 && renderableSessionComps.length === 0 && nonSkillSessionComps.length === 1;

    const workflowOwnedComponents = new Set<string>();
    const workflowNames = new Set<string>();
    const workflowGateway = new Map<string, string>();
    for (const comp of manifest.components ?? []) {
        if (comp.type !== "workflow") continue;
        workflowNames.add(comp.name);
        if (comp.nodes) {
            for (const node of comp.nodes) {
                if (node.type === "agent" && node.agent_name) {
                    workflowOwnedComponents.add(node.agent_name);
                }
            }
        }
        if (comp.depends_on) {
            for (const dep of comp.depends_on) {
                const depComp = manifest.components?.find(c => c.name === dep);
                if (depComp?.type === "agent") {
                    workflowOwnedComponents.add(dep);
                }
            }
        }
    }
    for (const comp of manifest.components ?? []) {
        if (comp.type === "gateway" && comp.depends_on) {
            for (const dep of comp.depends_on) {
                if (workflowNames.has(dep)) {
                    workflowOwnedComponents.add(comp.name);
                    workflowGateway.set(dep, comp.name);
                }
            }
        }
        if (comp.type === "workflow" && comp.depends_on) {
            for (const dep of comp.depends_on) {
                const depComp = manifest.components?.find(c => c.name === dep);
                if (depComp?.type === "gateway") {
                    workflowOwnedComponents.add(dep);
                    workflowGateway.set(comp.name, dep);
                }
            }
        }
    }

    const columnGroups: Map<string, Array<{ comp: NonNullable<BuildManifest["components"]>[number]; data: ComponentCanvasData | undefined }>> = new Map();

    const ownedConnectors = new Set<string>();
    for (const comp of manifest.components ?? []) {
        if (comp.type !== "agent" && comp.type !== "workflow") continue;
        for (const dep of [...(comp.depends_on ?? []), ...(comp.connectors ?? [])]) {
            const depComp = manifest.components?.find(c => c.name === dep);
            if (depComp?.type === "connector") ownedConnectors.add(dep);
        }
    }

    for (const comp of manifest.components ?? []) {
        const colType = typeToColumnType(comp.type);
        if (!colType) continue;
        if (workflowOwnedComponents.has(comp.name) && comp.type !== "workflow") continue;
        if (comp.type === "connector" && ownedConnectors.has(comp.name)) continue;
        if (!columnGroups.has(colType)) columnGroups.set(colType, []);
        columnGroups.get(colType)!.push({ comp, data: componentData.get(comp.name) });
    }

    const activeColumns: Array<{ colType: string; xOffset: number; width: number }> = [];
    let currentX = 0;

    for (const colType of COLUMN_ORDER) {
        const items = columnGroups.get(colType);
        if (!items || items.length === 0) continue;

        let colWidth = 0;
        for (const { comp, data } of items) {
            const wfConfig = comp.type === "workflow" ? resolveWorkflowConfig(comp, data) : null;
            if (wfConfig) {
                const layout = processWorkflowConfig(wfConfig, new Set(), new Set());
                colWidth = Math.max(colWidth, snapToMajor(layout.totalWidth || 360));
            } else {
                colWidth = Math.max(colWidth, CARD_WIDTHS[comp.type] ?? 240);
            }
        }

        activeColumns.push({ colType, xOffset: currentX, width: colWidth });
        currentX = snapToMajor(currentX + colWidth + MIN_COLUMN_GAP);
    }

    const columnXMap = new Map(activeColumns.map(c => [c.colType, c.xOffset]));

    const contentStartY = HEADER_HEIGHT + HEADER_GAP;

    for (const colType of COLUMN_ORDER) {
        const items = columnGroups.get(colType);
        if (!items || items.length === 0) continue;
        const xOffset = columnXMap.get(colType) ?? 0;

        let y = contentStartY;
        for (const { comp, data } of items) {
            const type = comp.type || "unknown";

            const wfConfig = type === "workflow" ? resolveWorkflowConfig(comp, data) : null;
            if (wfConfig) {
                const wfLayout = processWorkflowConfig(wfConfig, new Set(), new Set());

                const gwName = workflowGateway.get(comp.name);
                if (gwName) {
                    const gwComp = manifest.components?.find(c => c.name === gwName);
                    const gwData = componentData.get(gwName);
                    if (gwComp) {
                        const startNode = wfLayout.nodes.find((n: { id: string; x: number; width: number }) => n.id === "__start__");
                        const startCenterX = startNode ? xOffset + startNode.x + startNode.width / 2 : xOffset + (wfLayout.totalWidth || CARD_WIDTHS.gateway) / 2;
                        const compactGwWidth = 240;
                        const gwHeight = 48;
                        const compactGwX = startCenterX - compactGwWidth / 2;
                        nodes.push({
                            id: gwComp.name,
                            type: gwData?.isLoading ? "loading" : "gateway",
                            position: { x: compactGwX, y },
                            data: { component: gwComp, canvasData: gwData ?? null, compact: true },
                            width: compactGwWidth,
                            height: gwHeight,
                        });
                        edges.push({
                            id: `gw-wf:${gwName}->${comp.name}`,
                            source: gwName,
                            target: `wf:${comp.name}:__start__`,
                            sourceHandle: "bottom",
                            targetHandle: "top",
                            type: "hierarchy",
                        });
                        y += gwHeight + VERTICAL_GAP;
                    }
                }

                const wfPrefix = `wf:${comp.name}:`;
                const wfHeight = snapToMinor(wfLayout.totalHeight || 192);
                const wfWidth = wfLayout.totalWidth || 360;

                const BG_PAD = 20;
                const PILL_HALF_H = 20;
                const bgWidth = panelContentWidth(wfLayout, comp.name, wfWidth) + BG_PAD * 2;
                const bgTop = y + PILL_HALF_H;
                const bgHeight = wfHeight - PILL_HALF_H + BG_PAD;
                const startNode = wfLayout.nodes.find((n: { id: string; x: number; width: number }) => n.id === "__start__");
                const diagramCenterX = startNode ? xOffset + startNode.x + startNode.width / 2 : xOffset + wfWidth / 2;
                const bgX = diagramCenterX - bgWidth / 2;
                nodes.push({
                    id: `${wfPrefix}__bg__`,
                    type: "workflow-bg",
                    position: { x: bgX, y: bgTop },
                    data: { width: bgWidth, height: bgHeight },
                    width: bgWidth,
                    height: bgHeight,
                    selectable: false,
                    draggable: false,
                    zIndex: -1,
                });

                addWorkflowNodes(nodes, edges, wfLayout, wfPrefix, xOffset, y, comp, data, componentData, workflowOwnedComponents, manifest);

                y = snapToMinor(y + wfHeight + VERTICAL_GAP * 2);
            } else {
                const cardType = data?.isLoading ? "loading" : type;
                const isExpandedAgent = type === "agent" && isSingleAgent;
                const isExpandedGateway = type === "gateway" && isSingleGateway;
                const isExpandedConnector = type === "connector" && isSingleConnector;
                const isExpanded = isExpandedAgent || isExpandedGateway || isExpandedConnector;

                let connCount = 0;
                let skillCount = 0;
                let hasInstruction = false;
                let paramCount = 0;
                if (isExpandedAgent) {
                    const connectorNames = new Set([...(comp.depends_on ?? []), ...(comp.connectors ?? [])]);
                    connCount = [...connectorNames].filter(dep => manifest.components?.find(c => c.name === dep)?.type === "connector").length;
                    skillCount = data?.agentConfig?.skills?.length ?? 0;
                    hasInstruction = !!data?.agentConfig?.instruction && data.agentConfig.instruction.trim().length > 0;
                }
                if (isExpandedConnector) {
                    paramCount = Object.keys(data?.connectorMetadata?.values ?? {}).length;
                }

                const width = isExpandedAgent ? 480 : isExpandedConnector ? 380 : (CARD_WIDTHS[type] ?? 240);
                let height = estimateCardHeight(type, data ?? null, connCount, skillCount, isExpanded, hasInstruction, paramCount);
                if (type === "skill" && data && !data.isLoading) {
                    const toolCount = skillToolNames(comp.tools, data.skillConfig?.tools).length;
                    const refCount = data.skillConfig?.references?.length ?? 0;
                    if (toolCount > 0) height += 44;
                    if (refCount > 0) height += 44;
                    height = snapToMinor(height);
                }

                nodes.push({
                    id: comp.name,
                    type: cardType,
                    position: { x: xOffset, y },
                    data: {
                        component: comp,
                        canvasData: data ?? null,
                        manifest,
                        ...(type === "agent" ? { componentData } : {}),
                        ...(isExpandedAgent ? { isSingleAgent: true } : {}),
                        ...(isExpandedGateway ? { isSingleGateway: true } : {}),
                        ...(isExpandedConnector ? { isSingleConnector: true } : {}),
                    },
                    width,
                    height,
                });

                y += height + VERTICAL_GAP;
            }
        }
    }

    for (const comp of manifest.components ?? []) {
        const data = componentData.get(comp.name);

        if (comp.type === "gateway" && data?.gatewayMetadata && !workflowOwnedComponents.has(comp.name)) {
            for (const handler of data.gatewayMetadata.eventHandlers) {
                const targetName = handler.target_agent_name || handler.target_workflow_name;
                if (!targetName) continue;

                const targetComp = manifest.components?.find(c => c.name === targetName);
                const targetId = targetComp?.type === "workflow" ? `wf:${targetName}:__start__` : targetName;

                if (nodes.some(n => n.id === targetId)) {
                    edges.push({
                        id: `gw:${comp.name}->${targetName}`,
                        source: comp.name,
                        target: targetId,
                        ...(targetId.includes(":__start__") ? { targetHandle: "left" } : {}),
                        type: "dependency",
                        animated: false,
                    });
                }
            }
        }

        if (comp.depends_on && !workflowOwnedComponents.has(comp.name)) {
            for (const dep of comp.depends_on) {
                const depComp = manifest.components?.find(c => c.name === dep);
                const targetId = depComp?.type === "workflow" ? `wf:${dep}:__start__` : dep;

                if (nodes.some(n => n.id === targetId)) {
                    edges.push({
                        id: `dep:${comp.name}->${dep}`,
                        source: comp.name,
                        target: targetId,
                        sourceHandle: "source",
                        targetHandle: targetId.includes(":__start__") ? "left" : "target",
                        type: "dependency",
                        animated: false,
                    });
                }
            }
        }
    }

    return { nodes, edges };
}

const DEFAULT_EDGE_MARKER = {
    type: MarkerType.ArrowClosed,
    width: 16,
    height: 16,
};

function workflowEdgeHandles(e: { escalationAnswer?: boolean; answerSide?: "left" | "right" }): { sourceHandle: string; targetHandle: string } {
    return e.escalationAnswer ? escalationAnswerHandles(e.answerSide ?? "right") : { sourceHandle: "bottom", targetHandle: "top" };
}

function addWorkflowNodes(
    nodes: Node[],
    edges: Edge[],
    wfLayout: WfLayoutResult,
    prefix: string,
    xOffset: number,
    yOffset: number,
    comp: NonNullable<BuildManifest["components"]>[number],
    data: ComponentCanvasData | undefined,
    componentData: Map<string, ComponentCanvasData>,
    workflowOwnedAgents: Set<string> = new Set(),
    manifest?: BuildManifest | null
) {
    const ownedAgentNodes = wfLayout.nodes.filter((n: { type: string; data: { agentName?: string } }) => n.type === "agent" && n.data.agentName);
    const allChildrenDone = areWorkflowChildAgentsDone(
        ownedAgentNodes.map((n: { data: { agentName?: string } }) => n.data.agentName!),
        componentData,
        manifest
    );

    const Y_SCALE = 1.0;

    const addLayoutNodes = (layoutNodes: typeof wfLayout.nodes) => {
        for (const n of layoutNodes) {
            const nodeId = `${prefix}${n.id}`;
            const isStart = n.id === "__start__";

            if (n.type === "agent" && n.data.agentName && manifest) {
                const agentManifest = manifest.components?.find(c => c.name === n.data.agentName);
                if (agentManifest) {
                    const connNames = new Set([...(agentManifest.depends_on ?? []), ...(agentManifest.connectors ?? [])]);
                    const icons = [...connNames]
                        .map(cn => manifest.components?.find(c => c.name === cn))
                        .filter(c => c?.type === "connector")
                        .map(c => {
                            const cd = componentData.get(c!.name);
                            let iconKey = cd?.connectorMetadata?.icon;
                            if (!iconKey || iconKey === "other") iconKey = c!.icon;
                            return { name: c!.name, url: iconKey && iconKey !== "other" ? lookupConnectorIconByKey(iconKey) : undefined };
                        })
                        .filter(ic => ic.url);
                    if (icons.length > 0) n.data.icons = icons;
                }
            }

            const newWidth = isStart ? startCardWidth(comp.name, n.width) : n.width;
            const xShift = isStart ? (n.width - newWidth) / 2 : 0;

            const agentExtra: Record<string, unknown> = {};
            if (n.type === "agent" && n.data.agentName && manifest) {
                const agentManifest = manifest.components?.find(c => c.name === n.data.agentName);
                const agentCd = componentData.get(n.data.agentName);
                agentExtra.agentCanvasData = agentCd ?? null;
                agentExtra.agentManifestStatus = agentManifest?.status;
                agentExtra.agentManifestId = agentManifest?.id;
                agentExtra.agentComponentName = n.data.agentName;
                const depNames = new Set([...(agentManifest?.depends_on ?? []), ...(agentManifest?.connectors ?? [])]);
                agentExtra.hasDepWaitingForInput = [...depNames].some(dn => {
                    const dc = manifest.components?.find(c => c.name === dn);
                    return dc?.type === "connector" && (componentData.get(dn)?.isWaitingForInput ?? false);
                });
            }

            nodes.push({
                id: nodeId,
                type: `wf-${n.type}`,
                position: { x: xOffset + n.x + xShift, y: yOffset + n.y * Y_SCALE },
                data: {
                    layoutNode: n,
                    ...(isStart ? { workflowComponent: comp, workflowCanvasData: data ?? null, allChildrenDone } : {}),
                    ...agentExtra,
                    onNodeClick: () => {},
                    onExpand: () => {},
                    onCollapse: () => {},
                    onHighlightNodes: () => {},
                    knownNodeIds: new Set<string>(),
                },
                width: newWidth,
                height: n.height,
            });
        }
    };

    addLayoutNodes(wfLayout.nodes);

    const nodesWithPill = new Set<string>();
    for (const n of wfLayout.nodes) {
        if ((n.type === "agent" && n.data.agentName) || n.id === "__start__") {
            nodesWithPill.add(n.id);
        }
    }

    const branchKindByPill = new Map<string, DecisionBranchKind>();
    for (const n of wfLayout.nodes) {
        if (n.type === "condition" && n.data.branchKind) branchKindByPill.set(n.id, n.data.branchKind);
    }

    for (const e of wfLayout.edges) {
        const sourceId = `${prefix}${e.source}`;
        const targetId = `${prefix}${e.target}`;
        const branchKind = branchKindByPill.get(e.target) ?? branchKindByPill.get(e.source);

        edges.push({
            id: `${prefix}${e.id}`,
            source: sourceId,
            target: targetId,
            ...workflowEdgeHandles(e),
            type: "workflow-edge",
            data: {
                isStraight: e.isStraight,
                noTargetPill: !nodesWithPill.has(e.target),
                isConditionEdge: e.target.startsWith("__condition_"),
                ...(branchKind ? { branchKind } : {}),
                ...(e.escalationAnswer ? { escalationAnswer: true } : {}),
            },
            markerEnd: e.target.startsWith("__condition_") ? undefined : DEFAULT_EDGE_MARKER,
        });
    }

    for (const n of wfLayout.nodes) {
        if ((n.type === "agent" || n.type === "workflow") && n.data.agentName) {
            const agentName = n.data.agentName;
            if (componentData.has(agentName) && !workflowOwnedAgents.has(agentName)) {
                edges.push({
                    id: `wf-agent:${prefix}${n.id}->${agentName}`,
                    source: `${prefix}${n.id}`,
                    sourceHandle: "right",
                    target: agentName,
                    type: "dependency",
                    animated: false,
                });
            }
        }
    }
}
