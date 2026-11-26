// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useRef } from "react";
import { NotepadText } from "lucide-react";

import type { TemplateConfig } from "@/types";
import { Badge, CardTitle, Label } from "@/ui";

interface TemplatePreviewPanelProps {
    config: TemplateConfig;
    highlightedFields: string[];
    isReadyToSave: boolean;
}

export const TemplatePreviewPanel: React.FC<TemplatePreviewPanelProps> = ({ config, highlightedFields }) => {
    const hasContent = config.promptText && config.promptText.trim().length > 0;

    const updateCountRef = useRef(0);
    const lastHighlightedFieldsRef = useRef<string[]>([]);

    const highlightedFieldsChanged = highlightedFields.length > 0 && (highlightedFields.length !== lastHighlightedFieldsRef.current.length || highlightedFields.some((f, i) => f !== lastHighlightedFieldsRef.current[i]));

    if (highlightedFieldsChanged && hasContent) {
        updateCountRef.current += 1;
        lastHighlightedFieldsRef.current = [...highlightedFields];
    }

    const showBadges = updateCountRef.current >= 2 && highlightedFields.length > 0;

    const renderField = (label: string, value: string | undefined, fieldName: string, isCommand: boolean = false) => {
        const isHighlighted = highlightedFields.includes(fieldName);
        const isEmpty = !value || value.trim().length === 0;

        return (
            <div className="space-y-2">
                <div className="flex items-center gap-2">
                    <Label className="text-sm font-medium text-(--secondary-text-wMain)">{label}</Label>
                    {isHighlighted && showBadges && (
                        <Badge variant="default" className="bg-(--primary-wMain) text-xs text-(--primary-text-w10)">
                            Updated
                        </Badge>
                    )}
                </div>
                {isCommand ? (
                    <div className="rounded p-3 text-sm">{isEmpty ? <span className="text-(--secondary-text-wMain) italic">No {label.toLowerCase()} yet</span> : <span className="font-mono text-(--primary-wMain)">/{value}</span>}</div>
                ) : (
                    <div className="rounded p-3 text-sm">{isEmpty ? <span className="text-(--secondary-text-wMain) italic">No {label.toLowerCase()} yet</span> : value}</div>
                )}
            </div>
        );
    };

    const renderPromptText = () => {
        const isHighlighted = highlightedFields.includes("promptText");
        const isEmpty = !config.promptText || config.promptText.trim().length === 0;

        const highlightVariables = (text: string) => {
            const parts = text.split(/(\{\{[^}]+\}\})/g);
            return parts.map((part, index) => {
                if (part.match(/\{\{[^}]+\}\}/)) {
                    return (
                        <span key={index} className="rounded bg-(--primary-w20) px-1 font-medium text-(--info-wMain)">
                            {part}
                        </span>
                    );
                }
                return <span key={index}>{part}</span>;
            });
        };

        return (
            <div className="space-y-2">
                <div className="flex items-center gap-2">
                    {isHighlighted && showBadges && (
                        <Badge variant="default" className="bg-(--primary-wMain) text-xs text-(--primary-text-w10)">
                            Updated
                        </Badge>
                    )}
                </div>
                <div className="min-h-[288px] w-full rounded-md px-3 py-2 font-mono text-sm whitespace-pre-wrap">
                    {isEmpty ? <span className="text-(--secondary-text-wMain) italic">No prompt text yet</span> : highlightVariables(config.promptText!)}
                </div>
            </div>
        );
    };

    const renderVariables = () => {
        const variables = config.detectedVariables || [];

        if (variables.length === 0) {
            return <div className="py-2 text-sm text-(--secondary-text-wMain) italic">No variables detected yet</div>;
        }

        return (
            <div className="py-2">
                <div className="flex flex-wrap gap-2">
                    {variables.map((variable, index) => (
                        <span key={index} className="rounded bg-(--primary-w10) px-2 py-1 font-mono text-xs text-(--primary-wMain)">
                            {`{{${variable}}}`}
                        </span>
                    ))}
                </div>
            </div>
        );
    };

    return (
        <div className="flex h-full flex-col">
            <div className="hidden border-b px-4 py-3 sm:block">
                <div className="flex items-center gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-(--secondary-w10)">
                        <NotepadText className="h-4 w-4 text-(--secondary-text-wMain)" />
                    </div>
                    <div>
                        <h3 className="text-sm font-semibold">Preview</h3>
                    </div>
                </div>
            </div>

            <div className="flex-1 space-y-4 overflow-y-auto px-4" style={{ paddingTop: "24px" }}>
                {!hasContent ? (
                    <div className="flex h-full flex-col items-center justify-center p-8 text-center">
                        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-(--secondary-w10)">
                            <NotepadText className="h-8 w-8 text-(--secondary-text-wMain)" />
                        </div>
                        <h3 className="mb-2 text-lg font-semibold">No Template Yet</h3>
                        <p className="max-w-sm text-sm text-(--secondary-text-wMain)">Start chatting with the AI assistant to create your template. The preview will update in real-time as you describe your task.</p>
                    </div>
                ) : (
                    <>
                        <div>
                            <CardTitle className="mb-4 text-base">Basic Information</CardTitle>
                            <div className="space-y-6">
                                {renderField("Name", config.name, "name")}
                                {renderField("Description", config.description, "description")}
                                {renderField("Tag", config.category, "category")}
                                {renderField("Chat Shortcut", config.command, "command", true)}
                            </div>
                        </div>

                        <div>
                            <CardTitle className="mb-4 text-base">Content</CardTitle>
                            {renderPromptText()}
                        </div>

                        <div>
                            <CardTitle className="mb-4 text-base">Variables</CardTitle>
                            <div className="space-y-3">
                                {config.detectedVariables && config.detectedVariables.length > 0 ? (
                                    <>
                                        <p className="text-sm leading-relaxed text-(--secondary-text-wMain)">
                                            Variables are placeholder values that make your prompt flexible and reusable. Variables are enclosed in double brackets like{" "}
                                            <code className="rounded bg-(--secondary-w10) px-1.5 py-0.5 font-mono text-xs">{"{{Variable Name}}"}</code>. You will be asked to fill in these variable values whenever you use this prompt. The prompt above
                                            has the following variables:
                                        </p>
                                        {renderVariables()}
                                    </>
                                ) : (
                                    <div className="rounded-lg bg-(--secondary-w10) p-3 text-sm text-(--secondary-text-wMain) italic">No variables detected yet</div>
                                )}
                            </div>
                        </div>
                    </>
                )}
            </div>
        </div>
    );
};
