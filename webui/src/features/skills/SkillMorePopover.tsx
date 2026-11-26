// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Pencil, Trash2 } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { skillKeys, skillsApi } from "@/api/skills";

import { useUserContext } from "@/providers/contexts";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { ConfirmationDialog } from "@/common";
import type { Skill } from "@/types";

interface SkillMorePopoverProps {
    skill: Skill;
    children: React.ReactNode;
}

export const SkillMorePopover: React.FC<SkillMorePopoverProps> = ({ skill, children }) => {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    const handleDelete = async () => {
        setDeleteError(null);
        try {
            await skillsApi.delete(skill.id);
            await queryClient.invalidateQueries({ queryKey: skillKeys.lists() });
            setConfirmDeleteOpen(false);
        } catch (err) {
            setDeleteError(err instanceof Error ? err.message : "Delete failed");
        }
    };

    const menuActions: MenuAction[] = [];
    if (userHasCapability(CAPABILITIES.updateSkills)) {
        menuActions.push({
            id: "edit",
            label: "Re-upload",
            onClick: () => navigate(`/skills/${skill.id}/upload`),
            icon: <Pencil />,
            iconPosition: "left",
        });
    }
    if (userHasCapability(CAPABILITIES.deleteSkills)) {
        menuActions.push({
            id: "delete",
            label: "Delete",
            onClick: () => setConfirmDeleteOpen(true),
            icon: <Trash2 />,
            iconPosition: "left",
            divider: true,
        });
    }

    if (menuActions.length === 0) return null;

    return (
        <>
            <Popover>
                <PopoverTrigger data-testid="skillMorePopoverTrigger" onClick={e => e.stopPropagation()} asChild>
                    {children}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>
            <ConfirmationDialog
                open={confirmDeleteOpen}
                title={`Delete ${skill.name}`}
                content={
                    <div>
                        {(skill.agents?.length ?? 0) > 0 && (
                            <p className="mb-2">
                                This skill is used by <span className="font-semibold">{skill.agents?.length} agent(s)</span>. Deleting it will remove the skill from those agents and trigger a redeployment.
                            </p>
                        )}
                        <p>
                            The skill <span className="font-semibold">{skill.name}</span> and all its associated data will be permanently deleted.
                        </p>
                        {deleteError && <p className="mt-2 text-sm text-(--error-wMain)">{deleteError}</p>}
                    </div>
                }
                onOpenChange={setConfirmDeleteOpen}
                onCancel={() => setConfirmDeleteOpen(false)}
                onConfirm={handleDelete}
                actionLabels={{ confirm: "Delete" }}
            />
        </>
    );
};
