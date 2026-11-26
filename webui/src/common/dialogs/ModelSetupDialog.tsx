// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { lazy, Suspense, useEffect, useState } from "react";
import { Bot } from "lucide-react";

import type { ModelConfig } from "@/api/models/types";

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui";

const ModelSetupForm = lazy(() => import("./ModelSetupForm"));

interface ModelSetupDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    hasWritePermissions: boolean;
    onSuccess?: () => void;
    setupCopy?: { title: string; description: string; submitLabel?: string };
    replaces?: (model: ModelConfig) => boolean;
}

export function ModelSetupDialog({ open, onOpenChange, hasWritePermissions, onSuccess, setupCopy, replaces }: Readonly<ModelSetupDialogProps>) {
    const [formKey, setFormKey] = useState(0);
    useEffect(() => {
        if (open) setFormKey(k => k + 1);
    }, [open]);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex max-h-[80vh] flex-col overflow-hidden sm:max-w-2xl">
                <DialogHeader className={hasWritePermissions ? "flex-shrink-0" : "min-h-0 overflow-y-auto"}>
                    <DialogTitle>{hasWritePermissions ? (setupCopy?.title ?? "Configure Your AI Models") : "No Default LLM Models Available"}</DialogTitle>
                    <DialogDescription className="mb-3 flex flex-col space-y-3">
                        {hasWritePermissions ? (
                            (setupCopy?.description ?? <FirstRunDescription />)
                        ) : (
                            <>
                                <span>This platform offers powerful AI-enabled features like chatting with AI, agent creation and other helpful AI capabilities. Some features may not work as intended without configured models.</span>
                                <span>
                                    <strong>Contact an administrator</strong> to set up models for your agent mesh, or continue exploring the available features.
                                </span>
                            </>
                        )}
                    </DialogDescription>
                </DialogHeader>

                {hasWritePermissions ? (
                    <Suspense fallback={null}>
                        <ModelSetupForm key={formKey} onSuccess={onSuccess} onClose={() => onOpenChange(false)} replaces={replaces} submitLabel={setupCopy?.submitLabel} />
                    </Suspense>
                ) : (
                    <DialogFooter className="flex-shrink-0">
                        <Button variant="outline" onClick={() => onOpenChange(false)}>
                            Close
                        </Button>
                    </DialogFooter>
                )}
            </DialogContent>
        </Dialog>
    );
}

function FirstRunDescription() {
    return (
        <span className="flex inline items-center gap-1.5">
            Enter your LLM details to set up your built-in General and Planning models in one step — unlocking AI chat, agent creation, and more. These can be customized independently later. Or explore first and configure later in
            <Bot className="mx-1 inline size-4 shrink-0" />
            <strong>Agent Mesh</strong>.
        </span>
    );
}
