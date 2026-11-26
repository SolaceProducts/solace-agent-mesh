// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { CharacterCountLabel } from "@/common/form";
import { PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import { MAX_AGENT_DESCRIPTION_LENGTH } from "./AgentCreateFromPromptDialog";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { agentInfoSchema } from "./common";

type AgentInfoFormData = z.infer<typeof agentInfoSchema>;

interface AgentCreateDialogProps {
    isOpen: boolean;
    onCreate: (name: string, description: string) => void;
    onClose: () => void;
}

export const AgentCreateDialog: React.FC<AgentCreateDialogProps> = ({ isOpen, onClose, onCreate }) => {
    const {
        register,
        handleSubmit,
        formState: { errors },
        reset,
        watch,
    } = useForm<AgentInfoFormData>({
        resolver: zodResolver(agentInfoSchema),
        defaultValues: {
            name: "",
            description: "",
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });
    const descriptionValue = watch("description");

    const onSubmit = (data: AgentInfoFormData) => {
        onCreate(data.name, data.description);
        reset();
        onClose();
    };

    const handleClose = () => {
        reset();
        onClose();
    };

    return (
        <Dialog open={isOpen} onOpenChange={handleClose}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle className="flex max-w-[400px] flex-row gap-1">Create Agent</DialogTitle>
                    <DialogDescription className="flex flex-col gap-2">Tell us about the new agent. Give it a name and description to help others understand what it can do.</DialogDescription>
                </DialogHeader>

                <form onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} className="space-y-4 py-4">
                    <div>
                        <PageLabel required>Name</PageLabel>
                        <Input {...register("name")} type="text" data-testid="agentNameInput" className={`${errors.name ? "border-(--error-wMain)" : ""}`} />
                        {errors.name && <ErrorLabel message={errors.name.message} />}
                    </div>
                    <div>
                        <PageLabel required>Description</PageLabel>
                        <Textarea {...register("description")} data-testid="agentDescriptionInput" className={`${errors.description ? "border-(--error-wMain)" : ""}`} rows={4} />
                        {errors.description && <ErrorLabel message={errors.description.message} />}
                        {!errors.description && <CharacterCountLabel currentLength={descriptionValue?.length} maxLength={MAX_AGENT_DESCRIPTION_LENGTH} />}
                    </div>

                    <div className="flex justify-end gap-2">
                        <Button type="button" variant="ghost" onClick={handleClose}>
                            Cancel
                        </Button>
                        <Button type="submit">Continue</Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    );
};
