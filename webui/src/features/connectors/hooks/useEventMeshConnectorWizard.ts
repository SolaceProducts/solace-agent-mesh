// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import { useConnectorWizardBase, type WizardStep } from "./useConnectorWizard";

interface UseEventMeshConnectorWizardProps {
    schemaInfo?: { type: string };
    onWizardStepChange?: (step: WizardStep) => void;
    onSubmit: (data: any) => void;
}

export const useEventMeshConnectorWizard = ({ schemaInfo, onWizardStepChange, onSubmit }: UseEventMeshConnectorWizardProps) => {
    const isWizardActive = schemaInfo?.type === "event_mesh";

    const { wizardStep, setWizardStep } = useConnectorWizardBase({ isWizardActive, onWizardStepChange });

    const handleNextOrCreate = useCallback(
        (data: any) => {
            if (isWizardActive && wizardStep === "config") {
                setWizardStep("tools");
            } else if (isWizardActive && wizardStep === "tools") {
                setWizardStep("review");
            } else {
                onSubmit(data);
            }
        },
        [isWizardActive, wizardStep, setWizardStep, onSubmit]
    );

    return {
        wizardStep,
        setWizardStep,
        handleNextOrCreate,
        isWizardActive,
    };
};
