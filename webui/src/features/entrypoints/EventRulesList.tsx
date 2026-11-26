// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Plus } from "lucide-react";

import { EmptySection, PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";
import type { EventRule } from "@/types/models";

import { ConfirmationDialog } from "@/common";

import { EventRuleCard } from "./EventRuleCard";
import { EventRuleDialog } from "./EventRuleDialog";
import { useAgentOptions } from "./hooks";

interface EventRulesListProps {
    value: EventRule[];
    onChange: (rules: EventRule[]) => void;
}

export const EventRulesList = ({ value = [], onChange }: EventRulesListProps) => {
    const agents = useAgentOptions();
    const [isModalOpen, setIsModalOpen] = useState(false);
    const [editingRule, setEditingRule] = useState<EventRule | null>(null);
    const [editingIndex, setEditingIndex] = useState<number | null>(null);
    const [originalRuleName, setOriginalRuleName] = useState<string | undefined>(undefined);
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [deleteIndex, setDeleteIndex] = useState<number | null>(null);

    const handleAdd = () => {
        setEditingRule(null);
        setEditingIndex(null);
        setOriginalRuleName(undefined);
        setIsModalOpen(true);
    };

    const handleEdit = (rule: EventRule, index: number) => {
        setEditingRule({ ...rule });
        setEditingIndex(index);
        setOriginalRuleName(undefined);
        setIsModalOpen(true);
    };

    const handleClone = (rule: EventRule) => {
        const clonedRule = {
            ...JSON.parse(JSON.stringify(rule)),
        };
        setEditingRule(clonedRule);
        setEditingIndex(null);
        setOriginalRuleName(rule.name);
        setIsModalOpen(true);
    };

    const handleDeleteClick = (index: number) => {
        setDeleteIndex(index);
        setDeleteConfirmOpen(true);
    };

    const handleDeleteConfirm = () => {
        if (deleteIndex !== null) {
            const newRules = [...value];
            newRules.splice(deleteIndex, 1);
            onChange(newRules);
        }
        setDeleteConfirmOpen(false);
        setDeleteIndex(null);
    };

    const handleClose = () => {
        setIsModalOpen(false);
    };

    const handleSave = (rule: EventRule) => {
        if (editingIndex !== null) {
            const newRules = [...value];
            newRules[editingIndex] = rule;
            onChange(newRules);
        } else {
            onChange([rule, ...value]);
        }

        handleClose();
    };

    const existingNames = value.map(r => r.name).filter((_, i) => i !== editingIndex);

    return (
        <div className="pt-4">
            <div className="flex items-center justify-between py-1">
                <PageLabel required>Event Rules</PageLabel>
                <Button variant="ghost" size="sm" onClick={handleAdd} type="button">
                    <Plus /> Add Rule
                </Button>
            </div>
            <div className="py-2 text-(--secondary-text-wMain)">Event rules define how agents process incoming events and where they publish responses.</div>

            {value.length === 0 ? (
                <EmptySection message="No event rules configured" />
            ) : (
                <div>
                    {value.map((rule, index) => (
                        <EventRuleCard key={`${rule.name}-${index}`} rule={rule} onEdit={() => handleEdit(rule, index)} onClone={() => handleClone(rule)} onDelete={() => handleDeleteClick(index)} />
                    ))}
                </div>
            )}

            <EventRuleDialog open={isModalOpen} onClose={handleClose} onSave={handleSave} initialRule={editingRule} agents={agents} existingNames={existingNames} originalRuleName={originalRuleName} />
            <ConfirmationDialog
                open={deleteConfirmOpen}
                title="Delete Event Rule"
                content={deleteIndex !== null && value[deleteIndex] ? `Are you sure you want to delete the rule "${value[deleteIndex].name}"?` : "Are you sure you want to delete this rule?"}
                onConfirm={handleDeleteConfirm}
                onCancel={() => setDeleteConfirmOpen(false)}
                onOpenChange={open => !open && setDeleteConfirmOpen(false)}
            />
        </div>
    );
};
