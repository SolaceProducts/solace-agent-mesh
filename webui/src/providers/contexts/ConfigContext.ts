// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export interface ValidationLimits {
    projectNameMax?: number;
    projectDescriptionMax?: number;
    projectInstructionsMax?: number;
    projectFileDescriptionMax?: number;
    maxPerFileUploadSizeBytes?: number;
    maxBatchUploadSizeBytes?: number;
    maxZipUploadSizeBytes?: number;
    maxProjectSizeBytes?: number;
}

export interface ConfigContextValue {
    webuiServerUrl: string;
    platformServerUrl: string;
    googleChatEndpointBase?: string;
    configAuthLoginUrl: string;
    configUseAuthorization: boolean;
    configWelcomeMessage: string | null;
    configDisclaimerText: string;
    configRedirectUrl: string;
    configCollectFeedback: boolean;
    configAppName: string;
    configLogoUrl: string | null;
    configSmallLogoUrl: string | null;
    frontend_use_authorization: boolean;

    persistenceEnabled?: boolean;

    schedulerEnabled?: boolean;

    activationGateEnabled?: boolean;

    peopleSearchAvailable?: boolean;

    azureWorkloadIdentityEnabled?: boolean;

    productAnalyticsEnabled?: boolean;

    validationLimits?: ValidationLimits;
    dataRetention?: { taskDays: number; feedbackDays: number };

    backgroundTasksDefaultTimeoutMs?: number;

    platformConfigured: boolean;

    identityServiceType: string | null;

    claimKey: string;

    textToSpeechEnabled?: boolean;

    speechToTextEnabled?: boolean;

    refetch: () => Promise<void>;
}

export const ConfigContext = createContext<ConfigContextValue | null>(null);

export function useConfigContext(): ConfigContextValue {
    const context = useContext(ConfigContext);
    if (context === null) {
        throw new Error("useConfig must be used within a ConfigProvider");
    }
    return context;
}
