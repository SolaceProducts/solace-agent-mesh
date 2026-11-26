// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronRight, FileText, Paperclip, Wrench, X } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { AgentUsageCard, Metadata } from "@/common";
import type { Skill } from "@/types";

import { skillAssetCount, skillReferenceCount, skillToolCount, skillToolNames } from "./skillResources";

export const SKILL_TOOLS_ANCHOR = "skill-tools";

const MAX_PANEL_TOOLS = 6;

interface SkillSidePanelProps {
    skill: Skill;
    onClose?: () => void;
    onOpen?: (anchor?: string) => void;
}

const ToolsResourcesSection = ({ skill, onOpen }: { skill: Skill; onOpen?: (anchor?: string) => void }) => {
    const toolCount = skillToolCount(skill);
    const referenceCount = skillReferenceCount(skill);
    const assetCount = skillAssetCount(skill);
    if (toolCount === 0 && referenceCount === 0 && assetCount === 0) {
        return null;
    }

    const toolNames = skillToolNames(skill);
    const shownTools = toolNames.slice(0, MAX_PANEL_TOOLS);
    const overflow = toolCount - shownTools.length;
    const clickable = !!onOpen;

    const body = (
        <>
            <div className="flex items-center justify-between">
                <div className="text-base font-semibold">Tools &amp; Resources</div>
                {clickable && <ChevronRight className="size-4 text-(--secondary-text-wMain)" />}
            </div>
            <div className="flex flex-col gap-1.5 text-sm">
                <div className="flex items-center gap-2">
                    <Wrench className="size-4 shrink-0 text-(--secondary-text-wMain)" />
                    <span className="font-medium">{toolCount}</span>
                    <span className="text-(--secondary-text-wMain)">{toolCount === 1 ? "tool" : "tools"}</span>
                </div>
                <div className="flex items-center gap-2">
                    <FileText className="size-4 shrink-0 text-(--secondary-text-wMain)" />
                    <span className="font-medium">{referenceCount}</span>
                    <span className="text-(--secondary-text-wMain)">{referenceCount === 1 ? "reference" : "references"}</span>
                </div>
                <div className="flex items-center gap-2">
                    <Paperclip className="size-4 shrink-0 text-(--secondary-text-wMain)" />
                    <span className="font-medium">{assetCount}</span>
                    <span className="text-(--secondary-text-wMain)">{assetCount === 1 ? "asset" : "assets"}</span>
                </div>
            </div>
            {shownTools.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                    {shownTools.map((name, i) => (
                        <span key={`${name}-${i}`} className="rounded-full bg-(--background-w20) px-2 py-0.5 text-xs">
                            {name}
                        </span>
                    ))}
                    {overflow > 0 && <span className="rounded-full bg-(--background-w20) px-2 py-0.5 text-xs text-(--secondary-text-wMain)">+{overflow} more</span>}
                </div>
            )}
        </>
    );

    const baseClassName = "mb-4 flex flex-col gap-3 rounded-sm bg-(--secondary-w10) p-4";
    if (!clickable) {
        return (
            <div className={baseClassName} data-testid="skillToolsResources">
                {body}
            </div>
        );
    }
    return (
        <button type="button" onClick={() => onOpen?.(SKILL_TOOLS_ANCHOR)} className={cn(baseClassName, "w-full cursor-pointer text-left hover:bg-(--secondary-w20)")} data-testid="skillToolsResources">
            {body}
        </button>
    );
};

export const SkillSidePanel: React.FC<SkillSidePanelProps> = ({ skill, onClose, onOpen }) => {
    return skill ? (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between border-b px-4">
                <div className="truncate text-xl font-semibold" title={skill.name}>
                    {skill.name}
                </div>
                <Button variant="ghost" tooltip="Close" onClick={onClose} data-testid="skillSidePanelCloseButton">
                    <X />
                </Button>
            </div>

            <div className="flex-1 overflow-y-auto p-4">
                <div className="mb-4 flex flex-col gap-4 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="text-base font-semibold">Skill Details</div>
                    <div className="break-word">{skill.description || "No description provided"}</div>
                    {onOpen && (
                        <Button variant="outline" onClick={() => onOpen()} data-testid="openSkillButton">
                            Open Skill
                        </Button>
                    )}
                </div>

                <ToolsResourcesSection skill={skill} onOpen={onOpen} />

                <div className="mt-4 py-4 font-semibold">Agent Usage ({skill.agents?.length ?? 0})</div>

                <div className="flex flex-col gap-2">
                    {(skill.agents ?? []).length > 0 ? (
                        (skill.agents ?? []).map(agent => <AgentUsageCard key={agent.id} agentId={agent.id} agentName={agent.name} agentDescription={agent.description} />)
                    ) : (
                        <div className="flex items-center justify-center rounded-sm bg-(--background-w20) p-4 text-center text-sm text-(--secondary-text-wMain)">No agents are currently using this skill</div>
                    )}
                </div>
            </div>

            {skill?.id ? (
                <div className="p-4">
                    <Metadata metadata={{ id: skill.id, createdBy: skill.createdBy, createdAt: skill.createdAt, updatedBy: skill.updatedBy, updatedAt: skill.updatedAt }} />
                </div>
            ) : null}
        </div>
    ) : null;
};
