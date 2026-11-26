// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Sparkles } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { api } from "@/api/client";

import { CharacterCountLabel, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";
import { ErrorLabel } from "@/common/status";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Textarea } from "@/ui/textarea";

import { MessageBanner } from "@/common";
import type { Agent } from "@/types";

import { AgentCreateFromPromptHelp } from "./AgentCreateFromPromptHelp";

interface AgentCreateFromPromptDialogProps {
    isOpen: boolean;
    prompt?: string;
    isRegenerating?: boolean;
    onCreate: (agent: Agent, originalPrompt?: string) => void;
    onClose: () => void;
    onSkip?: () => void;
}

export const MAX_AGENT_DESCRIPTION_LENGTH = 1000;
const promptDescriptionSchema = z.object({
    description: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(10, "Value must be at least 10 characters")
                .max(MAX_AGENT_DESCRIPTION_LENGTH, `Value must be ${MAX_AGENT_DESCRIPTION_LENGTH} characters or less`)
                .refine(val => SAFE_TEXT_PATTERN.test(val), {
                    message: SAFE_TEXT_DESCRIPTION,
                })
        ),
});

type PromptDescriptionFormData = z.infer<typeof promptDescriptionSchema>;

export const AgentCreateFromPromptDialog: React.FC<AgentCreateFromPromptDialogProps> = ({ isOpen, prompt = "", isRegenerating = false, onClose, onCreate, onSkip }) => {
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const {
        register,
        handleSubmit,
        formState: { errors },
        reset,
        setFocus,
        watch,
    } = useForm<PromptDescriptionFormData>({
        resolver: zodResolver(promptDescriptionSchema),
        defaultValues: {
            description: prompt || "",
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });
    const descriptionValue = watch("description");

    useEffect(() => {
        if (isOpen) {
            setFocus("description");
        }
    }, [isOpen, setFocus]);

    useEffect(() => {
        if (prompt) {
            reset({ description: prompt });
        }
    }, [prompt, reset]);

    const onSubmit = async (data: PromptDescriptionFormData) => {
        setIsLoading(true);
        setError(null);

        try {
            const newAgent: Agent = await api.platform.post("/api/internal/platform/aiAssistant/generateAgent", { description: data.description });
            onCreate(newAgent, data.description);
            reset();
            onClose();
        } catch (err: unknown) {
            setError(getErrorMessage(err, "Could not generate agent. Try a new description."));
        } finally {
            setIsLoading(false);
        }
    };

    const handleClose = () => {
        if (!isLoading) {
            reset();
            setError(null);
            onClose();
        }
    };

    const handleSkip = () => {
        if (!isLoading) {
            reset();
            setError(null);
            onSkip?.();
        }
    };

    return (
        <Dialog open={isOpen} onOpenChange={isLoading ? undefined : handleClose}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>{isRegenerating ? "Regenerate Agent" : "Create Agent"}</DialogTitle>
                    {isRegenerating && <MessageBanner className="mt-2" variant="info" message="Regenerating may not keep any of the existing agent. If you want to make minor adjustments, we recommend editing the agent directly." />}
                    {error && <MessageBanner variant="error" className="max-h-fit" message={error} dismissible onDismiss={() => setError(null)} />}
                    <DialogDescription className="max-w-full py-2">Describe the problems your agent should solve and we'll generate a starting point. The more specific your use cases, the better the result.</DialogDescription>
                </DialogHeader>

                <div className="flex gap-6">
                    <div className="flex-1">
                        <form onSubmit={handleSubmit(onSubmit)} className="flex h-full flex-col space-y-4">
                            <div className="flex flex-1 flex-col gap-4 sm:flex-row">
                                <div className="flex min-w-0 flex-1 flex-col">
                                    <Textarea {...register("description")} data-testid="agent-prompt" className={`h-[200px] resize-none sm:h-[350px] ${isLoading ? "opacity-50" : ""}`} aria-invalid={!!errors.description} disabled={isLoading} />
                                    {errors.description && <ErrorLabel message={errors.description.message} />}
                                    {!errors.description && <CharacterCountLabel currentLength={descriptionValue?.length} maxLength={MAX_AGENT_DESCRIPTION_LENGTH} />}
                                </div>
                                <div className="sm:flex-shrink-0">
                                    <AgentCreateFromPromptHelp />
                                </div>
                            </div>

                            <div className="flex flex-wrap justify-between gap-2">
                                {!isRegenerating && (
                                    <Button testid="createManually" type="button" variant="ghost" onClick={handleSkip} disabled={isLoading}>
                                        Create Manually
                                    </Button>
                                )}
                                <div className="ml-auto flex gap-2">
                                    <Button testid="cancelAgent" type="button" variant="ghost" onClick={handleClose} disabled={isLoading}>
                                        Cancel
                                    </Button>
                                    <Button testid="generateAgent" type="submit" disabled={isLoading} className={isLoading ? "pointer-events-none" : ""}>
                                        <Sparkles />
                                        {isRegenerating ? "Regenerate" : "Generate"}
                                    </Button>
                                </div>
                            </div>
                        </form>
                    </div>
                </div>

                {isLoading && (
                    <>
                        <style>{`
                        @keyframes progressBarSlide {
                            0% { transform: translateX(-100%); }
                            100% { transform: translateX(400%); }
                        }
                        .progress-bar-animate {
                            animation: progressBarSlide 2s ease-in-out infinite;
                            width: 25%;
                            background: var(--brand-wMain);
                        }
                        `}</style>
                        <div className="absolute right-1 bottom-0 left-1 h-1 overflow-hidden rounded-full bg-(--secondary-w10)">
                            <div className="progress-bar-animate h-full rounded-full"></div>
                        </div>
                    </>
                )}
            </DialogContent>
        </Dialog>
    );
};
