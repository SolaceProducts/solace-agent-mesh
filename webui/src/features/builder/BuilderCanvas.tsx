// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "@xyflow/react/dist/style.css";
import "./canvas/canvasStyles.css";

import { type KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DefaultEdgeOptions, EdgeTypes, NodeMouseHandler, NodeTypes, OnNodesChange } from "@xyflow/react";
import { MarkerType, ReactFlow, ReactFlowProvider, useNodesInitialized, useReactFlow } from "@xyflow/react";
import { Scan, Workflow, ZoomIn, ZoomOut } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";
import { useConnectors } from "@/api/connectors";

import { Spinner } from "@/ui/spinner";

import { Button } from "@/ui";

import {
    AgentCardNode,
    CanvasTabs,
    ColumnHeaderNode,
    type ComponentGroup,
    computeCanvasLayout,
    ConnectorCardNode,
    DependencyEdge,
    deriveGroupStatus,
    filterManifestToGroup,
    GatewayCardNode,
    groupComponents,
    HierarchyEdge,
    LoadingCardNode,
    pausedWhenInterrupted,
    RemoteAgentCardNode,
    SkillCardNode,
    WfAgentFlowNode,
    WfConditionPillFlowNode,
    WfDecisionFlowNode,
    WfDiagramEdge,
    WfEndFlowNode,
    WfLoopFlowNode,
    WfMapFlowNode,
    WfStartFlowNode,
    WfSwitchFlowNode,
    WfToolFlowNode,
    WfWorkflowRefFlowNode,
    WorkflowBgNode,
    WorkflowCardNode,
    WorkflowContainerNode,
} from "./canvas";
import { CanvasViewProvider, useCanvasView } from "./canvas/canvasViewContext";
import { cardAccessibleName } from "./canvas/cardLabels";
import { isKeyboardOperableNodeType } from "./canvas/keyboardOperableNodes";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import type { BuilderActiveTab } from "./useBuilderPageState";

export interface CanvasNodeSelection {
    type: string;
    name: string;
}

interface BuilderCanvasProps {
    manifest: BuildManifest | null;
    componentData: Map<string, ComponentCanvasData>;
    selectedNodeId: string | null;
    onNodeSelect: (nodeId: string | null) => void;
    onNodeDoubleClick: (nodeId: string) => void;
    isEmpty: boolean;
    isLoading: boolean;
    isVisible?: boolean;
    hideControls?: boolean;
    builderActiveTab?: BuilderActiveTab;
    testTargetName?: string | null;
    onSelectTestTarget?: (name: string) => void;
    isApplyingChanges?: boolean;
    buildInterrupted?: boolean;
}

export const nodeTypes: NodeTypes = {
    agent: AgentCardNode,
    workflow: WorkflowCardNode,
    gateway: GatewayCardNode,
    connector: ConnectorCardNode,
    skill: SkillCardNode,
    a2a_agent: RemoteAgentCardNode,
    loading: LoadingCardNode,
    "column-header": ColumnHeaderNode,
    "workflow-container": WorkflowContainerNode,
    "workflow-bg": WorkflowBgNode,
    "wf-start": WfStartFlowNode,
    "wf-end": WfEndFlowNode,
    "wf-agent": WfAgentFlowNode,
    "wf-tool": WfToolFlowNode,
    "wf-workflow": WfWorkflowRefFlowNode,
    "wf-switch": WfSwitchFlowNode,
    "wf-decision": WfDecisionFlowNode,
    "wf-map": WfMapFlowNode,
    "wf-loop": WfLoopFlowNode,
    "wf-condition": WfConditionPillFlowNode,
};

const edgeTypes: EdgeTypes = {
    dependency: DependencyEdge,
    hierarchy: HierarchyEdge,
    "workflow-edge": WfDiagramEdge,
};

const NODE_ACTIVATION_KEYS = new Set(["Enter", " "]);

const isKeyboardOperableCard = (type: string | undefined) => !!type && type in nodeTypes && isKeyboardOperableNodeType(type);

const NODE_ACTIVATION_HINT = "Press Enter or Space to open details.";

const CANVAS_ARIA_LABELS = {
    "node.a11yDescription.default": NODE_ACTIVATION_HINT,
    "node.a11yDescription.keyboardDisabled": NODE_ACTIVATION_HINT,
};

const DECORATIVE_EDGE_OPTIONS: DefaultEdgeOptions = { domAttributes: { "aria-hidden": true } };

type BuilderCanvasInnerProps = Omit<BuilderCanvasProps, "builderActiveTab" | "testTargetName" | "onSelectTestTarget" | "isApplyingChanges" | "buildInterrupted">;

