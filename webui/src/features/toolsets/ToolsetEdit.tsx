// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { FormProvider, useForm } from "react-hook-form";
import { z } from "zod";

import { toolsetsApi, useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import { PAGE_COMMON_CLASSES, PageLabel, PageLabelWithValue, PageSection } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { getErrorMessage } from "@/utils/api";
import { isZipFile, MAX_UPLOAD_SIZE_BYTES, validateFileSizes, ZIP_INVALID_ERROR, ZIP_ONLY_ERROR } from "@/utils/fileValidation";

import { Button } from "@/ui/button";

import { FileUpload } from "@/common";
import type { Toolset } from "@/types";

import { toolsetBaseSchema } from "./schema";
import { ToolsetBuildGuide } from "./ToolsetBuildGuide";
import { ToolsetFormField } from "./ToolsetFormField";
import { ToolsetToolCards } from "./ToolsetToolCards";
import type { ToolsetFieldErrors } from "./utils";

export interface ToolsetFormData {
    name: string;
    description: string;
    file?: FileList | null;
    reimportFile?: FileList | null;
}

interface ToolsetEditProps {
    isNew?: boolean;
    initialData?: Toolset;
    initialFile?: File | null;
    onSubmit: (data: ToolsetFormData) => void;
    fieldErrors?: ToolsetFieldErrors;
    onDirtyStateChange?: (isDirty: boolean) => void;
    onNameTakenChange?: (nameTaken: boolean) => void;
    onConfigSaved?: () => void;
    onConfigError?: (title: string, message: string) => void;
    onConfigSuccess?: (message: string) => void;
}

const createSchema = (isNew: boolean) =>
    toolsetBaseSchema
        .extend({
            file: z.any().optional(),
            reimportFile: z.any().optional(),
        })
        .superRefine((data, ctx) => {
            if (isNew && !data.file?.[0]) {
                ctx.addIssue({ code: "custom", message: "Please upload a ZIP file.", path: ["file"] });
            }
        });

export const ToolsetEdit = ({ isNew = true, initialData, initialFile = null, onSubmit, fieldErrors, onDirtyStateChange, onNameTakenChange, onConfigSaved, onConfigError, onConfigSuccess }: ToolsetEditProps) => {
    const reimportInputRef = useRef<HTMLInputElement>(null);
    const [configValues, setConfigValues] = useState<Record<string, Record<string, unknown>>>({});

    const { data: builtinToolsets = [] } = useBuiltinToolsets();
    const { data: customToolsets = [] } = useCustomToolsets();

    const initialFiles = useMemo(() => {
        if (!initialFile) return null;
        const dataTransfer = new DataTransfer();
        dataTransfer.items.add(initialFile);
        return dataTransfer.files;
    }, [initialFile]);

    const methods = useForm<ToolsetFormData>({
        resolver: zodResolver(createSchema(isNew)),
        defaultValues: {
            name: initialData?.name ?? "",
            description: initialData?.description ?? "",
            file: initialFiles,
            reimportFile: null,
        },
        mode: "onSubmit",
    });

    const {
        formState: { errors, isDirty },
        setError,
        setValue,
        watch,
    } = methods;

    useEffect(() => {
        for (const [field, message] of Object.entries(fieldErrors ?? {})) {
            setError(field as keyof ToolsetFieldErrors, { type: "server", message });
        }
    }, [fieldErrors, setError]);

    const currentName = watch("name")?.trim() ?? "";
    const takenNames = new Set([...builtinToolsets, ...customToolsets].filter(t => t.id !== initialData?.id).map(t => t.name));
    const nameTaken = isNew && currentName.length > 0 && takenNames.has(currentName);

    const files = watch("file");
    const reimportFile = watch("reimportFile");

    useEffect(() => {
        onDirtyStateChange?.(isDirty);
    }, [isDirty, onDirtyStateChange]);

    useEffect(() => {
        onNameTakenChange?.(nameTaken);
    }, [nameTaken, onNameTakenChange]);

    useEffect(() => {
        if (isNew || !initialData || initialData.discoveryStatus !== "ready") return;
        const controller = new AbortController();
        (async () => {
            try {
                const resp = await toolsetsApi.getConfig(initialData.id, controller.signal);
                if (!controller.signal.aborted) setConfigValues(resp.values ?? {});
            } catch {}
        })();
        return () => controller.abort();
    }, [isNew, initialData]);

    const handleConfigSave = useCallback(
        async (toolName: string, values: Record<string, unknown>) => {
            if (!initialData) return;
            try {
                const resp = await toolsetsApi.patchConfig(initialData.id, { [toolName]: values });
                setConfigValues(resp.values ?? {});
                onConfigSuccess?.("Configuration saved");
                onConfigSaved?.();
            } catch (err) {
                onConfigError?.("Failed to save configuration", getErrorMessage(err, "Could not save tool configuration"));
            }
        },
        [initialData, onConfigSaved, onConfigError, onConfigSuccess]
    );

    const handleFileChange = (fileList: FileList | null) => {
        setValue("file", fileList, { shouldDirty: true, shouldValidate: true });
    };

    const handleValidate = async (fileList: FileList): Promise<{ valid: boolean; error?: string }> => {
        const file = fileList[0];
        if (!file.name.toLowerCase().endsWith(".zip")) {
            return { valid: false, error: ZIP_ONLY_ERROR };
        }
        const sizeCheck = validateFileSizes([file], { maxSizeBytes: MAX_UPLOAD_SIZE_BYTES });
        if (!sizeCheck.valid) {
            return { valid: false, error: sizeCheck.error };
        }
        if (!(await isZipFile(file))) {
            return { valid: false, error: ZIP_INVALID_ERROR };
        }
        return { valid: true };
    };

    const handleFormSubmit = (data: ToolsetFormData) => {
        if (nameTaken) return;
        onSubmit(data);
    };

    const formBody = (
        <FormProvider {...methods}>
            <form id="toolset-form" className={PAGE_COMMON_CLASSES} onSubmit={methods.handleSubmit(handleFormSubmit)}>
                <PageSection className="gap-6">
                    <ToolsetFormField
                        name="name"
                        fieldConfig={{
                            label: "Name",
                            required: true,
                            type: "text",
                        }}
                        error={errors.name ?? (nameTaken ? { type: "custom", message: "A toolset with this name already exists." } : undefined)}
                    />
                    <ToolsetFormField
                        name="description"
                        fieldConfig={{
                            label: "Description",
                            hint: "Explain what your toolset does in 1-2 short sentences for other users.",
                            type: "textarea",
                            maxLength: 1000,
                            required: true,
                        }}
                        error={errors.description}
                    />
                    {isNew ? (
                        <PageLabelWithValue>
                            <PageLabel required>Tools</PageLabel>
                            <div>Upload a packaged toolset file to register the tools it contains. The tools detected in the file are listed once the toolset is created.</div>
                            <FileUpload name="toolset-file" accept=".zip" value={files} onChange={handleFileChange} onValidate={handleValidate} />
                            {errors.file && <ErrorLabel message={errors.file.message as string} />}
                        </PageLabelWithValue>
                    ) : (
                        <div className="flex flex-col gap-2">
                            <div className="flex items-center justify-between">
                                <div className="text-(--secondary-text-wMain)">Tools</div>
                                <input
                                    ref={reimportInputRef}
                                    type="file"
                                    accept=".zip"
                                    className="hidden"
                                    onChange={e => {
                                        const file = e.target.files?.[0];
                                        e.target.value = "";
                                        if (file) {
                                            const dt = new DataTransfer();
                                            dt.items.add(file);
                                            setValue("reimportFile", dt.files, { shouldDirty: true });
                                        }
                                    }}
                                />
                                <Button variant="ghost" type="button" onClick={() => reimportInputRef.current?.click()}>
                                    Re-Import Toolset File
                                </Button>
                            </div>
                            {reimportFile ? (
                                <FileUpload name="toolset-reimport-file" accept=".zip" value={reimportFile} onChange={fileList => setValue("reimportFile", fileList, { shouldDirty: true })} onValidate={handleValidate} />
                            ) : (
                                <ToolsetToolCards files={initialData?.files ?? []} configValues={configValues} onConfigSave={handleConfigSave} />
                            )}
                        </div>
                    )}
                </PageSection>
            </form>
        </FormProvider>
    );

    if (!isNew) return formBody;

    return (
        <div className="grid max-w-7xl grid-cols-1 gap-6 md:grid-cols-[minmax(0,1fr)_400px] md:items-start">
            <div className="md:col-start-1">{formBody}</div>
            <div className="md:col-start-2">
                <ToolsetBuildGuide />
            </div>
        </div>
    );
};
