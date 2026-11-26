// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { FieldFooter } from "@/ui/fieldFooter";

import { Input, Textarea } from "@/ui";

import { projectTextLength } from "./validation";

interface ProjectDetailsFieldsProps {
    idPrefix: string;
    name: string;
    onNameChange: (value: string) => void;
    nameError?: string | null;
    description: string;
    onDescriptionChange: (value: string) => void;
    maxNameLength: number;
    maxDescriptionLength: number;
    isDescriptionOverLimit: boolean;
    disabled?: boolean;
    header?: ReactNode;
}

export const ProjectDetailsFields = ({ idPrefix, name, onNameChange, nameError, description, onDescriptionChange, maxNameLength, maxDescriptionLength, isDescriptionOverLimit, disabled = false, header }: ProjectDetailsFieldsProps) => {
    const descriptionLength = projectTextLength(description);

    return (
        <div className="space-y-4 py-4">
            {header}
            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-name`} className="text-sm font-medium">
                    Project Name <span className="text-(--brand-wMain)">*</span>
                </label>
                <Input
                    id={`${idPrefix}-name`}
                    value={name}
                    onChange={e => onNameChange(e.target.value)}
                    placeholder="Project name"
                    disabled={disabled}
                    maxLength={maxNameLength}
                    aria-invalid={!!nameError}
                    aria-describedby={nameError ? `${idPrefix}-name-error` : undefined}
                />
                {nameError && (
                    <div id={`${idPrefix}-name-error`} className="text-xs text-(--error-wMain)">
                        {nameError}
                    </div>
                )}
            </div>
            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-description`} className="text-sm font-medium">
                    Description
                </label>
                <Textarea
                    id={`${idPrefix}-description`}
                    value={description}
                    onChange={e => onDescriptionChange(e.target.value)}
                    placeholder="Project description"
                    rows={4}
                    disabled={disabled}
                    aria-invalid={isDescriptionOverLimit}
                    aria-describedby={`${idPrefix}-description-counter`}
                    className="resize-none text-sm"
                />
                <FieldFooter
                    id={`${idPrefix}-description-counter`}
                    hasError={isDescriptionOverLimit}
                    message={`${descriptionLength} / ${maxDescriptionLength}`}
                    error={`Description must be ${maxDescriptionLength} characters or fewer (currently ${descriptionLength})`}
                />
            </div>
        </div>
    );
};
