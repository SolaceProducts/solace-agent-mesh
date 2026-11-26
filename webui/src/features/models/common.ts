// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isDecisionModel, type ModelConfig } from "@/api/models/types";

import { isHttpUrl } from "@/utils/url";

export const DEFAULT_MODEL_ALIASES = ["general", "planning"];

export const isModelConfigured = (model: ModelConfig): boolean => {
    return !!model.provider && !!model.modelName?.trim();
};

export const modelConfigurationIssues = (model: ModelConfig): string[] => {
    const issues: string[] = [];
    if (!isHttpUrl(model.apiBase)) {
        issues.push(`API base is invalid: ${model.apiBase}`);
    }
    return issues;
};

import { AUTH_TYPE_LABELS, PROVIDER_DISPLAY_NAMES, splitModelPrefix } from "./modelProviderUtils";

export { AUTH_TYPE_LABELS, PROVIDER_DISPLAY_NAMES };

export const getProviderDisplayName = (provider: string | null | undefined): string | null => {
    if (!provider) return null;
    return PROVIDER_DISPLAY_NAMES[provider] || provider;
};

export const getDisplayModelName = (model: Pick<ModelConfig, "modelName" | "modelType">): string => {
    if (!model.modelName) return "";
    return isDecisionModel(model) ? model.modelName : splitModelPrefix(model.modelName).name || model.modelName;
};

export const getDisplayAliasName = (alias: string, createdBy?: string): string => {
    if (!alias) return "";

    if (createdBy === "system") {
        return alias
            .split("_")
            .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
            .join(" ");
    }

    return alias;
};
