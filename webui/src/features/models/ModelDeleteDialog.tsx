// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useId, useState } from "react";
import { AlertTriangle, ExternalLink } from "lucide-react";

import { type ModelDependent, ModelKeptError, modelsApi } from "@/api/models";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { ConfirmationDialog, MessageBanner } from "@/common";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger, Badge, Input } from "@/ui";
import { getErrorMessage } from "@/utils";

import { DEFAULT_MODEL_ALIASES } from "./common";

const DEPENDENT_LIST_ACCORDION_THRESHOLD = 5;
const WORKFLOW_DEPENDENT_TYPE = "workflow";
const DEPLOYED_STATUS = "deployed";
const DELETE_FAILED_MESSAGE = "Failed to delete the model.";

interface ModelDeleteDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void | Promise<void>;
    modelId: string;
    modelAlias: string;
    isLoading?: boolean;
}

export const ModelDeleteDialog = React.memo<ModelDeleteDialogProps>(({ open, onOpenChange, onConfirm, modelId, modelAlias, isLoading }) => {
    const isDefaultModel = DEFAULT_MODEL_ALIASES.includes(modelAlias.toLowerCase());

    if (isDefaultModel) {
        return <DefaultModelDialog open={open} onOpenChange={onOpenChange} modelAlias={modelAlias} />;
    }

    return <ConfirmDeleteDialog open={open} onOpenChange={onOpenChange} onConfirm={onConfirm} isLoading={isLoading} modelId={modelId} modelAlias={modelAlias} />;
});

