// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronUp, Wrench } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { AgentToolsetConfigDialog } from "@/features/agents/AgentToolsetConfigDialog";

import { useDescriptionTruncation } from "@/hooks";
import type { ToolsetFile } from "@/types";

interface ToolsetToolCardProps {
    file: ToolsetFile;
    configValues?: Record<string, unknown>;
    onConfigSave?: (toolName: string, values: Record<string, unknown>) => void;
}

const ToolsetToolCard: React.FC<ToolsetToolCardProps> = ({ file, configValues, onConfigSave }) => {
    const { isExpanded, isTruncated, descriptionRef, toggleExpanded } = useDescriptionTruncation(file.toolDescription ?? "", true);
    const hasConfig = file.configSchema && file.configSchema.length > 0;

    return (
        <div data-testid={`toolCard-${file.toolName}`} className={cn("flex items-center gap-2 rounded-sm border p-4")}>
            <div className="flex-shrink-0">
                <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
                    <Wrench className="size-5" />
                </div>
            </div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-center justify-between gap-2">
                    <div className="truncate font-medium" title={file.toolName}>
                        {file.toolName}
                    </div>
                    {isTruncated && (
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
                <div ref={descriptionRef} className={cn("text-small mt-1 text-(--secondary-text-wMain)", !isExpanded && "truncate")}>
                    {file.toolDescription ?? "—"}
                </div>
            </div>
            {hasConfig && onConfigSave && (
                <div className="flex-shrink-0">
                    <AgentToolsetConfigDialog toolName={file.toolName} configSchema={file.configSchema!} configValues={configValues ?? {}} onSave={onConfigSave} isToolsetDefault />
                </div>
            )}
        </div>
    );
};

interface ToolsetToolCardsProps {
    files: ToolsetFile[];
    discoveryStatus?: string;
    configValues?: Record<string, Record<string, unknown>>;
    onConfigSave?: (toolName: string, values: Record<string, unknown>) => void;
}

export const ToolsetToolCards: React.FC<ToolsetToolCardsProps> = ({ files, discoveryStatus, configValues, onConfigSave }) => {
    if (files.length === 0) {
        if (discoveryStatus === "failed") {
            return <div className="text-sm text-(--secondary-text-wMain)">Discovery failed — no tools available. Check the errors above for details.</div>;
        }
        return <div className="text-sm text-(--secondary-text-wMain)">No tools discovered yet</div>;
    }

    return (
        <div className="flex flex-col gap-4">
            {files.map(file => (
                <ToolsetToolCard key={file.id} file={file} configValues={configValues?.[file.toolName]} onConfigSave={onConfigSave} />
            ))}
        </div>
    );
};
