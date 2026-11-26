// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useRef, useState } from "react";
import { Loader2, Plug, Save } from "lucide-react";

import { useConnectors } from "@/api/connectors";

import { useChatContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, VisuallyHidden } from "@/ui/dialog";

import { CONNECTOR_WIZARD_STEPS, ConnectorEdit } from "@/features/connectors/ConnectorEdit";
import { HighlightFieldsProvider } from "@/features/connectors/HighlightFieldsContext";
import { isConnectorWizard } from "@/features/connectors/hooks";
import { validateConnector } from "@/features/connectors/utils";

import type { Connector, ConnectorSchema } from "@/types";

import { extractConnectorMetadata } from "./builderUtils";

type WizardStep = "config" | "tools" | "review";

interface ConnectorEditDialogProps {
    isOpen: boolean;
    onClose: () => void;
    connectorJson: string;
    onSave: (updatedJson: string) => void;
}

function getButtonText(nextStep: WizardStep | "save", isWizard: boolean): string {
    if (!isWizard) return "Save";

    const stepTitle = CONNECTOR_WIZARD_STEPS.find(step => step.id === nextStep)?.title;
    if (stepTitle) return `Next: ${stepTitle}`;

    return "Save";
}

export function ConnectorEditDialog({ isOpen, onClose, connectorJson, onSave }: ConnectorEditDialogProps) {
    const { addNotification } = useChatContext();
    const { getConnectorSchema } = useChatContext();
    const { data: connectors = [] } = useConnectors();

    const [isSaving, setIsSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [validationError, setValidationError] = useState<string | null>(null);

    const [nextStep, setNextStep] = useState<WizardStep | "save">("save");
    const [currentWizardStep, setCurrentWizardStep] = useState<WizardStep>("config");
    const wizardStepBackRef = useRef<(() => void) | null>(null);

    const metadata = useMemo(() => extractConnectorMetadata(connectorJson), [connectorJson]);

    const schemaInfo: ConnectorSchema | undefined = useMemo(() => {
        if (!metadata) return undefined;
        return getConnectorSchema(metadata.type, metadata.subtype);
    }, [metadata, getConnectorSchema]);

    const initialConnector: Connector | undefined = useMemo(() => {
        if (!metadata) return undefined;

        const cleanedValues: Record<string, any> = {};
        if (metadata.values) {
            for (const [key, value] of Object.entries(metadata.values)) {
                cleanedValues[key] = value === "<<__SAM_REQUIRED__>>" ? "" : value;
            }
        }

        return {
            id: "builder-new",
            name: metadata.name,
            description: metadata.description || "",
            type: metadata.type,
            subtype: metadata.subtype,
            values: cleanedValues,
        } as Connector;
    }, [metadata]);

    const [connectorToEdit, setConnectorToEdit] = useState<Connector | undefined>(initialConnector);
    const lastConnectorJsonRef = useRef(connectorJson);
    if (connectorJson !== lastConnectorJsonRef.current) {
        lastConnectorJsonRef.current = connectorJson;
        setConnectorToEdit(initialConnector);
    }

    const highlightFields = useMemo(() => {
        if (!metadata?.values) return new Set<string>();
        return new Set(
            Object.entries(metadata.values)
                .filter(([, v]) => v === "<<__SAM_REQUIRED__>>")
                .map(([k]) => k)
        );
    }, [metadata]);

    const isWizard = schemaInfo ? isConnectorWizard(schemaInfo) : false;

    const handleWizardStepChange = useCallback((step: WizardStep) => {
        setCurrentWizardStep(step);
        const stepToNextActionMap: Record<WizardStep, WizardStep | "save"> = {
            config: "tools",
            tools: "review",
            review: "save",
        };
        setNextStep(stepToNextActionMap[step]);
    }, []);

    const handleBack = useCallback(() => {
        wizardStepBackRef.current?.();
    }, []);

    const handleSave = useCallback(
        async (updatedConnector: Connector & { resources?: FileList; _isValidationStep?: boolean }) => {
            if (metadata) {
                updatedConnector.type = metadata.type as any;
                updatedConnector.subtype = metadata.subtype as any;
            }

            setIsSaving(true);
            setValidationError(null);
            setError(null);

            try {
                const typeResult = await validateConnector(updatedConnector, updatedConnector._isValidationStep);

                if (!typeResult.success) {
                    setValidationError(typeResult.error!);
                    return;
                }

                if (typeResult.updatedConnector) {
                    setConnectorToEdit(typeResult.updatedConnector);
                }

                if (typeResult.nextStep) {
                    setNextStep(typeResult.nextStep);
                    if (typeResult.nextStep !== "save") {
                        return;
                    }
                }

                const finalConnector = typeResult.updatedConnector || updatedConnector;
                const updatedJson = JSON.stringify(
                    {
                        name: finalConnector.name,
                        description: finalConnector.description,
                        type: metadata?.type,
                        subtype: metadata?.subtype,
                        values: finalConnector.values,
                    },
                    null,
                    2
                );

                onSave(updatedJson);
                addNotification("Connector configuration updated.", "success");
                onClose();
            } catch (err: unknown) {
                const msg = getErrorMessage(err, "Failed to update connector.");
                setError(msg);
            } finally {
                setIsSaving(false);
            }
        },
        [metadata, onSave, onClose, addNotification]
    );

    if (!metadata || !schemaInfo || !connectorToEdit) {
        return (
            <Dialog
                open={isOpen}
                onOpenChange={open => {
                    if (!open) onClose();
                }}
            >
                <DialogContent className="sm:max-w-md">
                    <VisuallyHidden>
                        <DialogTitle>Connector configuration</DialogTitle>
                    </VisuallyHidden>
                    <DialogDescription className="text-sm">Unable to parse connector configuration.</DialogDescription>
                    <Button variant="ghost" onClick={onClose} className="mt-4">
                        Close
                    </Button>
                </DialogContent>
            </Dialog>
        );
    }

    const showBackButton = isWizard && (currentWizardStep === "tools" || currentWizardStep === "review");

    return (
        <Dialog
            open={isOpen}
            onOpenChange={open => {
                if (!open) onClose();
            }}
        >
            <DialogContent className="flex h-[80vh] flex-col gap-0 p-0 sm:max-w-5xl">
                <VisuallyHidden>
                    <DialogDescription>Edit the connector configuration</DialogDescription>
                </VisuallyHidden>
                <div className="border-b px-6 py-4">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                            <Plug className="h-5 w-5" />
                            <DialogTitle className="leading-normal">{metadata.name || "Connector Configuration"}</DialogTitle>
                            <Badge type="info">Connector</Badge>
                        </div>
                    </div>
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto overscroll-y-contain px-6 py-4">
                    <HighlightFieldsProvider value={highlightFields}>
                        <ConnectorEdit
                            schemaInfo={schemaInfo}
                            initialConnectorToEdit={connectorToEdit}
                            isNew={false}
                            existingConnectors={connectors}
                            onSubmit={handleSave}
                            onWizardStepChange={handleWizardStepChange}
                            onExposeBackHandler={backFn => {
                                wizardStepBackRef.current = backFn;
                            }}
                            validationError={validationError}
                            onClearValidationError={() => setValidationError(null)}
                        />
                    </HighlightFieldsProvider>
                </div>

                <div className="border-t px-6 py-3">
                    {error && <div className="mb-2 text-xs text-(--error-wMain)">{error}</div>}
                    <div className={cn("flex", showBackButton ? "justify-between" : "justify-end")}>
                        {showBackButton && (
                            <Button variant="outline" onClick={handleBack} disabled={isSaving}>
                                Back
                            </Button>
                        )}
                        <div className="flex gap-2">
                            <Button variant="ghost" onClick={onClose}>
                                Cancel
                            </Button>
                            <Button type="submit" form="connector-form" disabled={isSaving || !schemaInfo}>
                                {isSaving ? <Loader2 className="mr-1.5 h-4 w-4 animate-spin" /> : <Save className="mr-1.5 h-4 w-4" />}
                                {getButtonText(nextStep, isWizard)}
                            </Button>
                        </div>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