function BuilderCanvasInner({ manifest, componentData, selectedNodeId, onNodeSelect, onNodeDoubleClick, isEmpty, isLoading, isVisible = true, hideControls = false }: Readonly<BuilderCanvasInnerProps>) {
    const reactFlowInstance = useReactFlow();
    const { buildInterrupted } = useCanvasView();
    const nodesInitialized = useNodesInitialized();
    const prevNodeCountRef = useRef(0);
    const hasFitRef = useRef(false);

    const groups: ComponentGroup[] = useMemo(() => groupComponents(manifest, componentData), [manifest, componentData]);

    const [activeGroupId, setActiveGroupId] = useState<string | null>(null);
    const userPickedTabRef = useRef(false);
    const lastAutoSwitchedToRef = useRef<string | null>(null);

    const generatingGroupId = useMemo(() => {
        if (!manifest?.components) return null;
        const generatingName = manifest.components.find(c => componentData.get(c.name)?.isGenerating)?.name;
        if (!generatingName) return null;
        const owning = groups.find(g => g.members.has(generatingName));
        return owning?.id ?? null;
    }, [manifest, componentData, groups]);

    useEffect(() => {
        if (groups.length === 0) {
            if (activeGroupId !== null) setActiveGroupId(null);
            return;
        }
        if (!activeGroupId || !groups.some(g => g.id === activeGroupId)) {
            const initial = generatingGroupId ?? groups[0].id;
            if (generatingGroupId) lastAutoSwitchedToRef.current = generatingGroupId;
            setActiveGroupId(initial);
            return;
        }
        if (!userPickedTabRef.current && generatingGroupId && generatingGroupId !== activeGroupId && generatingGroupId !== lastAutoSwitchedToRef.current) {
            lastAutoSwitchedToRef.current = generatingGroupId;
            setActiveGroupId(generatingGroupId);
        }
    }, [groups, activeGroupId, generatingGroupId]);

    const handleTabChange = useCallback((id: string) => {
        userPickedTabRef.current = true;
        setActiveGroupId(id);
    }, []);

    const activeGroup = groups.find(g => g.id === activeGroupId);
    const filteredManifest = useMemo(() => {
        if (!manifest || groups.length < 2 || !activeGroup) return manifest;
        return filterManifestToGroup(manifest, activeGroup.members);
    }, [manifest, groups.length, activeGroup]);

    const layout = useMemo(() => computeCanvasLayout(filteredManifest, componentData), [filteredManifest, componentData]);

    const statusFor = useCallback((g: ComponentGroup) => pausedWhenInterrupted(deriveGroupStatus(g, manifest, componentData), buildInterrupted), [manifest, componentData, buildInterrupted]);

    const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);

    const highlightedEdgeIdsByNode = useMemo(() => {
        const map = new Map<string, Set<string>>();
        const addEdge = (nodeId: string, edgeId: string) => {
            let set = map.get(nodeId);
            if (!set) {
                set = new Set();
                map.set(nodeId, set);
            }
            set.add(edgeId);
        };
        const isPill = (id: string) => id.includes(":__condition_") || id.startsWith("__condition_");
        const edgesAtPill = new Map<string, { in: typeof layout.edges; out: typeof layout.edges }>();
        for (const e of layout.edges) {
            if (isPill(e.target)) {
                let rec = edgesAtPill.get(e.target);
                if (!rec) {
                    rec = { in: [], out: [] };
                    edgesAtPill.set(e.target, rec);
                }
                rec.in.push(e);
            }
            if (isPill(e.source)) {
                let rec = edgesAtPill.get(e.source);
                if (!rec) {
                    rec = { in: [], out: [] };
                    edgesAtPill.set(e.source, rec);
                }
                rec.out.push(e);
            }
        }
        for (const e of layout.edges) {
            addEdge(e.source, e.id);
            addEdge(e.target, e.id);
            if (isPill(e.target)) {
                for (const sibling of edgesAtPill.get(e.target)?.out ?? []) addEdge(e.source, sibling.id);
            }
            if (isPill(e.source)) {
                for (const sibling of edgesAtPill.get(e.source)?.in ?? []) addEdge(e.target, sibling.id);
            }
        }
        return map;
    }, [layout.edges]);

    const highlightedEdgeIds = useMemo(() => (hoveredNodeId ? (highlightedEdgeIdsByNode.get(hoveredNodeId) ?? null) : null), [hoveredNodeId, highlightedEdgeIdsByNode]);

    const nodesWithSelection = useMemo(
        () =>
            layout.nodes.map(n => ({
                ...n,
                selected: n.id === selectedNodeId,
                focusable: isKeyboardOperableCard(n.type),
                ariaLabel: cardAccessibleName(n),
            })),
        [layout.nodes, selectedNodeId]
    );

    const edgesWithSelection = useMemo(() => {
        const mapped = layout.edges.map(e => {
            const highlighted = !!highlightedEdgeIds?.has(e.id);
            if (e.type === "dependency") {
                return { ...e, data: { ...e.data, selectedNodeId, highlighted } };
            }
            if (e.type === "workflow-edge") {
                const markerEnd = highlighted && e.markerEnd ? { type: MarkerType.ArrowClosed, width: 16, height: 16, color: "var(--success-wMain)" } : e.markerEnd;
                return { ...e, markerEnd, data: { ...e.data, highlighted } };
            }
            return e;
        });
        if (!highlightedEdgeIds || highlightedEdgeIds.size === 0) return mapped;
        const normal: typeof mapped = [];
        const top: typeof mapped = [];
        for (const e of mapped) {
            if (highlightedEdgeIds.has(e.id)) top.push(e);
            else normal.push(e);
        }
        return [...normal, ...top];
    }, [layout.edges, selectedNodeId, highlightedEdgeIds]);

    useEffect(() => {
        if (!isVisible) return;
        const nodeCount = layout.nodes.length;
        if (nodeCount === 0) {
            hasFitRef.current = false;
            prevNodeCountRef.current = 0;
            return;
        }
        if (hasFitRef.current && nodeCount === prevNodeCountRef.current) return;
        const doFit = () => {
            hasFitRef.current = true;
            prevNodeCountRef.current = nodeCount;
            reactFlowInstance.fitView({ padding: 0.15, duration: 300, maxZoom: 1.5 });
        };
        if (nodesInitialized) {
            doFit();
            return;
        }
        const t = setTimeout(doFit, 300);
        return () => clearTimeout(t);
    }, [layout.nodes.length, nodesInitialized, reactFlowInstance, isVisible]);

    useEffect(() => {
        if (!activeGroupId || layout.nodes.length === 0 || !isVisible) return;
        const t = setTimeout(() => {
            reactFlowInstance.fitView({ padding: 0.15, duration: 300, maxZoom: 1.5 });
        }, 50);
        return () => clearTimeout(t);
    }, [activeGroupId, reactFlowInstance, isVisible, layout.nodes.length]);

    const wasVisibleRef = useRef(isVisible);
    useEffect(() => {
        const wasVisible = wasVisibleRef.current;
        wasVisibleRef.current = isVisible;
        if (!wasVisible && isVisible && layout.nodes.length > 0) {
            setTimeout(() => {
                reactFlowInstance.fitView({ padding: 0.15, duration: 300, maxZoom: 1.5 });
            }, 100);
        }
    }, [isVisible, layout.nodes.length, reactFlowInstance]);

    const prevSelectedRef = useRef<string | null>(null);
    useEffect(() => {
        if (!selectedNodeId || selectedNodeId === prevSelectedRef.current) {
            prevSelectedRef.current = selectedNodeId;
            return;
        }
        prevSelectedRef.current = selectedNodeId;

        const node = layout.nodes.find(n => n.id === selectedNodeId);
        if (!node) return;

        setTimeout(() => {
            reactFlowInstance.fitView({
                nodes: [{ id: selectedNodeId }],
                duration: 300,
                padding: 0.5,
                maxZoom: 1.2,
            });
        }, 50);
    }, [selectedNodeId, layout.nodes, reactFlowInstance]);

    const activateNode = useCallback(
        (nodeId: string) => {
            if (nodeId.startsWith("__header_")) return;
            onNodeSelect(nodeId);
        },
        [onNodeSelect]
    );

    const handleNodeClick: NodeMouseHandler = useCallback((_event, node) => activateNode(node.id), [activateNode]);

    const handleKeyDown = useCallback(
        (event: KeyboardEvent<HTMLDivElement>) => {
            const target = event.target as HTMLElement;
            if (!NODE_ACTIVATION_KEYS.has(event.key) || !target.classList.contains("react-flow__node") || !target.dataset.id) return;
            event.preventDefault();
            if (!event.repeat) activateNode(target.dataset.id);
        },
        [activateNode]
    );

    const handleNodeDblClick: NodeMouseHandler = useCallback(
        (_event, node) => {
            if (node.id.startsWith("__header_")) return;
            onNodeDoubleClick(node.id);
        },
        [onNodeDoubleClick]
    );

    const handlePaneClick = useCallback(() => {
        onNodeSelect(null);
    }, [onNodeSelect]);

    const handleNodeMouseEnter: NodeMouseHandler = useCallback((_event, node) => {
        setHoveredNodeId(node.id);
    }, []);

    const handleNodeMouseLeave: NodeMouseHandler = useCallback(() => {
        setHoveredNodeId(null);
    }, []);

    const handleNodesChange: OnNodesChange = useCallback(() => {}, []);

    if (isLoading && layout.nodes.length === 0) {
        return (
            <div className="builder-blueprint flex h-full w-full items-center justify-center">
                <Spinner size="medium" variant="primary">
                    <p className="mt-4 text-sm">Loading configuration...</p>
                </Spinner>
            </div>
        );
    }

    if (isEmpty) {
        return (
            <div className="builder-blueprint flex h-full w-full items-center justify-center">
                <div className="text-center">
                    <Workflow className="mx-auto mb-4 h-16 w-16 opacity-40" />
                    <p className="text-lg font-medium">No configuration yet</p>
                    <p className="mt-2 max-w-sm text-sm">Start chatting with the Builder agent to generate agent and workflow configurations. They will appear here as a visual preview.</p>
                </div>
            </div>
        );
    }

    return (
        <div className="builder-canvas flex h-full w-full flex-col" style={{ backgroundColor: "var(--background-w20)" }}>
            {groups.length >= 2 && activeGroupId && <CanvasTabs groups={groups} activeId={activeGroupId} onActiveChange={handleTabChange} statusFor={statusFor} />}
            <div className="relative min-h-0 flex-1">
                {!hideControls && (
                    <div className="absolute top-4 right-4 z-50 flex items-center gap-3 rounded-sm border bg-(--background-w10) px-4 py-2">
                        <Button onClick={() => reactFlowInstance.fitView({ padding: 0.15, duration: 300, maxZoom: 1.5 })} variant="ghost" size="sm" tooltip="Center view">
                            <Scan className="h-4 w-4" />
                        </Button>
                        <div className="h-6 w-px border-l" />
                        <Button onClick={() => reactFlowInstance.zoomOut({ duration: 200 })} variant="ghost" size="sm" tooltip="Zoom out">
                            <ZoomOut className="h-4 w-4" />
                        </Button>
                        <Button onClick={() => reactFlowInstance.zoomIn({ duration: 200 })} variant="ghost" size="sm" tooltip="Zoom in">
                            <ZoomIn className="h-4 w-4" />
                        </Button>
                    </div>
                )}
                <ReactFlow
                    nodes={nodesWithSelection}
                    edges={edgesWithSelection}
                    nodeTypes={nodeTypes}
                    edgeTypes={edgeTypes}
                    onNodeClick={handleNodeClick}
                    onKeyDown={handleKeyDown}
                    onNodeDoubleClick={handleNodeDblClick}
                    onNodeMouseEnter={handleNodeMouseEnter}
                    onNodeMouseLeave={handleNodeMouseLeave}
                    onPaneClick={handlePaneClick}
                    onNodesChange={handleNodesChange}
                    nodesDraggable
                    nodesConnectable={false}
                    elementsSelectable={false}
                    edgesFocusable={false}
                    defaultEdgeOptions={DECORATIVE_EDGE_OPTIONS}
                    ariaLabelConfig={CANVAS_ARIA_LABELS}
                    minZoom={0.15}
                    maxZoom={2}
                    fitView
                    fitViewOptions={{ padding: 0.15 }}
                    proOptions={{ hideAttribution: true }}
                >
                    <svg>
                        <defs>
                            <marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                                <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--secondary-w70)" />
                            </marker>
                        </defs>
                    </svg>
                </ReactFlow>
            </div>
        </div>
    );
}

export function BuilderCanvas({ builderActiveTab = "build", testTargetName = null, onSelectTestTarget = () => {}, isApplyingChanges = false, buildInterrupted = false, ...innerProps }: Readonly<BuilderCanvasProps>) {
    const { data: liveAgentCards } = useAgentCards();
    const { data: liveConnectors } = useConnectors();
    return (
        <CanvasViewProvider
            builderActiveTab={builderActiveTab}
            testTargetName={testTargetName}
            onSelectTestTarget={onSelectTestTarget}
            isApplyingChanges={isApplyingChanges}
            buildInterrupted={buildInterrupted}
            liveAgentCards={liveAgentCards}
            liveConnectors={liveConnectors}
        >
            <ReactFlowProvider>
                <BuilderCanvasInner {...innerProps} />
            </ReactFlowProvider>
        </CanvasViewProvider>
    );
}