const DefaultModelDialog: React.FC<{
    open: boolean;
    onOpenChange: (open: boolean) => void;
    modelAlias: string;
}> = ({ open, onOpenChange, modelAlias }) => {
    const displayName = modelAlias.charAt(0).toUpperCase() + modelAlias.slice(1);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-xl">
                <DialogHeader>
                    <DialogTitle>Unable to Delete</DialogTitle>
                    <DialogDescription />
                </DialogHeader>
                <div className="flex flex-col gap-3">
                    <p>The {displayName} model cannot be deleted as it is required for AI features in the platform.</p>
                </div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" title="Close">
                            Close
                        </Button>
                    </DialogClose>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

const agentHref = (dependent: ModelDependent) => `#/agent-mesh/${dependent.id}`;
const workflowHref = (dependent: ModelDependent) => `#/agent-mesh/workflows/${encodeURIComponent(dependent.name)}`;

const pluralize = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

const DependentLink = ({ dependent, href }: { dependent: ModelDependent; href: string }) => (
    <div className="flex items-center gap-2 px-3 py-1.5">
        <Button variant="link" className="h-auto p-0 text-sm" onClick={() => globalThis.open(href, "_blank")}>
            {dependent.name}
            <ExternalLink size={13} className="shrink-0" />
        </Button>
        {dependent.deploymentStatus === DEPLOYED_STATUS && (
            <Badge type="warning" data-testid="modelDeleteWillUndeploy">
                Will be undeployed
            </Badge>
        )}
        {dependent.mayStillRun === true && dependent.deploymentStatus !== DEPLOYED_STATUS && (
            <Badge type="warning" data-testid="modelDeleteMayStillRun">
                May still be running
            </Badge>
        )}
    </div>
);

const DependentList: React.FC<{
    title: string;
    accordionItem: string;
    dependents: ModelDependent[];
    hrefFor: (dependent: ModelDependent) => string;
}> = ({ title, accordionItem, dependents, hrefFor }) => {
    const links = dependents.map(dependent => <DependentLink key={dependent.id} dependent={dependent} href={hrefFor(dependent)} />);

    return (
        <div className="overflow-hidden rounded-md border border-(--secondary-w20) bg-(--background-w20)">
            {dependents.length > DEPENDENT_LIST_ACCORDION_THRESHOLD ? (
                <Accordion type="single" collapsible>
                    <AccordionItem value={accordionItem} className="border-b-0">
                        <AccordionTrigger className="px-3 py-2.5 text-sm text-(--secondary-text-wMain) hover:no-underline [&>svg]:mr-0 [&>svg]:ml-auto">{title}</AccordionTrigger>
                        <AccordionContent className="pb-0">
                            <div className="flex max-h-64 flex-col overflow-y-auto">{links}</div>
                        </AccordionContent>
                    </AccordionItem>
                </Accordion>
            ) : (
                <>
                    <p className="px-3 py-2.5 text-sm text-(--secondary-text-wMain)">{title}</p>
                    <div className="flex flex-col">{links}</div>
                </>
            )}
        </div>
    );
};

const WorkflowDependents: React.FC<{ workflows: ModelDependent[] }> = ({ workflows }) => (
    <div className="flex flex-col gap-3" data-testid="modelDeleteWorkflowDependents">
        <div className="flex gap-3">
            <AlertTriangle size={18} className="mt-0.5 shrink-0 text-(--warning-wMain)" />
            <p className="text-sm text-(--primary-text-wMain)">{`This model is currently being used by ${pluralize(workflows.length, "workflow")}. Deleting this model will un-deploy any workflows that are currently deployed.`}</p>
        </div>
        <DependentList title={`Affected Workflows (${workflows.length})`} accordionItem="affected-workflows" dependents={workflows} hrefFor={workflowHref} />
    </div>
);

const AgentDependents: React.FC<{ agents: ModelDependent[] }> = ({ agents }) => (
    <div className="flex flex-col gap-3">
        <div className="flex gap-3">
            <AlertTriangle size={18} className="mt-0.5 shrink-0 text-(--warning-wMain)" />
            <p className="text-sm text-(--primary-text-wMain)">
                {`This model is currently being used by ${pluralize(agents.length, "agent")}. Deleting this model will cause these agents to become un-deployed. Any code-based agents referencing this model will also stop functioning.`}
            </p>
        </div>
        <DependentList title={`Affected Agents (${agents.length})`} accordionItem="affected-agents" dependents={agents} hrefFor={agentHref} />
    </div>
);

const splitDependents = (dependents: ModelDependent[]) => ({
    agents: dependents.filter(dependent => dependent.type !== WORKFLOW_DEPENDENT_TYPE),
    workflows: dependents.filter(dependent => dependent.type === WORKFLOW_DEPENDENT_TYPE),
});

const ConfirmDeleteDialog: React.FC<{
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void | Promise<void>;
    isLoading?: boolean;
    modelId: string;
    modelAlias: string;
}> = ({ open, onOpenChange, onConfirm, isLoading, modelId, modelAlias }) => {
    const confirmInputId = useId();
    const [confirmText, setConfirmText] = useState("");
    const [dependents, setDependents] = useState<ModelDependent[]>([]);
    const [isCheckingDependents, setIsCheckingDependents] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);
    const [errorReference, setErrorReference] = useState<string | null>(null);

    const loadDependents = useCallback(
        async (keepOnFailure = false) => {
            setIsCheckingDependents(true);
            try {
                setDependents(await modelsApi.getDependents(modelId));
            } catch {
                if (!keepOnFailure) setDependents([]);
            } finally {
                setIsCheckingDependents(false);
            }
        },
        [modelId]
    );

    useEffect(() => {
        if (!open) return;
        loadDependents();
    }, [open, loadDependents]);

    const handleOpenChange = (isOpen: boolean) => {
        if (!isOpen) {
            setConfirmText("");
            setDeleteError(null);
            setErrorReference(null);
        }
        onOpenChange(isOpen);
    };

    const handleConfirm = async () => {
        setDeleteError(null);
        setErrorReference(null);
        try {
            await onConfirm();
            setConfirmText("");
        } catch (error) {
            setDeleteError(getErrorMessage(error, DELETE_FAILED_MESSAGE));
            setErrorReference(error instanceof ModelKeptError ? (error.errorId ?? null) : null);
            await loadDependents(true);
            throw error;
        }
    };

    const { agents, workflows } = splitDependents(dependents);

    return (
        <ConfirmationDialog
            open={open}
            onOpenChange={handleOpenChange}
            title="Delete Model"
            actionLabels={{ confirm: "Delete" }}
            isEnabled={modelAlias.length > 0 && confirmText.trim() === modelAlias}
            isLoading={isLoading || isCheckingDependents}
            onConfirm={handleConfirm}
            content={
                <div className="flex flex-col gap-4">
                    {deleteError && (
                        <div className="flex flex-col gap-1">
                            <MessageBanner variant="error" message={deleteError} />
                            {errorReference && <p className="text-xs text-(--secondary-text-wMain)">{`Reference: ${errorReference}`}</p>}
                        </div>
                    )}

                    {isCheckingDependents && <p className="text-sm text-(--secondary-text-wMain)">Checking what uses this model...</p>}

                    {!isCheckingDependents && (
                        <div className="flex flex-col gap-3">
                            {agents.length > 0 && <AgentDependents agents={agents} />}
                            {workflows.length > 0 && <WorkflowDependents workflows={workflows} />}
                            {dependents.length === 0 && <p className="text-sm text-(--secondary-text-wMain)">If any code-based agents are referencing this model, they will no longer function correctly. This action cannot be undone.</p>}
                        </div>
                    )}

                    {!isCheckingDependents && (
                        <div className="flex flex-col gap-2">
                            <label htmlFor={confirmInputId} className="text-sm font-medium">
                                Type <strong>{modelAlias}</strong> to confirm the deletion.
                            </label>
                            <Input id={confirmInputId} value={confirmText} onChange={e => setConfirmText(e.target.value)} disabled={isLoading} autoComplete="off" data-testid="modelDeleteConfirmInput" />
                        </div>
                    )}
                </div>
            }
        />
    );
};

ModelDeleteDialog.displayName = "ModelDeleteDialog";
