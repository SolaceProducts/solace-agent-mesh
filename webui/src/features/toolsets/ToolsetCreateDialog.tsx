// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { X } from "lucide-react";
import { useForm } from "react-hook-form";

import { PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import { type ToolsetBaseFormData, toolsetBaseSchema } from "./schema";

interface ToolsetCreateDialogProps {
    isOpen: boolean;
    onCreate: (name: string, description: string) => Promise<void>;
    onClose: () => void;
    filename?: string | null;
    onRemoveFile?: () => void;
}

export function ToolsetCreateDialog({ isOpen, onClose, onCreate, filename, onRemoveFile }: ToolsetCreateDialogProps) {
    const [isSubmitting, setIsSubmitting] = useState(false);

    const {
        register,
        handleSubmit,
        formState: { errors },
        reset,
    } = useForm<ToolsetBaseFormData>({
        resolver: zodResolver(toolsetBaseSchema),
        defaultValues: { name: "", description: "" },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const onSubmit = async (data: ToolsetBaseFormData) => {
        setIsSubmitting(true);
        try {
            await onCreate(data.name, data.description);
            reset();
            onClose();
        } catch {
        } finally {
            setIsSubmitting(false);
        }
    };

    const handleClose = () => {
        reset();
        onClose();
    };

    return (
        <Dialog open={isOpen} onOpenChange={handleClose}>
            <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>Create Toolset</DialogTitle>
                </DialogHeader>

                <form onSubmit={handleSubmit(onSubmit)} className="space-y-4 py-4">
                    {filename && (
                        <div className="flex items-center justify-between rounded-sm border p-3">
                            <span className="text-sm font-medium">{filename}</span>
                            <Button type="button" variant="ghost" size="sm" onClick={onRemoveFile} className="h-6 w-6 p-0" title="Remove file">
                                <X className="size-4" />
                            </Button>
                        </div>
                    )}

                    <div>
                        <PageLabel required>Name</PageLabel>
                        <Input {...register("name")} type="text" className={errors.name ? "border-(--error-wMain)" : ""} />
                        {errors.name && <ErrorLabel message={errors.name.message} />}
                    </div>

                    <div>
                        <PageLabel required>Description</PageLabel>
                        <Textarea {...register("description")} className={errors.description ? "border-(--error-wMain)" : ""} rows={3} />
                        {errors.description && <ErrorLabel message={errors.description.message} />}
                    </div>

                    <div className="flex justify-end gap-2">
                        <Button type="button" variant="ghost" onClick={handleClose}>
                            Cancel
                        </Button>
                        <Button type="submit" disabled={isSubmitting}>
                            {isSubmitting ? "Creating..." : "Create & Add to Agent"}
                        </Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    );
}
