// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useState } from "react";
import { BookOpen, Bot, Globe, Pencil, Plug, Workflow, X } from "lucide-react";

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

function buildChangeSummary(original: BuildManifest, edited: BuildManifest): string {
    const changes: string[] = [];
    if (edited.name !== original.name) {
        changes.push(`Renamed project to "${edited.name}"`);
    }
    if (edited.description !== original.description) {
        changes.push(`Updated description`);
    }
    const originalNames = new Set(original.components?.map(c => c.name) || []);
    const editedNames = new Set(edited.components?.map(c => c.name) || []);
    for (const n of originalNames) {
        if (n && !editedNames.has(n)) {
            changes.push(`Removed component "${n}"`);
        }
    }
    return changes.length > 0 ? `I'd like to make these changes to the plan:\n${changes.map(c => `- ${c}`).join("\n")}` : "I've reviewed the plan and it looks good.";
}

export interface BuildPlanEditFormProps {
    originalManifest: BuildManifest;
    initialEditedManifest: BuildManifest;
    onSubmit: (changeSummary: string) => void;
    onCancel: () => void;
}

export const BuildPlanEditForm: React.FC<BuildPlanEditFormProps> = ({ originalManifest, initialEditedManifest, onSubmit, onCancel }) => {
    const [editedManifest, setEditedManifest] = useState<BuildManifest>(initialEditedManifest);

    const handleRemoveComponent = useCallback((index: number) => {
        setEditedManifest(prev => {
            if (!prev.components) return prev;
            const components = [...prev.components];
            components.splice(index, 1);
            return { ...prev, components };
        });
    }, []);

    const handleSave = useCallback(() => {
        onSubmit(buildChangeSummary(originalManifest, editedManifest));
    }, [editedManifest, originalManifest, onSubmit]);

    return (
        <div className="flex w-[480px] max-w-full flex-col gap-3 rounded-lg border px-5 py-4" style={{ backgroundColor: "var(--background-w10)", borderColor: "var(--secondary-w8040)" }}>
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <Pencil className="h-4 w-4" />
                    <span className="text-sm font-semibold">Edit Action Plan</span>
                </div>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0"
                    onClick={e => {
                        stopPropagation(e);
                        onCancel();
                    }}
                >
                    <X className="h-3.5 w-3.5" />
                </Button>
            </div>

            <div>
                <label className="mb-1 block text-xs font-medium">Project Name</label>
                <input
                    type="text"
                    className="w-full rounded-md border bg-transparent px-3 py-1.5 text-sm focus:ring-1 focus:ring-(--primary-wMain) focus:outline-none"
                    value={editedManifest.name || ""}
                    onChange={e => setEditedManifest(prev => ({ ...prev, name: e.target.value }))}
                />
            </div>

            <div>
                <label className="mb-1 block text-xs font-medium">Description</label>
                <textarea
                    className="w-full resize-none rounded-md border bg-transparent px-3 py-1.5 text-sm focus:ring-1 focus:ring-(--primary-wMain) focus:outline-none"
                    rows={3}
                    value={editedManifest.description || ""}
                    onChange={e => setEditedManifest(prev => ({ ...prev, description: e.target.value }))}
                />
            </div>

            {editedManifest.components && editedManifest.components.length > 0 && (
                <div>
                    <label className="mb-2 block text-xs font-medium">Components</label>
                    <div className="flex flex-col gap-2">
                        {editedManifest.components.map((comp: BuildManifestComponent, idx) => {
                            const Icon = COMPONENT_ICON_MAP[comp.type || "agent"] || Bot;
                            return (
                                <div key={idx} className="flex items-center gap-2 rounded-md border px-3 py-2">
                                    <Icon className="h-4 w-4 flex-shrink-0" />
                                    <span className="min-w-0 flex-1 truncate text-sm">{comp.name || "Unnamed"}</span>
                                    <span className="text-xs">{COMPONENT_TYPE_LABELS[comp.type || "agent"]}</span>
                                    <button
                                        type="button"
                                        className="ml-1 flex-shrink-0 transition-colors"
                                        onClick={e => {
                                            stopPropagation(e);
                                            handleRemoveComponent(idx);
                                        }}
                                        title="Remove component"
                                    >
                                        <X className="h-3.5 w-3.5" />
                                    </button>
                                </div>
                            );
                        })}
                    </div>
                </div>
            )}

            <div className="flex items-center justify-end gap-2 pt-1">
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={e => {
                        stopPropagation(e);
                        onCancel();
                    }}
                >
                    Cancel
                </Button>
                <Button
                    type="button"
                    variant="default"
                    size="sm"
                    onClick={e => {
                        stopPropagation(e);
                        handleSave();
                    }}
                >
                    Submit Changes
                </Button>
            </div>
        </div>
    );
};
