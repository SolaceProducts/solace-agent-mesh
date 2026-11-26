// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { toolsetsApi, useCustomToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useNavigationBlocker } from "@/common/dialogs";
import { PageContentWrapper, PageFooter } from "@/common/layout";
import type { ProductAnalyticsErrorSource } from "@/common/product-analytics";

import { Button } from "@/ui/button";

import { ConfirmationDialog, ErrorDialog, Header, LoadingBlocker } from "@/common";

import { ToolsetEdit, type ToolsetFormData } from "./ToolsetEdit";
import { placeFieldErrors, saveToolset, type ToolsetFieldErrors, uploadToolsetFile } from "./utils";

export const ToolsetEditPage = () => {
    const { navigate, useLoaderData, addNotification, customToolsetToEdit, setCustomToolsetToEdit, setNewlyCreatedToolsetId } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: customToolsets = [], isLoading: customToolsetsLoading, refetch: customToolsetsRefetch } = useCustomToolsets();
    const { isNew, toolsetId } = useLoaderData();
    const location = useLocation();
    const droppedFile = isNew ? ((location.state as { droppedFile?: File } | null)?.droppedFile ?? null) : null;
    const [isLoading, setIsLoading] = useState(false);
    const [nameTaken, setNameTaken] = useState(false);
    const [errorObject, setErrorObject] = useState<{ title: string; message: string; errorSource: ProductAnalyticsErrorSource } | null>(null);
    const [fieldErrors, setFieldErrors] = useState<ToolsetFieldErrors>();
    const [isFormDirty, setIsFormDirty] = useState(false);
    const [reimportConfirm, setReimportConfirm] = useState<ToolsetFormData | null>(null);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    useEffect(() => {
        setBlockingEnabled(isFormDirty);
    }, [isFormDirty, setBlockingEnabled]);

    useEffect(() => {
        return () => setBlockingEnabled(false);
    }, [setBlockingEnabled]);

    const toolset = useMemo(() => {
        if (isNew) return null;
        if (customToolsetToEdit) return customToolsetToEdit;
        return customToolsets?.find(p => p.id === toolsetId) ?? null;
    }, [isNew, toolsetId, customToolsetToEdit, customToolsets]);

    const handleCancel = useCallback(() => {
        allowNavigation(() => {
            setCustomToolsetToEdit(null);
            if (isNew) {
                navigate("/toolsets");
            } else {
                navigate(-1);
            }
        });
    }, [navigate, allowNavigation, isNew, setCustomToolsetToEdit]);

    if (!userHasCapability(CAPABILITIES.createToolsets) && !userHasCapability(CAPABILITIES.updateToolsets)) {
        return <Navigate to="/toolsets" replace />;
    }

    const performSave = async (data: ToolsetFormData) => {
        setIsLoading(true);
        try {
            if (isNew) {
                const file = data.file?.[0];
                if (!file) {
                    setErrorObject({ title: "Toolset Creation Failed", message: "No file selected", errorSource: "client" });
                    setIsLoading(false);
                    return;
                }
                const createdId = await saveToolset(toolsetsApi, data.name, data.description, file);
                await customToolsetsRefetch();
                setCustomToolsetToEdit(null);
                setNewlyCreatedToolsetId(createdId);
                setBlockingEnabled(false);
                allowNavigation(() => {
                    navigate("/toolsets");
                    addNotification("Toolset created successfully", "success");
                });
            } else {
                await toolsetsApi.update(toolsetId, { name: data.name, description: data.description });
                try {
                    if (data.reimportFile?.[0]) {
                        await uploadToolsetFile(toolsetsApi, toolsetId, data.reimportFile[0]);
                    }
                } finally {
                    await customToolsetsRefetch();
                    setCustomToolsetToEdit(null);
                }
                setBlockingEnabled(false);
                allowNavigation(() => {
                    navigate(`/toolsets/${toolsetId}`);
                    addNotification("Toolset updated successfully", "success");
                });
            }
        } catch (error) {
            const { fields, dialogMessage } = placeFieldErrors(error, isNew ? "Failed to create toolset" : "Failed to update toolset");
            setFieldErrors(fields);
            if (!dialogMessage) return;
            setErrorObject({
                title: isNew ? "Toolset Creation Failed" : "Toolset Update Failed",
                message: dialogMessage,
                errorSource: "api",
            });
        } finally {
            setIsLoading(false);
        }
    };

    const boundAgentCount = toolset?.agents?.length ?? 0;

    const handleSave = async (data: ToolsetFormData) => {
        if (!isNew && data.reimportFile?.[0] && boundAgentCount > 0) {
            setReimportConfirm(data);
            return;
        }
        await performSave(data);
    };

    const title = isNew ? "Create Toolset" : `Edit ${toolset?.name ?? "Toolset"}`;
    const toolsetNotFound = !isNew && !customToolsetsLoading && !toolset;

    let formContent;
    if (customToolsetsLoading && !isNew) {
        formContent = null;
    } else if (toolsetNotFound) {
        formContent = <div className="flex h-full items-center justify-center text-(--secondary-text-wMain)">Toolset not found.</div>;
    } else {
        formContent = (
            <ToolsetEdit
                isNew={isNew}
                initialData={toolset ?? undefined}
                initialFile={droppedFile}
                onSubmit={handleSave}
                fieldErrors={fieldErrors}
                onDirtyStateChange={setIsFormDirty}
                onNameTakenChange={setNameTaken}
                onConfigSaved={() => {
                    void customToolsetsRefetch();
                }}
                onConfigError={(title, message) => setErrorObject({ title, message, errorSource: "api" })}
                onConfigSuccess={message => addNotification(message, "success")}
            />
        );
    }

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <Header title={title} breadcrumbs={[{ label: "Toolsets", onClick: () => navigate("/toolsets") }, { label: title }]} />
            <PageContentWrapper>{formContent}</PageContentWrapper>

            <PageFooter>
                <Button type="button" variant="ghost" onClick={handleCancel} data-testid="toolsetCancelButton">
                    {isNew ? "Discard Changes" : "Cancel"}
                </Button>
                <Button type="submit" form="toolset-form" disabled={isLoading || toolsetNotFound || nameTaken} data-testid="toolsetSaveButton">
                    {isNew ? "Create" : "Save"}
                </Button>
            </PageFooter>

            <LoadingBlocker isLoading={isLoading} />
            <NavigationBlocker />
            {errorObject && <ErrorDialog title={errorObject.title} open={!!errorObject} error={errorObject.message} errorSource={errorObject.errorSource} onOpenChange={open => !open && setErrorObject(null)} />}
            <ConfirmationDialog
                open={!!reimportConfirm}
                title="Reimport toolset?"
                content={
                    <p>
                        This toolset is used by <span className="font-semibold">{boundAgentCount} agent(s)</span>. Reimporting will replace its tools and redeploy those agents. Continue?
                    </p>
                }
                onOpenChange={open => !open && setReimportConfirm(null)}
                onCancel={() => setReimportConfirm(null)}
                onConfirm={() => {
                    const data = reimportConfirm;
                    setReimportConfirm(null);
                    if (data) void performSave(data);
                }}
                actionLabels={{ confirm: "Reimport" }}
            />
        </div>
    );
};
