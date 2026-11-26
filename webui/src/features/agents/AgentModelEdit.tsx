// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Cpu, ExternalLink, Loader2 } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { useChatModelConfigs } from "@/api/models/hooks";
import { type ModelConfig } from "@/api/models/types";

import { getSectionTitle } from "@/common/layout";

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";
import type { Agent } from "@/types";

import { agentModelSchema } from "./agentEditSchemas";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentModelCard } from "./AgentModelCard";
import { AgentEditSection, AgentEditWrapper } from "./common";

type AgentModelFormData = z.infer<typeof agentModelSchema>;

interface AgentModelEditProps {
    agent: Agent;
    onApply: (agent: Agent) => void;
    onDiscard: () => void;
}

export const AgentModelEdit = ({ agent, onApply, onDiscard }: AgentModelEditProps) => {
    const { data: models = [], isLoading, error, refetch } = useChatModelConfigs();

    const modelRef = agent.modelProvider?.[0] ?? "";
    const resolvedModel = useMemo(() => (modelRef ? (models.find(m => m.id === modelRef || m.alias === modelRef) ?? null) : null), [models, modelRef]);
    const currentModelExists = !!resolvedModel;

    const { handleSubmit, watch, setValue } = useForm<AgentModelFormData>({
        resolver: zodResolver(agentModelSchema),
        values: {
            selectedModel: resolvedModel?.id ?? "",
        },
        mode: "onSubmit",
    });

    const selectedModel = watch("selectedModel");

    useEffect(() => {
        if (selectedModel && models.length > 0 && !models.some(m => m.id === selectedModel)) {
            setValue("selectedModel", "", { shouldValidate: true });
        }
    }, [models, selectedModel, setValue]);

    const onSubmit = (data: AgentModelFormData) => {
        onApply({ ...agent, modelProvider: [data.selectedModel] });
    };

    const sortedModels = useMemo(() => [...models].sort((a: ModelConfig, b: ModelConfig) => a.alias.localeCompare(b.alias)), [models]);

    if (isLoading) {
        return (
            <div className="flex flex-1 items-center justify-center">
                <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
            </div>
        );
    }

    if (error) {
        return (
            <AgentEditWrapper sectionTitle={getSectionTitle(Cpu, "Model", undefined, undefined, true)} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard}>
                <AgentEditSection>
                    <div className="flex flex-col items-center justify-center gap-4 py-12">
                        <p className="text-(--secondary-text-wMain)">Failed to load model configurations.</p>
                        <Button variant="outline" onClick={() => refetch()}>
                            Retry
                        </Button>
                    </div>
                </AgentEditSection>
            </AgentEditWrapper>
        );
    }

    if (models.length === 0) {
        return (
            <AgentEditWrapper sectionTitle={getSectionTitle(Cpu, "Model", undefined, undefined, true)} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard}>
                <AgentEditSection>
                    <div className="flex flex-col items-center justify-center gap-4 py-12">
                        <p className="text-(--secondary-text-wMain)">No models have been set up for your organization. Agents cannot be created without a model.</p>
                        <Button variant="default" onClick={() => window.open("/#/models", "_blank")}>
                            Manage Models <ExternalLink className="ml-1 size-4" />
                        </Button>
                    </div>
                </AgentEditSection>
            </AgentEditWrapper>
        );
    }

    return (
        <AgentEditWrapper sectionTitle={getSectionTitle(Cpu, "Model", undefined, undefined, true)} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard}>
            <AgentEditSection>
                <div>Select a model configuration for your agent.</div>

                {modelRef && !currentModelExists && <MessageBanner variant="warning" className="mt-4" message="Previously selected model is no longer available. Please select a new model." />}

                <fieldset className="mt-8 space-y-3 pr-8" aria-label="Model selection">
                    {sortedModels.map(model => (
                        <AgentModelCard key={model.id} model={model} selectable selected={selectedModel === model.id} onSelect={() => setValue("selectedModel", model.id)} />
                    ))}
                </fieldset>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
