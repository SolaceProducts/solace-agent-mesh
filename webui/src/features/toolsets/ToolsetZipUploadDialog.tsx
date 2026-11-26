// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { toolsetsApi } from "@/api/toolsets";

import { PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { getErrorMessage } from "@/utils/api";
import { isZipFile, MAX_UPLOAD_SIZE_BYTES, validateFileSizes, ZIP_INVALID_ERROR, ZIP_ONLY_ERROR } from "@/utils/fileValidation";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/ui/dialog";

import { FileUpload } from "@/common";

interface ToolsetZipUploadDialogProps {
    isOpen: boolean;
    toolsetId: string | null;
    onClose: () => void;
    onUploaded?: () => void;
    onFileSelected?: (file: File) => void;
}

export function ToolsetZipUploadDialog({ isOpen, toolsetId, onClose, onUploaded, onFileSelected }: ToolsetZipUploadDialogProps) {
    const [file, setFile] = useState<FileList | null>(null);
    const [isUploading, setIsUploading] = useState(false);
    const [uploadError, setUploadError] = useState<string | null>(null);

    const handleClose = () => {
        setFile(null);
        setUploadError(null);
        onClose();
    };

    const handleFileChange = (files: FileList | null) => {
        setFile(files);
        setUploadError(null);
    };

    const handleValidateFile = async (files: FileList): Promise<{ valid: boolean; error?: string }> => {
        const file = files[0];
        if (!file?.name.toLowerCase().endsWith(".zip")) {
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

    const handleSubmit = async () => {
        if (!file || file.length === 0) {
            setUploadError("Please select a zip file to upload.");
            return;
        }

        if (!toolsetId && onFileSelected) {
            onFileSelected(file[0]);
            setFile(null);
            return;
        }

        if (!toolsetId) {
            setUploadError("Toolset ID is required for upload");
            return;
        }

        setIsUploading(true);
        setUploadError(null);

        try {
            await toolsetsApi.uploadBundle(toolsetId, file[0]);

            setFile(null);
            if (onUploaded) onUploaded();
            onClose();
        } catch (err: unknown) {
            setUploadError(getErrorMessage(err, "Upload failed. Please try again."));
        } finally {
            setIsUploading(false);
        }
    };

    return (
        <Dialog open={isOpen} onOpenChange={handleClose}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{toolsetId ? "Upload Zip" : "Create Toolset"}</DialogTitle>
                    <DialogDescription>
                        {toolsetId
                            ? "Upload a zip file containing your Python tools and a manifest.yaml manifest. Tools will be auto-detected from the manifest."
                            : "Custom tools go beyond the built-in catalog. Use a preferred programming language and framework, package the tool, and upload it."}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-4">
                    <div>
                        <PageLabel required>{toolsetId ? "Zip File" : "Toolset"}</PageLabel>
                        <FileUpload name="zip-file" accept=".zip" value={file} onChange={handleFileChange} onValidate={handleValidateFile} />
                        {uploadError && <ErrorLabel message={uploadError} />}
                    </div>

                    <div className="flex justify-end gap-2">
                        <Button type="button" variant="ghost" onClick={handleClose} disabled={isUploading}>
                            Cancel
                        </Button>
                        <Button type="button" onClick={handleSubmit} disabled={isUploading || !file}>
                            {isUploading ? "Uploading..." : toolsetId ? "Upload" : "Next"}
                        </Button>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
