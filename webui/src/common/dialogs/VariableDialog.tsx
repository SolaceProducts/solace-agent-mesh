// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";

import { detectVariables, replaceVariables } from "@/utils/promptUtils";

import type { PromptGroup } from "@/types/prompts";

import { Button } from "@/ui";

import { MessageBanner } from "../banners/MessageBanner";

interface VariableDialogProps {
    group: PromptGroup;
    onSubmit: (processedPrompt: string) => void;
    onClose: () => void;
}

export const VariableDialog: React.FC<VariableDialogProps> = ({ group, onSubmit, onClose }) => {
    const promptText = group.productionPrompt?.promptText || "";
    const variables = detectVariables(promptText);

    const [values, setValues] = useState<Record<string, string>>(() => {
        const initial: Record<string, string> = {};
        variables.forEach(v => {
            initial[v] = "";
        });
        return initial;
    });
    const [showError, setShowError] = useState(false);
    const firstInputRef = useRef<HTMLTextAreaElement>(null);

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();

        const allFilled = variables.every(v => values[v]?.trim());
        if (!allFilled) {
            setShowError(true);
            setTimeout(() => setShowError(false), 3000);
            return;
        }

        const processedPrompt = replaceVariables(promptText, values);
        onSubmit(processedPrompt);
    };

    useEffect(() => {
        if (firstInputRef.current) {
            firstInputRef.current.focus();
        }
    }, []);

    useEffect(() => {
        const handleEscape = (e: KeyboardEvent) => {
            if (e.key === "Escape") {
                onClose();
            }
        };

        window.addEventListener("keydown", handleEscape);
        return () => window.removeEventListener("keydown", handleEscape);
    }, [onClose]);

    return (
        <div className="overlay-backdrop fixed inset-0 z-50 flex items-center justify-center p-4">
            <div className="flex max-h-[80vh] w-full max-w-lg flex-col rounded-lg border bg-(--background-w10) shadow-lg">
                <div className="flex-shrink-0 p-6 pb-4">
                    <h2 className="text-lg font-semibold">Insert {group.name}</h2>
                    <p className="mt-1 text-sm text-(--secondary-text-wMain)">Variables represent placeholder information in the template. Enter a value for each placeholder below.</p>
                </div>

                {showError && (
                    <div className="flex-shrink-0 px-6">
                        <MessageBanner variant="error" message="Please fill in all variables before inserting the prompt" />
                    </div>
                )}

                <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
                    <div className="flex-1 overflow-y-auto px-6 py-4">
                        <div className="space-y-4">
                            {variables.map((variable, index) => (
                                <div key={variable}>
                                    <label htmlFor={`var-${variable}`} className="mb-1 block text-sm font-medium">
                                        {variable}
                                    </label>
                                    <textarea
                                        id={`var-${variable}`}
                                        ref={index === 0 ? firstInputRef : null}
                                        value={values[variable]}
                                        onChange={e =>
                                            setValues(prev => ({
                                                ...prev,
                                                [variable]: e.target.value,
                                            }))
                                        }
                                        className="min-h-[80px] w-full rounded-md border bg-(--background-w10) p-2 text-sm focus:ring-2 focus:ring-(--primary-wMain) focus:outline-none"
                                        required
                                    />
                                </div>
                            ))}
                        </div>
                    </div>

                    <div className="flex flex-shrink-0 justify-end gap-2 border-t p-6 pt-4">
                        <Button type="button" variant="ghost" onClick={onClose}>
                            Cancel
                        </Button>
                        <Button type="submit">Insert Prompt</Button>
                    </div>
                </form>
            </div>
        </div>
    );
};
