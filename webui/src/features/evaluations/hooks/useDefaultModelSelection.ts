// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

import { useChatModelConfigs } from "@/api/models/hooks";

export function useDefaultModelSelection(enabled: boolean, currentModelId: string | undefined, setModel: (id: string) => void) {
    const { data: modelConfigs = [] } = useChatModelConfigs();
    useEffect(() => {
        if (!enabled || currentModelId || modelConfigs.length === 0) return;
        const defaultModel = modelConfigs.find(m => m.alias?.trim().toLowerCase() === "general") ?? modelConfigs[0];
        if (defaultModel) setModel(defaultModel.id);
    }, [enabled, currentModelId, modelConfigs, setModel]);
}
