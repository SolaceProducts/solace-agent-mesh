// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";
import { useController, useFormContext } from "react-hook-form";

import { PasswordInput } from "@/common/form";
import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Textarea } from "@/ui/textarea";

import type { ConnectorFieldSchema } from "@/types";

import { useStoredSecrets } from "./ConnectorEdit";
import { useIsFieldHighlighted } from "./HighlightFieldsContext";

interface ConnectorFormFieldProps {
    name: string;
    fieldConfig: ConnectorFieldSchema;
    error?: any;
    className?: string;
    disabled?: boolean;
}

export const ConnectorFormField: React.FC<ConnectorFormFieldProps> = ({ name, error, className, fieldConfig, disabled }) => {
    const { hint, type, required, placeholder, options, label, maxLength } = fieldConfig;
    const { register, control } = useFormContext();
    const { field } = useController({ name });
    const storedSecrets = useStoredSecrets();
    const isHighlighted = useIsFieldHighlighted(name);

    return useMemo(() => {
        const registerOptions = type === "number" ? { valueAsNumber: true } : {};

        let inputElement;
        if (options && options.length > 0) {
            inputElement = (
                <Select value={field.value} onValueChange={field.onChange} disabled={disabled}>
                    <SelectTrigger id={name} data-testid={`connector-field-${name.replaceAll(".", "-")}`}>
                        <SelectValue placeholder={placeholder ?? "Select an option from the list"} />
                    </SelectTrigger>
                    <SelectContent>
                        {options.map(opt => {
                            const value = typeof opt === "string" ? opt : opt.value;
                            const label = typeof opt === "string" ? opt : opt.label;
                            const description = typeof opt === "string" ? "" : opt.description;
                            return (
                                <SelectItem key={value} value={value} description={description}>
                                    {label}
                                </SelectItem>
                            );
                        })}
                    </SelectContent>
                </Select>
            );
        } else if (type === "textarea") {
            const currentLength = typeof field.value === "string" ? field.value.length : 0;
            const remainingChars = maxLength ? maxLength - currentLength : 0;
            const isOverLimit = remainingChars < 0;

            inputElement = (
                <>
                    <Textarea {...register(name, registerOptions)} className="resize-none" aria-invalid={!!error} placeholder={placeholder} rows={3} disabled={disabled} data-testid={`connector-field-${name.replaceAll(".", "-")}`} />
                    {maxLength && (
                        <div className="mt-1 flex items-center justify-between text-xs">
                            {error && <ErrorLabel className="!mt-0" message={error.message} />}
                            <div className={`ml-auto ${isOverLimit ? "text-(--error-wMain)" : "text-(--secondary-text-wMain)"}`}>{remainingChars}</div>
                        </div>
                    )}
                </>
            );
        } else if (type === "password") {
            inputElement = <PasswordInput name={name} control={control} hasStoredValue={storedSecrets.has(name)} placeholder={placeholder} rules={{ required }} invalid={!!error} />;
        } else {
            inputElement = <Input {...register(name, registerOptions)} type={type} aria-invalid={!!error} placeholder={placeholder} disabled={disabled} autoComplete="off" data-testid={`connector-field-${name.replaceAll(".", "-")}`} />;
        }

        const showErrorBelowInput = error && !(type === "textarea" && maxLength);

        const containerTestId = type === "password" ? `connector-field-${name.replaceAll(".", "-")}` : undefined;

        return (
            <PageLabelWithValue className={className} data-testid={containerTestId}>
                <PageLabel required={required}>{label}</PageLabel>
                {hint && <div>{hint}</div>}
                {inputElement}
                {showErrorBelowInput && <ErrorLabel message={error.message} />}
            </PageLabelWithValue>
        );
    }, [className, hint, error, label, required, name, register, placeholder, type, field, maxLength, control, storedSecrets, isHighlighted, disabled, options]);
};
