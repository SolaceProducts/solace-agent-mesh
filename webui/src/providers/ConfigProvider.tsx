// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useMemo } from "react";

import type { BackendConfig } from "@/api/config";
import { useConfigQuery } from "@/api/config";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts";
import { useCsrfContext } from "@/providers/contexts";

import { EmptyState } from "@/common/status/EmptyState";

interface ConfigProviderProps {
    children: ReactNode;
}

const platformUrlConfiguredOnLegacyBackend = (data: BackendConfig): boolean => Boolean(data.frontendPlatformServerUrl);

function mapConfigToContext(data: BackendConfig): Omit<ConfigContextValue, "refetch"> {
    const effectiveUseAuthorization = data.frontendUseAuthorization ?? false;
    const backgroundTasksDefaultTimeoutMs = data.backgroundTasksConfig?.defaultTimeoutMs ?? 3600000;
    const platformConfigured = data.frontendPlatformServiceEnabled ?? platformUrlConfiguredOnLegacyBackend(data);
    const textToSpeechEnabled = data.ttsSettings?.textToSpeech ?? false;
    const speechToTextEnabled = data.sttSettings?.speechToText ?? false;

    return {
        webuiServerUrl: data.frontendServerUrl,
        platformServerUrl: data.frontendPlatformServerUrl ?? "",
        googleChatEndpointBase: data.googleChatEndpointBase ?? "",
        configAuthLoginUrl: data.frontendAuthLoginUrl,
        configUseAuthorization: effectiveUseAuthorization,
        configWelcomeMessage: data.frontendWelcomeMessage ?? null,
        configDisclaimerText: data.frontendDisclaimerText ?? "",
        configRedirectUrl: data.frontendRedirectUrl,
        configCollectFeedback: data.frontendCollectFeedback,
        configAppName: data.frontendAppName,
        configLogoUrl: data.frontendLogoUrl ?? null,
        configSmallLogoUrl: data.frontendSmallLogoUrl ?? null,
        frontend_use_authorization: data.frontendUseAuthorization,
        persistenceEnabled: data.persistenceEnabled ?? false,
        schedulerEnabled: data.schedulerEnabled ?? false,
        activationGateEnabled: data.activationGateEnabled ?? false,
        peopleSearchAvailable: data.peopleSearchAvailable ?? false,
        azureWorkloadIdentityEnabled: data.azureWorkloadIdentityEnabled ?? false,
        productAnalyticsEnabled: data.productAnalyticsEnabled ?? false,
        validationLimits: data.validationLimits,
        dataRetention: data.dataRetention,
        backgroundTasksDefaultTimeoutMs,
        platformConfigured,
        identityServiceType: data.identityServiceType,
        claimKey: data.externalAuthClaimKey ?? "",
        textToSpeechEnabled,
        speechToTextEnabled,
    };
}

export function ConfigProvider({ children }: Readonly<ConfigProviderProps>) {
    const { fetchCsrfToken } = useCsrfContext();
    const { data: result, isLoading, isError, error, refetch } = useConfigQuery();

    const mappedConfig = useMemo(() => (result ? mapConfigToContext(result.data) : null), [result]);

    useEffect(() => {
        if (!mappedConfig?.configUseAuthorization) return;
        console.log("Fetching CSRF token for config-related requests...");
        fetchCsrfToken();
    }, [mappedConfig?.configUseAuthorization, fetchCsrfToken]);

    const value = useMemo<ConfigContextValue | null>(
        () =>
            mappedConfig
                ? {
                      ...mappedConfig,
                      refetch: async () => {
                          await refetch();
                      },
                  }
                : null,
        [mappedConfig, refetch]
    );

    if (value) {
        return <ConfigContext.Provider value={value}>{children}</ConfigContext.Provider>;
    }

    if (isLoading) {
        return <EmptyState variant="loading" title="Loading Configuration..." className="h-screen w-screen" />;
    }

    if (isError) {
        console.error("Error initializing app:", error);
        return <EmptyState variant="error" title="Configuration Error" subtitle="Please check the backend server and network connection, then refresh the page." className="h-screen w-screen" />;
    }

    return <EmptyState variant="loading" title="Initializing Application..." className="h-screen w-screen" />;
}
