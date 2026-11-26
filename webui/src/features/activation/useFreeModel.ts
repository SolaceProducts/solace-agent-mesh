// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { callsFreeModel, FREE_MODEL_ALIASES, type FreeModelStatus, useActivation, useFreeModelStatus } from "@/api/activation";
import { useModelConfigs } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { useIsActivationGateEnabled } from "@/hooks";

export interface FreeModelLabel {
    modelName: string;
    provider: string;
    providerName: string;
}

const UNNAMED_FREE_MODEL = "Free model";

function labelFor(model: ModelConfig, status: FreeModelStatus | undefined): FreeModelLabel {
    const display = status?.models?.find(m => m.modelName === model.modelName);
    return { modelName: display?.displayName ?? UNNAMED_FREE_MODEL, provider: "anthropic", providerName: "Anthropic" };
}

export function useFreeModel() {
    const gateEnabled = useIsActivationGateEnabled();
    const { data: activation } = useActivation(gateEnabled);
    const freeModel = gateEnabled && activation?.state === "active" && activation.freeModelChosen ? activation.freeModel : undefined;
    const { data: models } = useModelConfigs(freeModel !== undefined);
    const isFreeModel = (model: ModelConfig) => freeModel !== undefined && callsFreeModel(model, freeModel);
    const anyOnFreeModel = (models ?? []).some(isFreeModel);
    const inUse = (models ?? []).some(model => FREE_MODEL_ALIASES.includes(model.alias?.toLowerCase() ?? "") && isFreeModel(model));
    const { data: status } = useFreeModelStatus(anyOnFreeModel);
    const freeModelLabel = (model: ModelConfig) => (isFreeModel(model) ? labelFor(model, status) : undefined);
    const known = freeModel === undefined || models !== undefined;
    return { chosen: freeModel !== undefined, inUse, known, isFreeModel, freeModelLabel };
}
