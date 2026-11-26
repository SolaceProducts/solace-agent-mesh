// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Controls, type Edge, type Node, Panel, ReactFlow, useEdgesState, useNodesState, useReactFlow } from "@xyflow/react";

import { PopoverManual } from "@/ui/popoverManual";
import { type AgentCardInfoFE } from "@/types/agent";
import { type AgentNodeData, type CommunicationEdgeData } from "@/types/graph";

import { AgentCommEdge } from "./AgentCommEdge";
import { AgentCommNode } from "./AgentCommNode";
import { AgentMeshPopoverContent } from "./AgentMeshPopoverContent";
import { analyzeAgentCommunication } from "./communicationAnalysis";

const nodeTypes = {
    agentCommNode: AgentCommNode,
};

const edgeTypes = {
    commEdge: AgentCommEdge,
};

const POPOVER_OFFSET = { x: 16, y: 32 };

const AgentMeshFlowRenderer: React.FC<{
    agents: AgentCardInfoFE[];
    currentLayoutProp?: string;
}> = ({ agents, currentLayoutProp }) => {
    const [nodes, setNodes, onNodesChange] = useNodesState<Node<AgentNodeData>>([]);
    const [edges, setEdges, onEdgesChange] = useEdgesState<Edge<CommunicationEdgeData>>([]);
    const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
    const [isPopoverOpen, setIsPopoverOpen] = useState(false);
    const [popoverNode, setPopoverNode] = useState<Node<AgentNodeData> | null>(null);
    const [isDragging, setIsDragging] = useState(false);
    const popoverAnchorRef = useRef<HTMLDivElement>(null);
    const { fitView } = useReactFlow();

    const flowData = useMemo(() => {
        return analyzeAgentCommunication(agents, currentLayoutProp || "hierarchical");
    }, [agents, currentLayoutProp]);

    useEffect(() => {
        setNodes(flowData.nodes.map(n => ({ ...n, style: { ...(n.style || {}), opacity: 1 } })));
        setEdges(flowData.edges.map(e => ({ ...e, style: { ...(e.style || {}), opacity: 0.8 } })));
    }, [flowData, setNodes, setEdges]);

    useEffect(() => {
        if (!selectedAgent) {
            setNodes((nds: Node<AgentNodeData>[]) => nds.map(n => ({ ...n, style: { ...(n.style || {}), opacity: 1 } })));
            setEdges((eds: Edge<CommunicationEdgeData>[]) => eds.map(e => ({ ...e, style: { ...(e.style || {}), opacity: 0.8 } })));
            return;
        }

        const baseEdges = flowData.edges;

        const connectedNodeIds = new Set<string>([selectedAgent]);
        baseEdges.forEach(edge => {
            if (edge.source === selectedAgent) connectedNodeIds.add(edge.target);
            if (edge.target === selectedAgent) connectedNodeIds.add(edge.source);
        });

        setNodes((nds: Node<AgentNodeData>[]) =>
            nds.map(n => ({
                ...n,
                style: { ...(n.style || {}), opacity: connectedNodeIds.has(n.id) ? 1 : 0.3 },
            }))
        );

        setEdges((eds: Edge<CommunicationEdgeData>[]) =>
            eds.map(e => ({
                ...e,
                style: {
                    ...(e.style || {}),
                    opacity: e.source === selectedAgent || e.target === selectedAgent ? 1 : 0.3,
                },
            }))
        );
    }, [selectedAgent, flowData, setNodes, setEdges]);

    useEffect(() => {
        if (flowData.nodes.length > 0 && fitView) {
            const timer = setTimeout(() => {
                fitView({ duration: 300, padding: 0.1 });
            }, 150);
            return () => clearTimeout(timer);
        }
    }, [flowData.nodes.length, currentLayoutProp, fitView]);

    const handleNodeClick = useCallback(
        (_event: React.MouseEvent, node: Node<AgentNodeData>) => {
            if (isDragging) {
                return;
            }

            const agentName = node.data.agentName;
            setSelectedAgent(prevSelected => (prevSelected === agentName ? null : agentName));
            setPopoverNode(node);
            setIsPopoverOpen(true);
        },
        [isDragging]
    );

    const handlePopoverClose = useCallback(() => {
        setIsPopoverOpen(false);
        setPopoverNode(null);
    }, []);

    const handlePaneClick = useCallback(() => {
        setSelectedAgent(null);
        handlePopoverClose();
    }, [handlePopoverClose]);

    const handleNodeDragStart = useCallback(() => {
        setIsDragging(true);
        if (isPopoverOpen) {
            setIsPopoverOpen(false);
        }
    }, [isPopoverOpen]);

    const handleNodeDragStop = useCallback(() => {
        setTimeout(() => {
            setIsDragging(false);
        }, 100);
    }, []);

    if (!agents || agents.length === 0) {
        return <div className={`flex h-full items-center justify-center`}>No agents available to display communication flow.</div>;
    }

    if (flowData.nodes.length === 0 && agents.length > 0) {
        return <div className={`flex h-full items-center justify-center`}>No communication relationships found between agents.</div>;
    }

    return (
        <div style={{ height: "100%", width: "100%" }} className="relative">
            <ReactFlow
                nodes={nodes}
                edges={edges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onNodeClick={handleNodeClick}
                onPaneClick={handlePaneClick}
                onNodeDragStart={handleNodeDragStart}
                onNodeDragStop={handleNodeDragStop}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                fitViewOptions={{ padding: 0.1 }}
                proOptions={{ hideAttribution: true }}
                nodesDraggable={true}
                elementsSelectable={true}
                nodesConnectable={false}
                minZoom={0.2}
            >
                <Controls
                    style={
                        {
                            "--xy-controls-button-background-color": "var(--background-w10)",
                            "--xy-controls-button-background-color-hover": "var(--background-w20)",
                            "--xy-controls-button-color": "var(--primary-text-wMain)",
                            "--xy-controls-button-color-hover": "var(--primary-text-wMain)",
                            "--xy-controls-button-border-color": "var(--secondary-w40)",
                            "--xy-controls-box-shadow": "none",
                            border: "1px solid var(--secondary-w40)",
                        } as React.CSSProperties
                    }
                />

                <Panel position="top-right" className="z-10">
                    <div ref={popoverAnchorRef} />
                </Panel>
            </ReactFlow>

            <PopoverManual isOpen={isPopoverOpen} onClose={handlePopoverClose} anchorRef={popoverAnchorRef} offset={POPOVER_OFFSET} placement="right-start" className="max-w-[350px] min-w-[350px] p-2">
                <AgentMeshPopoverContent agent={agents.find(agent => agent.name === popoverNode?.data.agentName) || null} />
            </PopoverManual>
        </div>
    );
};

export { AgentMeshFlowRenderer };
