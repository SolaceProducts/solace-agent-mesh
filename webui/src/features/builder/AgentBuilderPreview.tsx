// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Bot, Package, Sparkles, Wrench } from "lucide-react";

import type { CanvasNodeSelection } from "./BuilderCanvas";
import { extractAgentDiagramConfig, humanizeComponentName } from "./builderUtils";

interface AgentBuilderPreviewProps {
    yamlText: string;
    onNodeSelected?: (node: CanvasNodeSelection | null) => void;
    onViewportChange?: (transform: { scale: number; x: number; y: number }) => void;
}

function formatToolType(type: string): string {
    if (type === "sam_remote") return "Remote";
    if (type === "mcp") return "MCP";
    if (type === "builtin") return "Built-in";
    return type;
}

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 2;

export function AgentBuilderPreview({ yamlText, onNodeSelected: _onNodeSelected, onViewportChange }: AgentBuilderPreviewProps) {
    const config = useMemo(() => extractAgentDiagramConfig(yamlText), [yamlText]);
    const containerRef = useRef<HTMLDivElement>(null);
    const [transform, setTransform] = useState({ x: 0, y: 0, scale: 1 });
    const panState = useRef<{ startX: number; startY: number; startTx: number; startTy: number } | null>(null);

    useEffect(() => {
        onViewportChange?.(transform);
    }, [transform, onViewportChange]);

    const handleWheel = useCallback((e: React.WheelEvent) => {
        e.preventDefault();
        const rect = containerRef.current?.getBoundingClientRect();
        if (!rect) return;

        const px = e.clientX - rect.left;
        const py = e.clientY - rect.top;

        if (e.ctrlKey || e.metaKey) {
            const delta = -e.deltaY * 0.01;
            setTransform(prev => {
                const newScale = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, prev.scale * (1 + delta)));
                const ratio = newScale / prev.scale;
                return {
                    scale: newScale,
                    x: px - ratio * (px - prev.x),
                    y: py - ratio * (py - prev.y),
                };
            });
        } else {
            setTransform(prev => ({
                ...prev,
                x: prev.x - e.deltaX,
                y: prev.y - e.deltaY,
            }));
        }
    }, []);

    const handleMouseDown = useCallback(
        (e: React.MouseEvent) => {
            if (e.button !== 0) return;
            panState.current = { startX: e.clientX, startY: e.clientY, startTx: transform.x, startTy: transform.y };

            const handleMouseMove = (moveEvent: MouseEvent) => {
                const ps = panState.current;
                if (!ps) return;
                setTransform(prev => ({
                    ...prev,
                    x: ps.startTx + (moveEvent.clientX - ps.startX),
                    y: ps.startTy + (moveEvent.clientY - ps.startY),
                }));
            };
            const handleMouseUp = () => {
                panState.current = null;
                window.removeEventListener("mousemove", handleMouseMove);
                window.removeEventListener("mouseup", handleMouseUp);
            };
            window.addEventListener("mousemove", handleMouseMove);
            window.addEventListener("mouseup", handleMouseUp);
        },
        [transform.x, transform.y]
    );

    if (!config) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <p className="text-sm">Unable to parse agent configuration.</p>
            </div>
        );
    }

    const individualTools = config.tools?.filter((t: { tool_type: string }) => t.tool_type !== "builtin-group") ?? [];
    const toolsets = config.toolsets ?? [];
    const skills = config.skills ?? [];
    const hasTools = individualTools.length > 0 || toolsets.length > 0;
    const hasSkills = skills.length > 0;

    return (
        <div ref={containerRef} className="h-full w-full cursor-grab overflow-hidden active:cursor-grabbing" onWheel={handleWheel} onMouseDown={handleMouseDown}>
            <div
                className="flex h-full w-full items-start justify-center pt-16"
                style={{
                    transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
                    transformOrigin: "0 0",
                }}
            >
                <div className="w-full max-w-md rounded-xl border bg-(--background-w10) shadow-lg">
                    <div className="border-b px-5 py-4">
                        <div className="flex items-center gap-3">
                            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-(--brand-wMain)/15">
                                <Bot className="h-5 w-5" style={{ color: "var(--brand-wMain)" }} />
                            </div>
                            <div className="min-w-0">
                                <h2 className="truncate text-base font-semibold" title={config.name}>
                                    {humanizeComponentName(config.name)}
                                </h2>
                                <span className="text-xs">Agent</span>
                            </div>
                        </div>
                        {config.description && <p className="mt-3 line-clamp-3 text-sm leading-relaxed">{config.description}</p>}
                    </div>

                    {hasTools && (
                        <div className="border-b px-5 py-3">
                            <h3 className="mb-2 text-xs font-medium tracking-wider uppercase">Tools</h3>
                            <div className="space-y-1">
                                {individualTools.map((tool: { tool_type: string; tool_name?: string }, i: number) => (
                                    <div key={`tool-${i}`} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm">
                                        <Wrench className="h-3.5 w-3.5 shrink-0" style={{ color: "var(--accent-n2-wMain)" }} />
                                        <span className="min-w-0 truncate">{tool.tool_name || "Unnamed tool"}</span>
                                        <span className="ml-auto shrink-0 text-xs">{formatToolType(tool.tool_type)}</span>
                                    </div>
                                ))}
                                {toolsets.map((ts: string, i: number) => (
                                    <div key={`ts-${i}`} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm">
                                        <Package className="h-3.5 w-3.5 shrink-0" style={{ color: "var(--accent-n2-wMain)" }} />
                                        <span className="min-w-0 truncate">{humanizeComponentName(ts)}</span>
                                        <span className="ml-auto shrink-0 text-xs">Group</span>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {hasSkills && (
                        <div className="px-5 py-3">
                            <h3 className="mb-2 text-xs font-medium tracking-wider uppercase">Abilities</h3>
                            <div className="space-y-1">
                                {skills.map((skill: { name: string }, i: number) => (
                                    <div key={`skill-${i}`} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm">
                                        <Sparkles className="h-3.5 w-3.5 shrink-0" style={{ color: "var(--info-wMain)" }} />
                                        <span className="min-w-0 truncate">{skill.name}</span>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {config.inputModes?.length || config.outputModes?.length ? (
                        <div className="border-t px-5 py-3">
                            <h3 className="mb-2 text-xs font-medium tracking-wider uppercase">Communication</h3>
                            <div className="flex flex-wrap gap-1.5">
                                {config.inputModes?.map((mode: string) => (
                                    <span key={`in-${mode}`} className="rounded bg-(--secondary-w10) px-2 py-0.5 text-xs">
                                        ← {mode}
                                    </span>
                                ))}
                                {config.outputModes?.map((mode: string) => (
                                    <span key={`out-${mode}`} className="rounded bg-(--secondary-w10) px-2 py-0.5 text-xs">
                                        → {mode}
                                    </span>
                                ))}
                            </div>
                        </div>
                    ) : null}
                </div>
            </div>
        </div>
    );
}
