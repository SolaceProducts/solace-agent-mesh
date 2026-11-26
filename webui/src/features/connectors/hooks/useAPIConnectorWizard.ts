// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import type { UseFormClearErrors, UseFormGetValues, UseFormSetError } from "react-hook-form";

import type { Connector } from "@/types";

import { useConnectorWizardBase, validateAndAdvanceFromToolsStep, validateConnectorName, type WizardStep } from "./useConnectorWizard";

interface UseAPIConnectorWizardProps {
    schemaInfo: { type: string; subtype?: string };
    getValues: UseFormGetValues<any>;
    setError: UseFormSetError<any>;
    clearErrors: UseFormClearErrors<any>;
    onSubmit: (data: any) => void;
    onWizardStepChange?: (step: WizardStep) => void;
    onClearValidationError?: () => void;
    existingConnectors?: Connector[];
    isNew?: boolean;
    currentConnectorId?: string;
}

export const useAPIConnectorWizard = ({ schemaInfo, getValues, setError, clearErrors, onSubmit, onWizardStepChange, onClearValidationError, existingConnectors = [], isNew = false, currentConnectorId }: UseAPIConnectorWizardProps) => {
    const isAPIWizard = schemaInfo?.type === "api" && schemaInfo?.subtype === "openapi";

    const { wizardStep, setWizardStep } = useConnectorWizardBase({
        isWizardActive: isAPIWizard,
        onWizardStepChange,
    });

    const handleNextOrCreate = useCallback(
        (data: any) => {
            if (onClearValidationError) {
                onClearValidationError();
            }

            if (isAPIWizard && wizardStep === "config") {
                const connectorName = getValues("name");
                const tools = getValues("tools");
                const openAPISpec = getValues("openAPISpec");

                const nameError = validateConnectorName(connectorName, existingConnectors, isNew, currentConnectorId);
                if (nameError) {
                    setError("name", { type: "manual", message: nameError });
                    return;
                }

                if (!openAPISpec) {
                    setError("resources", {
                        type: "manual",
                        message: "An OpenAPI specification file is required",
                    });
                    return;
                }

                if (!tools || tools.length === 0) {
                    setError("resources", {
                        type: "manual",
                        message: "No tools found in OpenAPI specification",
                    });
                    return;
                }

                clearErrors(["name", "resources"]);

                setWizardStep("tools");
            } else if (isAPIWizard && wizardStep === "tools") {
                validateAndAdvanceFromToolsStep("values.allow_list", getValues, setError, clearErrors, setWizardStep);
            } else {
                onSubmit(data);
            }
        },
        [isAPIWizard, wizardStep, onSubmit, getValues, setError, clearErrors, onClearValidationError, existingConnectors, isNew, currentConnectorId]
    );

    return {
        wizardStep,
        setWizardStep,
        isAPIWizard,
        handleNextOrCreate,
    };
};
