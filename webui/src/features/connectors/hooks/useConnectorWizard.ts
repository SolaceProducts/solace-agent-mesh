// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

export type WizardStep = "config" | "tools" | "review";

export const isMCPConnectorWizard = (schemaInfo?: { type: string }): boolean => {
    return schemaInfo?.type === "mcp";
};

export const isAPIConnectorWizard = (schemaInfo?: { type: string; subtype?: string }): boolean => {
    return schemaInfo?.type === "api" && schemaInfo?.subtype === "openapi";
};

export const isEventMeshConnectorWizard = (schemaInfo?: { type: string }): boolean => {
    return schemaInfo?.type === "event_mesh";
};

export const isConnectorWizard = (schemaInfo?: { type: string; subtype?: string }): boolean => {
    return isMCPConnectorWizard(schemaInfo) || isAPIConnectorWizard(schemaInfo) || isEventMeshConnectorWizard(schemaInfo);
};

interface UseConnectorWizardBaseProps {
    isWizardActive: boolean;
    onWizardStepChange?: (step: WizardStep) => void;
}

export const useConnectorWizardBase = ({ isWizardActive, onWizardStepChange }: UseConnectorWizardBaseProps) => {
    const [wizardStep, setWizardStep] = useState<WizardStep>("config");

    useEffect(() => {
        if (isWizardActive && onWizardStepChange) {
            onWizardStepChange(wizardStep);
        }
    }, [wizardStep, onWizardStepChange, isWizardActive]);

    return {
        wizardStep,
        setWizardStep,
    };
};

export const normalizeToolSelection = (value: any): string[] => {
    if (Array.isArray(value)) {
        return value.filter((item): item is string => typeof item === "string");
    }
    if (typeof value === "string") {
        return value
            .split(",")
            .map(s => s.trim())
            .filter(Boolean);
    }
    return [];
};

export const validateToolSelection = (toolSelection: any): string | null => {
    const toolArray = normalizeToolSelection(toolSelection);
    if (toolArray.length === 0) {
        return "At least one tool must be selected";
    }
    return null;
};

export const validateConnectorName = (connectorName: string, existingConnectors: Array<{ id: string; name: string }>, isNew: boolean, currentConnectorId?: string): string | null => {
    if (!connectorName) {
        return null;
    }

    const isDuplicate = existingConnectors.some(connector => {
        if (!isNew && connector.id === currentConnectorId) {
            return false;
        }
        return connector.name.toLowerCase() === connectorName.toLowerCase();
    });

    if (isDuplicate) {
        return "A connector with this name already exists. Please choose a different name.";
    }

    return null;
};

export const validateAndAdvanceFromToolsStep = (
    toolSelectionFieldPath: string,
    getValues: (field: string) => any,
    setError: (field: string, error: { type: string; message: string }) => void,
    clearErrors: (field: string) => void,
    setWizardStep: (step: WizardStep) => void
): boolean => {
    const toolSelection = getValues(toolSelectionFieldPath);

    const toolError = validateToolSelection(toolSelection);
    if (toolError) {
        setError(toolSelectionFieldPath, { type: "manual", message: toolError });
        return false;
    }

    clearErrors(toolSelectionFieldPath);

    setWizardStep("review");
    return true;
};
