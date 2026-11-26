// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { Upload } from "lucide-react";
import { useLoaderData, useNavigate } from "react-router-dom";

import { useSkill } from "@/api/skills";

import { useUserContext } from "@/providers/contexts";

import { PageFooter } from "@/common/layout";

import { Button } from "@/ui/button";

import { EmptyState, Header, MessageBanner, Metadata } from "@/common";

import { SkillAgentUsage } from "./SkillAgentUsage";
import { SkillDetailsView } from "./SkillDetailsView";

export const SkillViewPage: React.FC = () => {
    const { skillId } = useLoaderData() as { skillId: string };
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: skill, isLoading, error } = useSkill(skillId);

    const [currentTab, setCurrentTab] = useState<"details" | "agentUsage">("details");

    const tabs = useMemo(
        () => [
            { id: "details", label: "Details", isActive: currentTab === "details", onClick: () => setCurrentTab("details") },
            { id: "agentUsage", label: `Agent Usage (${skill?.agents?.length ?? 0})`, isActive: currentTab === "agentUsage", onClick: () => setCurrentTab("agentUsage") },
        ],
        [currentTab, skill]
    );

    if (isLoading) {
        return <EmptyState variant="loading" title="Loading Skill..." />;
    }

    if (error) {
        return (
            <div className="flex h-96 items-center justify-center">
                <MessageBanner variant="error" message={`Error loading skill. ${error instanceof Error ? error.message : String(error)}`} />
            </div>
        );
    }

    if (!skill) {
        return <EmptyState variant="noImage" title="Skill not found" />;
    }

    const headerButtons =
        currentTab === "details" && userHasCapability(CAPABILITIES.updateSkills)
            ? [
                  <Button key="reupload" variant="ghost" onClick={() => navigate(`/skills/${skillId}/upload`)} title="Re-upload Skill">
                      <Upload />
                      Re-upload Skill
                  </Button>,
              ]
            : undefined;

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <Header title={skill.name} breadcrumbs={[{ label: "Skills", onClick: () => navigate("/skills") }, { label: skill.name }]} buttons={headerButtons} tabs={tabs} />

            {currentTab === "details" ? (
                <>
                    <SkillDetailsView skill={skill} />
                    <div className="px-4 py-4 sm:px-8">
                        <Metadata metadata={{ id: skill.id, createdBy: skill.createdBy, createdAt: skill.createdAt, updatedBy: skill.updatedBy, updatedAt: skill.updatedAt }} />
                    </div>
                </>
            ) : (
                <SkillAgentUsage skill={skill} />
            )}

            <PageFooter>
                <Button variant="outline" onClick={() => navigate("/skills")}>
                    Close
                </Button>
            </PageFooter>
        </div>
    );
};
