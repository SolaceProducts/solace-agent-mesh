// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { agentsApi, invalidateAgentRelations, useAgents } from "@/api/agents";
import { useCustomToolsets } from "@/api/toolsets";

import { useChatContext } from "@/providers/contexts";

import { ErrorLabel } from "@/common/status";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import { SearchableCombobox } from "@/ui/searchable-combobox";
import { Textarea } from "@/ui/textarea";

import type { Agent, Toolset } from "@/types";

interface ToolsetAddToAgentDialogProps {
    isOpen: boolean;
    toolset: Toolset;
    onClose: () => void;
}

export const ToolsetAddToAgentDialog: React.FC<ToolsetAddToAgentDialogProps> = ({ isOpen, toolset, onClose }) => {
    const { addNotification } = useChatContext();
    const { data: userAgents = [], isLoading: agentsLoading } = useAgents();
    const { refetch: customToolsetsRefetch } = useCustomToolsets();
    const queryClient = useQueryClient();

    const [selectedAgentId, setSelectedAgentId] = useState<string>("");
    const [agentInstructions, setAgentInstructions] = useState("");
    const [cachedAgent, setCachedAgent] = useState<Agent | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [errors, setErrors] = useState<{ agentId?: string; instructions?: string; submit?: string }>({});

    const availableAgents = useMemo(() => (userAgents ?? []).filter((a): a is Agent & { id: string } => !!a.id && a.type === "standard"), [userAgents]);

    const agentItems = useMemo(() => {
        const attachedAgentIds = new Set((toolset.agents ?? []).map(a => a.id));
        return availableAgents.filter(a => !attachedAgentIds.has(a.id));
    }, [availableAgents, toolset.agents]);

    const handleAgentSelect = useCallback(async (agentId: string) => {
        setSelectedAgentId(agentId);
        setErrors(e => ({ ...e, agentId: undefined }));
        setCachedAgent(null);
        setAgentInstructions("");
        try {
            const response = await agentsApi.get(agentId);
            setCachedAgent(response);
            setAgentInstructions(response.systemPrompt ?? "");
        } catch {}
    }, []);

    const handleClose = useCallback(() => {
        setSelectedAgentId("");
        setAgentInstructions("");
        setCachedAgent(null);
        setErrors({});
        onClose();
    }, [onClose]);

    const handleSubmit = useCallback(async () => {
        const newErrors: typeof errors = {};
        if (!selectedAgentId) newErrors.agentId = "Please select an agent";
        if (!agentInstructions.trim()) newErrors.instructions = "Agent instructions are required";
        if (Object.keys(newErrors).length > 0) {
            setErrors(newErrors);
            return;
        }

        setIsLoading(true);
        try {
            const currentAgent = cachedAgent ?? (await agentsApi.get(selectedAgentId));

            const currentToolsets = currentAgent.toolsets ?? [];
            const toolsetBindingKey = toolset.type === "builtin" ? toolset.id : toolset.name;
            const updatedToolsets = currentToolsets.includes(toolsetBindingKey) ? currentToolsets : [...currentToolsets, toolsetBindingKey];

            const currentExcluded = currentAgent.toolsetExcludedTools ?? {};
            const updatedExcluded = { ...currentExcluded };
            delete updatedExcluded[toolsetBindingKey];

            const systemPrompt = agentInstructions.trim();

            await agentsApi.update(selectedAgentId, {
                name: currentAgent.name,
                description: currentAgent.description,
                systemPrompt,
                skills: currentAgent.skills,
                skillRefs: (currentAgent as any).skillRefs,
                skillIds: currentAgent.skillIds,
                skillConfigs: currentAgent.skillConfigs,
                toolsets: updatedToolsets,
                toolsetExcludedTools: updatedExcluded,
                toolsetConfigs: currentAgent.toolsetConfigs,
                connectors: currentAgent.connectors,
                inputModes: currentAgent.inputModes,
                outputModes: currentAgent.outputModes,
                modelProvider: currentAgent.modelProvider,
                additionalConfigurations: currentAgent.additionalConfigurations,
                deploy: false,
            });

            invalidateAgentRelations(queryClient);
            await customToolsetsRefetch();
            addNotification("Toolset added to agent successfully", "success");
            handleClose();
        } catch (error) {
            setErrors({ submit: getErrorMessage(error, "Failed to add toolset to agent") });
        } finally {
            setIsLoading(false);
        }
    }, [selectedAgentId, agentInstructions, toolset.id, toolset.name, toolset.type, cachedAgent, addNotification, customToolsetsRefetch, handleClose, queryClient]);

    return (
        <Dialog open={isOpen} onOpenChange={handleClose}>
            <DialogContent className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-2xl" onOpenAutoFocus={e => e.preventDefault()}>
                <DialogHeader>
                    <DialogTitle>Add Toolset to Agent</DialogTitle>
                </DialogHeader>

                <div className="flex flex-col gap-4 overflow-y-auto pt-6 pb-2">
                    <div>
                        <div className="mb-1 text-sm font-semibold">
                            Agent<span className="ml-1 text-(--brand-wMain)">*</span>
                        </div>
                        <SearchableCombobox
                            data-testid="agentSelectTrigger"
                            items={agentItems}
                            value={selectedAgentId}
                            onChange={handleAgentSelect}
                            getId={a => a.id}
                            getSearchText={a => a.name}
                            renderItem={a => <span>{a.name}</span>}
                            renderSelected={a => <span>{a.name}</span>}
                            placeholder=""
                            searchPlaceholder="Search agents..."
                            noMatchText="No agents match your search."
                            disabled={!agentsLoading && agentItems.length === 0}
                        />
                        {errors.agentId && <ErrorLabel message={errors.agentId} />}
                    </div>

                    <div>
                        <div className="mb-1 text-sm font-semibold">
                            Agent Instructions<span className="ml-1 text-(--brand-wMain)">*</span>
                        </div>
                        <p className="mb-2 text-sm">Update the agent instructions to specify how the toolset should be used. Clear instructions help the agent apply the tools accurately and consistently.</p>
                        <Textarea
                            data-testid="agentInstructionsTextarea"
                            value={agentInstructions}
                            onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => {
                                setAgentInstructions(e.target.value);
                                setErrors(err => ({ ...err, instructions: undefined }));
                            }}
                            className={cn("max-h-40 resize-none overflow-y-auto", errors.instructions ? "border-(--error-wMain)" : "")}
                            rows={4}
                        />
                        {errors.instructions && <ErrorLabel message={errors.instructions} />}
                    </div>

                    {errors.submit && <ErrorLabel message={errors.submit} />}
                </div>

                <div className="flex justify-end gap-2 pt-6">
                    <Button data-testid="cancelButton" type="button" variant="ghost" onClick={handleClose} disabled={isLoading}>
                        Cancel
                    </Button>
                    <Button data-testid="addButton" type="button" onClick={handleSubmit} disabled={isLoading}>
                        {isLoading ? "Adding..." : "Add"}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
};
