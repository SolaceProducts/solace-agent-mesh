// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { FileText, Wrench } from "lucide-react";

import { useSkillFiles } from "@/api/skills";

import { PageSection } from "@/common/layout";

import { formatBytes } from "@/utils/format";

import { DiscoveryIndicator } from "@/common";
import type { Skill, SkillFileRef } from "@/types";

import { SkillFileCard } from "./SkillFileCard";
import { SkillFilePreview } from "./SkillFilePreview";
import { bundledToolName } from "./skillResources";

interface SkillDetailsViewProps {
    skill: Skill;
}

interface SelectedFile {
    path: string;
    title: string;
}

const sectionHeader = (label: string) => <div className="mb-2 text-(--secondary-text-wMain)">{label}</div>;

export const SkillDetailsView: React.FC<SkillDetailsViewProps> = ({ skill }) => {
    const { data: files, isLoading: filesLoading } = useSkillFiles(skill.id);
    const [selected, setSelected] = useState<SelectedFile | null>(null);

    const references = files?.references ?? [];
    const assets = files?.assets ?? [];
    const tools = skill.bundledTools ?? [];

    const renderFileCards = (items: SkillFileRef[], prefix: "references" | "assets", emptyLabel: string) => {
        if (filesLoading) {
            return <div className="text-sm text-(--secondary-text-wMain)">Loading files…</div>;
        }
        if (items.length === 0) {
            return <div className="text-sm text-(--secondary-text-wMain)">{emptyLabel}</div>;
        }
        return (
            <div className="flex flex-col gap-4">
                {items.map(item => {
                    const fullPath = `${prefix}/${item.path}`;
                    return <SkillFileCard key={fullPath} icon={FileText} title={item.path} subtitle={formatBytes(item.sizeBytes)} onClick={() => setSelected({ path: fullPath, title: item.path })} isActive={selected?.path === fullPath} />;
                })}
            </div>
        );
    };

    return (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden sm:flex-row">
            <div className="flex-1 overflow-y-auto p-4 leading-5 sm:p-8">
                <div className="flex flex-col gap-8">
                    <PageSection>
                        {sectionHeader("Description")}
                        <div>{skill.description || "No description provided"}</div>
                    </PageSection>

                    <PageSection>
                        {sectionHeader("Status")}
                        <DiscoveryIndicator status={skill.discoveryStatus} showReady testIdPrefix="skillStatus" inProgressLabel="Processing" />
                    </PageSection>

                    <PageSection>
                        {sectionHeader("Reference")}
                        {renderFileCards(references, "references", "No references bundled")}
                    </PageSection>

                    <PageSection>
                        {sectionHeader("Tools")}
                        {tools.length === 0 ? (
                            <div className="text-sm text-(--secondary-text-wMain)">No tools bundled</div>
                        ) : (
                            <div className="flex flex-col gap-4">
                                {tools.map((tool, index) => (
                                    <SkillFileCard key={`${bundledToolName(tool) || "tool"}-${index}`} icon={Wrench} title={bundledToolName(tool) || JSON.stringify(tool)} />
                                ))}
                            </div>
                        )}
                    </PageSection>

                    <PageSection>
                        {sectionHeader("Assets")}
                        {renderFileCards(assets, "assets", "No assets bundled")}
                    </PageSection>
                </div>
            </div>

            {selected && (
                <div className="w-full sm:w-2/5 sm:min-w-[360px]">
                    <SkillFilePreview skillId={skill.id} path={selected.path} title={selected.title} onClose={() => setSelected(null)} />
                </div>
            )}
        </div>
    );
};
