// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import type { UseFormClearErrors, UseFormGetValues, UseFormSetError, UseFormSetValue } from "react-hook-form";

import type { Connector } from "@/types";

import { buildOAuthInitiateRequest, parseKeyValuePairs } from "../utils/authConfigBuilder";

import { validateAndAdvanceFromToolsStep } from "./useConnectorWizard";
import { useMCPOAuth } from "./useMCPOAuth";

interface UseMCPConnectorWizardProps {
    schemaInfo: { type: string };
    initialConnectorToEdit?: Connector;
    formValues: Record<string, any>;
    getValues: UseFormGetValues<any>;
    setValue: UseFormSetValue<any>;
    setError: UseFormSetError<any>;
    clearErrors: UseFormClearErrors<any>;
    onSubmit: (data: any) => void;
    onWizardStepChange?: (step: "config" | "tools" | "review") => void;
    onClearValidationError?: () => void;
}

export const useMCPConnectorWizard = ({ schemaInfo, initialConnectorToEdit, formValues, getValues, setValue, setError, clearErrors, onSubmit, onWizardStepChange, onClearValidationError }: UseMCPConnectorWizardProps) => {
    const [wizardStep, setWizardStep] = useState<"config" | "tools" | "review">("config");
    const { startOAuthFlow, discoverTools, isAuthenticating, isDiscovering, error: oauthError, sessionId, setError: setOAuthError } = useMCPOAuth();

    const isMCPWizard = schemaInfo?.type === "mcp";

    const isEditing = initialConnectorToEdit?.id && initialConnectorToEdit.id !== "new";

    const [isInitialMount, setIsInitialMount] = useState(true);

    const previousToolsRef = useRef<any[] | null>(null);

    useEffect(() => {
        const timer = setTimeout(() => setIsInitialMount(false), 0);
        return () => clearTimeout(timer);
    }, []);

    useEffect(() => {
        if (initialConnectorToEdit && (initialConnectorToEdit as any).mcpTools) {
            const currentTools = (initialConnectorToEdit as any).mcpTools;
            const previousTools = previousToolsRef.current;

            setValue("mcpTools", currentTools);

            const toolsChanged = previousTools !== currentTools;

            const shouldAdvance = !isEditing || (!isInitialMount && toolsChanged);

            if (shouldAdvance) {
                setWizardStep("tools");
            }

            previousToolsRef.current = currentTools;
        }
    }, [initialConnectorToEdit, setValue, isEditing, isInitialMount]);

    useEffect(() => {
        if (onWizardStepChange) {
            onWizardStepChange(wizardStep);
        }
    }, [wizardStep, onWizardStepChange]);

    useEffect(() => {
        if (sessionId && wizardStep === "config" && formValues) {
            const autoDiscoverTools = async () => {
                const customHeaders = parseKeyValuePairs(formValues.custom_headers);
                const tools = await discoverTools(sessionId, formValues.server_url, formValues.connection_type, customHeaders);

                if (tools && tools.length > 0) {
                    setValue("mcpTools", tools);
                    setValue("values.manifest", JSON.stringify(tools));
                    setWizardStep("tools");
                }
            };

            autoDiscoverTools();
        }
    }, [sessionId, formValues, discoverTools, setValue, setOAuthError]);

    const handleNextOrCreate = useCallback(
        async (data: any) => {
            if (onClearValidationError) {
                onClearValidationError();
            }

            if (isMCPWizard && wizardStep === "config") {
                const authType = formValues.auth_type;

                if (authType === "oauth") {
                    const oauthRequest = buildOAuthInitiateRequest(formValues);
                    await startOAuthFlow(oauthRequest);
                } else {
                    onSubmit({ ...data, _isValidationStep: true });
                }
            } else if (isMCPWizard && wizardStep === "tools") {
                validateAndAdvanceFromToolsStep("values.selected_tools", getValues, setError, clearErrors, setWizardStep);
            } else {
                if (isMCPWizard && wizardStep === "review") {
                    const mcpTools = getValues("mcpTools");
                    const selectedTools = getValues("values.selected_tools");

                    if (mcpTools && Array.isArray(mcpTools)) {
                        data.values.manifest = mcpTools;
                    }

                    if (selectedTools) {
                        data.values.selected_tools = selectedTools;
                    }
                }

                onSubmit(data);
            }
        },
        [isMCPWizard, wizardStep, formValues, startOAuthFlow, onSubmit, getValues, setError, clearErrors, onClearValidationError]
    );

    return {
        wizardStep,
        setWizardStep,
        isMCPWizard,
        isAuthenticating,
        isDiscovering,
        oauthError,
        handleNextOrCreate,
    };
};
