// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { IdCard, Sparkles, Workflow as WorkflowIcon } from "lucide-react";
import { useForm } from "react-hook-form";
import { Navigate, useNavigate, useParams } from "react-router-dom";

import { useWorkflowsQuery } from "@/api/workflows";
import { useUpdateWorkflowMutation } from "@/api/workflows/hooks";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { ErrorLabel } from "@/common/status";

import { getWorkflowNodeCount, withWorkflowBodyFields, workflowBody } from "@/utils/agentUtils";
import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import { ConfirmationDialog, EmptyState, ErrorDialog, Footer, getSectionTitle, Header, LoadingBlocker, PageContentWrapper, PageLabel, PageLayout, useNavigationBlocker } from "@/common";

import { type WorkflowInfoFormData, workflowInfoSchema } from "./common";
import { toAgentCardInfo } from "./toAgentCardInfo";
import { useEditWorkflowInBuilder } from "./useEditWorkflowInBuilder";

export function WorkflowEditPage() {
    const navigate = useNavigate();
    const { id: workflowId } = useParams<{ id?: string }>();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { addNotification } = useChatContext();

    const { data: workflows = [], isLoading, error } = useWorkflowsQuery();
    const workflow = useMemo(() => workflows.find(w => w.id === workflowId), [workflows, workflowId]);

    const updateWorkflow = useUpdateWorkflowMutation();
    const editInBuilder = useEditWorkflowInBuilder();
    const [errorObject, setErrorObject] = useState<{ title: string; message: string } | null>(null);
    const [confirmEditWithAi, setConfirmEditWithAi] = useState(false);
    const { NavigationBlocker, allowNavigation, setBlockingEnabled } = useNavigationBlocker();

    const {
        register,
        handleSubmit,
        formState: { errors, isDirty },
    } = useForm<WorkflowInfoFormData>({
        resolver: zodResolver(workflowInfoSchema),
        values: {
            name: workflow?.name ?? "",
            description: ((workflowBody(workflow?.appConfig).description as string) || workflow?.description) ?? "",
            version: (workflowBody(workflow?.appConfig).version as string) ?? "",
        },
        resetOptions: { keepDirtyValues: true },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    useEffect(() => setBlockingEnabled(isDirty), [isDirty, setBlockingEnabled]);

    if (!userHasCapability(CAPABILITIES.updateWorkflows)) {
        return <Navigate to="/workflows" replace />;
    }

    if (isLoading) {
        return <EmptyState variant="loading" title="Loading workflow..." />;
    }

    if (error) {
        return <EmptyState variant="error" title="Error loading workflow" subtitle={error.message} buttons={[{ text: "Go To Workflows", variant: "default", onClick: () => navigate("/workflows") }]} />;
    }

    if (!workflow) {
        return <EmptyState variant="error" title="Workflow Not Found" buttons={[{ text: "Go To Workflows", variant: "default", onClick: () => navigate("/workflows") }]} />;
    }

    const title = `Edit ${workflow.displayName || workflow.name}`;
    const nodeCount = getWorkflowNodeCount(toAgentCardInfo(workflow));
    const pluralizedNodeCount = `${nodeCount} node${nodeCount === 1 ? "" : "s"}`;
    const nodeCountLabel = nodeCount > 0 ? pluralizedNodeCount : "No nodes";

    const onSubmit = async (data: WorkflowInfoFormData) => {
        try {
            await updateWorkflow.mutateAsync({
                id: workflow.id,
                payload: {
                    name: data.name,
                    description: data.description,
                    appConfig: withWorkflowBodyFields(workflow.appConfig, { description: data.description, version: data.version }),
                },
            });
            addNotification("Workflow updated successfully.", "success");
            allowNavigation(() => navigate("/workflows"));
        } catch (err) {
            setErrorObject({ title: "Workflow Update Failed", message: getErrorMessage(err, "An error occurred. Please try again.") });
        }
    };

    return (
        <PageLayout className="min-[896px]:min-w-4xl">
            <Header title={title} breadcrumbs={[{ label: "Workflows", onClick: () => navigate("/workflows") }, { label: title }]} />

            <PageContentWrapper>
                <form id="workflow-edit-form" onSubmit={handleSubmit(onSubmit)}>
                    <div className="mb-8">
                        <div className="mb-4">{getSectionTitle(IdCard, "Workflow Details")}</div>
                        <div className="space-y-6">
                            <div>
                                <PageLabel required>Name</PageLabel>
                                <Input data-testid="workflow-name" {...register("name")} type="text" aria-invalid={!!errors.name} />
                                {errors.name && <ErrorLabel message={errors.name.message} />}
                            </div>

                            <div>
                                <PageLabel required>Description</PageLabel>
                                <Textarea data-testid="workflow-description" {...register("description")} className="resize-none" aria-invalid={!!errors.description} rows={4} />
                                {errors.description && <ErrorLabel message={errors.description.message} />}
                            </div>

                            <div>
                                <PageLabel>Version</PageLabel>
                                <Input data-testid="workflow-version" {...register("version")} type="text" placeholder="e.g. 1.0.0" aria-invalid={!!errors.version} />
                                {errors.version && <ErrorLabel message={errors.version.message} />}
                            </div>
                        </div>
                    </div>

                    <div>
                        <div className="mb-4">{getSectionTitle(WorkflowIcon, "Configuration")}</div>
                        <div className="flex items-center justify-between gap-4 rounded-xs bg-(--secondary-w10) p-4">
                            <div className="text-sm text-(--secondary-text-wMain)">{nodeCountLabel}. Edit the workflow's steps, schemas and logic with AI in the builder.</div>
                            <Button type="button" variant="outline" size="sm" onClick={() => (isDirty ? setConfirmEditWithAi(true) : editInBuilder(toAgentCardInfo(workflow)))}>
                                <Sparkles className="h-3.5 w-3.5" />
                                Edit with AI
                            </Button>
                        </div>
                    </div>
                </form>
            </PageContentWrapper>

            <Footer>
                <Button variant="ghost" title="Cancel" onClick={() => allowNavigation(() => navigate("/workflows"))} disabled={updateWorkflow.isPending}>
                    Cancel
                </Button>
                <Button type="submit" form="workflow-edit-form" title="Save" disabled={updateWorkflow.isPending}>
                    {updateWorkflow.isPending ? "Saving..." : "Save"}
                </Button>
            </Footer>

            {errorObject && <ErrorDialog open={!!errorObject} title={errorObject.title} error={errorObject.message} errorSource="api" onOpenChange={open => !open && setErrorObject(null)} />}
            <LoadingBlocker isLoading={updateWorkflow.isPending} />

            <NavigationBlocker />
            <ConfirmationDialog
                open={confirmEditWithAi}
                title="Unsaved Changes Will Be Discarded"
                description="Leaving to edit with AI will discard any unsaved changes to the workflow details."
                actionLabels={{ cancel: "Return to Form", confirm: "Leave & Discard Changes" }}
                onOpenChange={setConfirmEditWithAi}
                onConfirm={() => {
                    setConfirmEditWithAi(false);
                    allowNavigation(() => editInBuilder(toAgentCardInfo(workflow)));
                }}
                onCancel={() => setConfirmEditWithAi(false)}
            />
        </PageLayout>
    );
}
