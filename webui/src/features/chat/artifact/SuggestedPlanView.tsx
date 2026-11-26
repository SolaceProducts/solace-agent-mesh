// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { BookOpen, Bot, Circle, Globe, Pencil, Plug, Workflow } from "lucide-react";

import { Button } from "@/ui";

import type { BuildManifest, BuildManifestComponent } from "./useBuildPlanArtifact";

const COMPONENT_ICON_MAP: Record<string, typeof Bot> = {
    workflow: Workflow,
    connector: Plug,
    gateway: Globe,
    skill: BookOpen,
    agent: Bot,
};

const COMPONENT_TYPE_LABELS: Record<string, string> = {
    gateway: "Entrypoint",
    workflow: "Workflow",
    agent: "Agent",
    skill: "Skill",
    connector: "Connector",
};

function stopPropagation(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
}

export interface SuggestedPlanViewProps {
    manifest: BuildManifest | null;
    sortedComponents: BuildManifestComponent[];
    hideActions: boolean;
    onEdit: () => void;
    onKeepEditing: () => void;
    onBuildActivate: () => void;
}

export const SuggestedPlanView: React.FC<SuggestedPlanViewProps> = ({ manifest, sortedComponents, hideActions, onEdit, onKeepEditing, onBuildActivate }) => {
    return (
        <div className="flex w-[480px] max-w-full flex-col gap-3 rounded-lg border px-5 py-4" style={{ backgroundColor: "var(--background-w10)", borderColor: "var(--secondary-w8040)" }}>
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2.5">
                    <Circle className="h-5 w-5" />
                    <span className="text-sm font-semibold">Build Plan</span>
                </div>
                {!hideActions && (
                    <button
                        type="button"
                        className="flex items-center gap-1 text-sm font-medium text-(--primary-wMain) transition-colors hover:opacity-80"
                        onClick={e => {
                            stopPropagation(e);
                            onEdit();
                        }}
                    >
                        <Pencil className="h-3.5 w-3.5" />
                        Edit Plan
                    </button>
                )}
            </div>

            {manifest?.description && <p className="text-sm leading-relaxed">{manifest.description}</p>}

            {sortedComponents.length > 0 && (
                <div>
                    <h4 className="mb-2 text-xs font-semibold tracking-wide uppercase">Components</h4>
                    <div className="flex flex-col gap-2">
                        {sortedComponents.map((comp, idx) => {
                            const Icon = COMPONENT_ICON_MAP[comp.type || "agent"] || Bot;
                            return (
                                <div key={idx} className="flex items-center gap-3 rounded-md border px-3 py-2.5" style={{ borderColor: "var(--secondary-w8040)" }}>
                                    <Circle className="h-4 w-4 flex-shrink-0 opacity-50" />
                                    <div className="min-w-0 flex-1">
                                        <div className="flex items-center gap-2">
                                            <Icon className="h-3.5 w-3.5 flex-shrink-0" />
                                            <span className="truncate text-sm font-medium">{comp.name || "Unnamed"}</span>
                                        </div>
                                        {comp.type && (
                                            <span className="text-xs">
                                                {COMPONENT_TYPE_LABELS[comp.type] || comp.type}
                                                {comp.connector_type && ` · ${comp.connector_type}`}
                                                {comp.gateway_type && ` · ${comp.gateway_type}`}
                                            </span>
                                        )}
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </div>
            )}

            {!hideActions && (
                <div className="flex items-center justify-end gap-3 pt-1">
                    <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="text-sm font-medium"
                        onClick={e => {
                            stopPropagation(e);
                            onKeepEditing();
                        }}
                    >
                        Keep Editing
                    </Button>
                    <Button
                        type="button"
                        variant="default"
                        size="sm"
                        onClick={e => {
                            stopPropagation(e);
                            onBuildActivate();
                        }}
                    >
                        Build &amp; Activate
                    </Button>
                </div>
            )}
        </div>
    );
};
