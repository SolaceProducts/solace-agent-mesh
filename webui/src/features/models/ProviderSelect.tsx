// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type CapabilityStatus, findCapabilityBadge, useCapabilities } from "@/api/capabilities";
import type { ModelType } from "@/api/models/types";

import { ModelProviderIcon } from "@/common";
import { ComboBox, type ComboBoxItem } from "@/ui";

import { getProviderConfig, type ModelProvider } from "./modelProviderUtils";

interface ProviderSelectProps {
    value: string | undefined;
    onValueChange: (value: string) => void;
    providers: ModelProvider[];
    modelType?: ModelType;
    placeholder?: string;
    disabled?: boolean;
    invalid?: boolean;
}

export const ProviderSelect = ({ value, onValueChange, providers, modelType, placeholder, disabled, invalid }: ProviderSelectProps) => {
    const { data: capabilities } = useCapabilities();
    const dropDownItems: ComboBoxItem[] = providers.map(provider => {
        const config = getProviderConfig(provider.id, modelType);
        const servesDecisionModels = config.modelType === "decision";
        let subtext: string | undefined;
        if (provider.id === "custom") {
            subtext = "Configure a provider that implements the OpenAI-compatible API protocol (including LiteLLM-compatible endpoints)";
        } else if (servesDecisionModels) {
            subtext = config.description;
        }
        return {
            id: provider.id,
            label: provider.label,
            icon: <ModelProviderIcon provider={provider.id} size="xs" />,
            subtext,
            badge: providerBadge(capabilities, config.capabilityKey),
            section: provider.id === "custom" || servesDecisionModels ? "advanced" : "default",
        };
    });

    return <ComboBox value={value} onValueChange={onValueChange} items={dropDownItems} placeholder={placeholder || ""} disabled={disabled} invalid={invalid} dropdownMaxHeight={400} data-testid="modelProviderSelect" />;
};

function providerBadge(capabilities: { key: string; status: CapabilityStatus }[] | undefined, capabilityKey: string | undefined): string | undefined {
    return capabilityKey ? (findCapabilityBadge(capabilities, capabilityKey) ?? undefined) : undefined;
}
