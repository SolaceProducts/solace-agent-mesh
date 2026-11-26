// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCcw } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { Header } from "@/common";

import { SkillsView } from "./SkillsView";

export function SkillsPage() {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const userCanCreateSkills = userHasCapability(CAPABILITIES.createSkills);

    const buttons = [];
    if (userCanCreateSkills) {
        buttons.push(
            <Button key="add-skill" variant="outline" onClick={() => navigate("/skills/upload")}>
                <Plus />
                Create Skill
            </Button>
        );
    }
    buttons.push(
        <Button
            key="refresh-skills"
            variant="ghost"
            title="Refresh Skills"
            onClick={async () => {
                await queryClient.invalidateQueries({ queryKey: ["skills"] });
                await queryClient.refetchQueries({ queryKey: ["skills"], type: "active" });
                window.dispatchEvent(new CustomEvent("skills-refetch"));
            }}
        >
            <RefreshCcw />
            Refresh
        </Button>
    );

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-0 flex-col">
                <Header title="Skills" buttons={buttons} />
                <div className="relative min-h-0 flex-1">
                    <SkillsView />
                </div>
            </div>
        </div>
    );
}
