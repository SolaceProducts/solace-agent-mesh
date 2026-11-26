// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect } from "react";
import { OpenFeature, OpenFeatureProvider } from "@openfeature/react-sdk";

import { useFeatureFlags } from "@/api/features";

import { SamFeatureProvider } from "./openfeature/SamFeatureProvider";

interface FeatureFlagProviderProps {
    children: ReactNode;
}

export function FeatureFlagProvider({ children }: Readonly<FeatureFlagProviderProps>) {
    const { data, isError } = useFeatureFlags();

    useEffect(() => {
        if (data) {
            const flags = Object.fromEntries(data.map(f => [f.key, f.resolved]));
            OpenFeature.setProvider(new SamFeatureProvider(flags));
        } else if (isError) {
            console.warn("Failed to load feature flags, using default flag values");
            OpenFeature.setProvider(new SamFeatureProvider({}));
        }
    }, [data, isError]);

    return <OpenFeatureProvider>{children}</OpenFeatureProvider>;
}
