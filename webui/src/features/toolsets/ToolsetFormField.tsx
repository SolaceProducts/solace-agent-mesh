// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FieldError, useController, useFormContext } from "react-hook-form";

import { CharacterCountLabel } from "@/common/form";
import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

interface FieldConfig {
    label: string;
    type: "text" | "textarea";
    required?: boolean;
    hint?: string;
    maxLength?: number;
    placeholder?: string;
}

interface ToolsetFormFieldProps {
    name: string;
    fieldConfig: FieldConfig;
    error?: FieldError;
    className?: string;
}

export const ToolsetFormField: React.FC<ToolsetFormFieldProps> = ({ name, error, className, fieldConfig }) => {
    const { hint, type, required, placeholder, label, maxLength } = fieldConfig;
    const { register } = useFormContext();
    const { field } = useController({ name });

    let inputElement;
    if (type === "textarea") {
        const currentLength = typeof field.value === "string" ? field.value.length : 0;

        inputElement = (
            <>
                <Textarea {...register(name)} className="resize-none" aria-invalid={!!error} placeholder={placeholder} rows={3} maxLength={maxLength} data-testid={`toolset-${name}`} />
                {maxLength && (
                    <div className="mt-1 flex items-center">
                        {error && <ErrorLabel className="!mt-0" message={error.message} />}
                        <div className="ml-auto">
                            <CharacterCountLabel currentLength={currentLength} maxLength={maxLength} />
                        </div>
                    </div>
                )}
            </>
        );
    } else {
        inputElement = <Input {...register(name)} type={type} aria-invalid={!!error} placeholder={placeholder} autoComplete="off" data-testid={`toolset-${name}`} />;
    }

    const showErrorBelowInput = error && !(type === "textarea" && maxLength);

    return (
        <PageLabelWithValue className={className}>
            <PageLabel required={required}>{label}</PageLabel>
            {hint && <div>{hint}</div>}
            {inputElement}
            {showErrorBelowInput && <ErrorLabel message={error.message} />}
        </PageLabelWithValue>
    );
};
