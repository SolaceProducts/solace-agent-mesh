// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentProps } from "react";

import type { ModelConfig } from "@/api/models/types";

import { ModelProviderIcon } from "@/common";

import { useFreeModel } from "./useFreeModel";

type ModelConfigIconProps = Readonly<{
    model: ModelConfig | undefined;
    storedProvider?: string | null;
    size?: ComponentProps<typeof ModelProviderIcon>["size"];
}>;

export function ModelConfigIcon({ model, storedProvider, size }: ModelConfigIconProps) {
    const { freeModelLabel } = useFreeModel();
    const provider = model ? (freeModelLabel(model)?.provider ?? model.provider) : storedProvider;
    return <ModelProviderIcon provider={provider} size={size} />;
}
