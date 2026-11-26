// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UseFormReturn } from "react-hook-form";

import { ApiV2Error } from "@/api/common";
import type { RemoteAgentsApi } from "@/api/remote-agents";

import { getErrorMessage } from "@/utils/api";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import type { ConnectAgentFormMethods } from "./ConnectAgentForm";

export const CONNECT_STEP_IDS = {
    CONNECT: "connect",
    ADDITIONAL_INFO: "additional-info",
    REVIEW: "review",
} as const;

export const STEP1_FIELDS = new Set(["agentUrl", "agentCardLocation", "agentCardUrl", "authenticationType", "authBearerToken", "authApikeyValue", "authOauth2TokenUrl", "authOauth2ClientId", "authOauth2ClientSecret", "authOauth2Scope"]);

export function buildWellKnownUrl(baseUrl: string): string {
    const trimmedUrl = baseUrl.replace(/\/+$/, "");
    return `${trimmedUrl}/.well-known/agent-card.json`;
}

const DUPLICATE_NAME_HINTS = ["duplicate", "already exists", "unique", "idx_a2a_agents_name_normalized"];

export function isDuplicateNameError(detail: string): boolean {
    return DUPLICATE_NAME_HINTS.some(hint => detail.includes(hint));
}

export function routeValidationFieldError(
    fieldName: string,
    message: string,
    formMethods: ConnectAgentFormMethods | null,
    additionalInfoFormMethods: UseFormReturn<AdditionalInfoFormData> | null,
    stepperMethods: { current: { id: string }; goTo: (id: string) => void } | undefined | null
): boolean {
    if (STEP1_FIELDS.has(fieldName)) {
        formMethods?.setError(fieldName as any, { type: "manual", message });
        if (stepperMethods && stepperMethods.current.id !== CONNECT_STEP_IDS.CONNECT) {
            stepperMethods.goTo(CONNECT_STEP_IDS.CONNECT);
        }
        return false;
    }
    additionalInfoFormMethods?.setError(fieldName as any, { type: "manual", message });
    return true;
}

export function applyValidationErrorsToForms(
    validationDetails: Record<string, string[]>,
    formMethods: ConnectAgentFormMethods | null,
    additionalInfoFormMethods: UseFormReturn<AdditionalInfoFormData> | null,
    stepperMethods: { current: { id: string }; goTo: (id: string) => void } | undefined | null
): boolean {
    let shouldNavigateToStep2 = false;
    for (const fieldName of Object.keys(validationDetails)) {
        const errorMessages = validationDetails[fieldName];
        if (!Array.isArray(errorMessages) || errorMessages.length === 0) continue;
        if (routeValidationFieldError(fieldName, errorMessages[0], formMethods, additionalInfoFormMethods, stepperMethods)) {
            shouldNavigateToStep2 = true;
        }
    }
    return shouldNavigateToStep2;
}

export interface ConnectErrorState {
    errorTitle: string;
    errorMessage: string;
    shouldNavigateToStep2: boolean;
}

export function mapBadRequestOrConflict(
    error: ApiV2Error,
    isEditMode: boolean,
    formData: { agentCardLocation?: string },
    formMethods: ConnectAgentFormMethods | null,
    additionalInfoFormMethods: UseFormReturn<AdditionalInfoFormData> | null,
    stepperMethods: { current: { id: string }; goTo: (id: string) => void } | undefined | null
): ConnectErrorState {
    const detail = error.message;
    const validationDetails = error.validationDetails;
    if (isDuplicateNameError(detail)) {
        if (additionalInfoFormMethods) {
            const hasAgentCard = formData.agentCardLocation !== "none";
            const fieldName = hasAgentCard ? "solaceDisplayName" : "agentName";
            additionalInfoFormMethods.setError(fieldName as any, {
                type: "manual",
                message: "This name already exists. Please choose a different name.",
            });
        }
        return {
            errorTitle: "Duplicate Agent Name",
            errorMessage: "An agent with this name (or similar name) already exists. Please choose a different name.",
            shouldNavigateToStep2: true,
        };
    }
    if (validationDetails) {
        const shouldNavigateToStep2 = applyValidationErrorsToForms(validationDetails, formMethods, additionalInfoFormMethods, stepperMethods);
        return {
            errorTitle: "Validation Error",
            errorMessage: "Please fix the validation errors and try again.",
            shouldNavigateToStep2,
        };
    }
    return {
        errorTitle: isEditMode ? "Update Failed" : "Connection Failed",
        errorMessage: detail || "Invalid request data. Please check your inputs.",
        shouldNavigateToStep2: false,
    };
}

const REJECTED_REQUEST_STATUSES = new Set([400, 409, 422]);

export function mapConnectError(
    error: unknown,
    isEditMode: boolean,
    formData: { agentCardLocation?: string },
    formMethods: ConnectAgentFormMethods | null,
    additionalInfoFormMethods: UseFormReturn<AdditionalInfoFormData> | null,
    stepperMethods: { current: { id: string }; goTo: (id: string) => void } | undefined | null
): ConnectErrorState {
    const status = error instanceof ApiV2Error ? error.status : undefined;
    if (status === 501) {
        return {
            errorTitle: "Feature Not Enabled",
            errorMessage: "Remote A2A agents feature is not enabled on the server. Please contact your administrator.",
            shouldNavigateToStep2: false,
        };
    }
    if (status === 403) {
        return {
            errorTitle: "Permission Denied",
            errorMessage: `You don't have permission to ${isEditMode ? "update" : "create"} remote agents.`,
            shouldNavigateToStep2: false,
        };
    }
    if (error instanceof ApiV2Error && REJECTED_REQUEST_STATUSES.has(error.status)) {
        return mapBadRequestOrConflict(error, isEditMode, formData, formMethods, additionalInfoFormMethods, stepperMethods);
    }
    return {
        errorTitle: isEditMode ? "Update Failed" : "Connection Failed",
        errorMessage: getErrorMessage(error, "Failed to connect agent. Please try again."),
        shouldNavigateToStep2: false,
    };
}

export function buildConnectSuccessMessage(isEditMode: boolean, deployed: boolean): string {
    const verbPast = isEditMode ? "updated" : "connected";
    const middle = deployed ? "and deployed " : "";
    return `Remote agent ${verbPast} ${middle}successfully`.replace(/\s+/g, " ").trim();
}

export interface DeployAfterSaveResult {
    failed: boolean;
    message?: string;
}

export async function runDeployAfterSave(resolvedAgentId: string, deployRemoteAgent: RemoteAgentsApi["deploy"]): Promise<DeployAfterSaveResult> {
    try {
        const deployResp = await deployRemoteAgent(resolvedAgentId, "deploy");
        if (deployResp.status === "failed") {
            return { failed: true, message: deployResp.errorMessage ?? "Deployment failed." };
        }
        return { failed: false };
    } catch (deployErr) {
        console.error("deploy after connect failed:", deployErr);
        return {
            failed: true,
            message: getErrorMessage(deployErr, "Deployment failed."),
        };
    }
}
