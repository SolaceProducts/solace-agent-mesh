// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { useNavigate } from "react-router-dom";

import { AgentUsageList } from "@/common";
import type { Skill } from "@/types";

interface SkillAgentUsageProps {
    skill: Skill;
}

export const SkillAgentUsage: React.FC<SkillAgentUsageProps> = ({ skill }) => {
    const navigate = useNavigate();

    return <AgentUsageList agents={skill.agents ?? []} emptyTitle="No agents use this skill" onGoToAgents={() => navigate("/agents")} />;
};
