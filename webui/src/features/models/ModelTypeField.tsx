// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCapabilityBadge } from "@/api/capabilities";
import type { ModelType } from "@/api/models/types";

import { type RadioOption, RadioOptions } from "@/common/form";
import { FormFieldLayoutItem } from "@/common/layout";

import { LifecycleBadge } from "@/ui";

import { DECISION_MODELS_CAPABILITY, MODEL_TYPE_LABELS } from "./modelProviderUtils";

const MODEL_TYPE_DESCRIPTIONS: Record<ModelType, string> = {
    chat: "A large language model that agents and evaluations use.",
    decision: "A structured model that answers typed questions for workflow decision nodes and decision evaluators. Agents and LLM-judge evaluators cannot use it.",
};

interface ModelTypeFieldProps {
    modelType: ModelType;
    onChange?: (modelType: ModelType) => void;
}

export const ModelTypeField = ({ modelType, onChange }: ModelTypeFieldProps) => {
    const decisionBadgeText = useCapabilityBadge(DECISION_MODELS_CAPABILITY);
    const decisionBadge = decisionBadgeText ? <LifecycleBadge>{decisionBadgeText}</LifecycleBadge> : undefined;

    if (!onChange) {
        return (
            <FormFieldLayoutItem label="Model Type" helpText="A model's type cannot be changed after it is created.">
                <div className="flex items-center gap-2 text-sm" data-testid="modelTypeReadOnly">
                    {MODEL_TYPE_LABELS[modelType]}
                    {modelType === "decision" && decisionBadge}
                </div>
            </FormFieldLayoutItem>
        );
    }

    const options: RadioOption[] = [
        { value: "chat", label: MODEL_TYPE_LABELS.chat, description: MODEL_TYPE_DESCRIPTIONS.chat },
        { value: "decision", label: MODEL_TYPE_LABELS.decision, description: MODEL_TYPE_DESCRIPTIONS.decision, badge: decisionBadge },
    ];
    return (
        <FormFieldLayoutItem label="Model Type" required helpText="Choose what the model is for; it decides which providers are offered. The type cannot be changed after the model is created.">
            <RadioOptions orientation="vertical" ariaLabel="Model Type" options={options} value={modelType} onChange={value => onChange(value as ModelType)} />
        </FormFieldLayoutItem>
    );
};
