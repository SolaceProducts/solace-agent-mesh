// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface BackendConfig {
    frontendServerUrl: string;
    frontendPlatformServerUrl?: string;
    googleChatEndpointBase?: string;
    frontendPlatformServiceEnabled?: boolean;
    frontendAuthLoginUrl: string;
    frontendUseAuthorization: boolean;
    frontendWelcomeMessage: string | null;
    frontendDisclaimerText?: string;
    frontendRedirectUrl: string;
    frontendCollectFeedback: boolean;
    frontendAppName: string;
    frontendLogoUrl: string | null;
    frontendSmallLogoUrl?: string | null;
    persistenceEnabled?: boolean;
    schedulerEnabled?: boolean;
    activationGateEnabled?: boolean;
    peopleSearchAvailable?: boolean;
    azureWorkloadIdentityEnabled?: boolean;
    productAnalyticsEnabled?: boolean;
    identityServiceType: string | null;
    externalAuthProvider?: string;
    externalAuthClaimKey?: string;
    sessionIdleTimeoutSeconds: number;
    validationLimits?: {
        projectNameMax?: number;
        projectDescriptionMax?: number;
        projectInstructionsMax?: number;
    };
    dataRetention?: {
        taskDays: number;
        feedbackDays: number;
    };
    backgroundTasksConfig?: {
        defaultTimeoutMs?: number;
    };
    ttsSettings?: {
        textToSpeech?: boolean;
        engineTTS?: string;
        ttsProvider?: string;
    };
    sttSettings?: {
        speechToText?: boolean;
    };
}

export interface ConfigQueryResult {
    data: BackendConfig;
    desktopApiBase: string | null;
}
