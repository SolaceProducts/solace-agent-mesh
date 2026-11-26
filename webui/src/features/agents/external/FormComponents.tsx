// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Controller } from "react-hook-form";

import { HelpPopover } from "@/common/HelpPopover";
import { PageLabel } from "@/common/layout";
import { ErrorLabel, WarningLabel } from "@/common/status";

import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

export interface FormFieldProps {
    label: string;
    required?: boolean;
    error?: { message?: string };
    warning?: string;
    helpText?: string;
    labelHelp?: string;
    statusIndicator?: React.ReactNode;
    children: React.ReactNode;
}

export interface TextInputProps {
    name: string;
    label: string;
    register: any;
    error?: { message?: string };
    warning?: string;
    required?: boolean;
    helpText?: string;
    disabled?: boolean;
    readOnly?: boolean;
    statusIndicator?: React.ReactNode;
    onBlur?: () => void;
}

export interface SelectOption {
    value: string;
    label: string;
    description?: string;
}

export interface SelectFieldProps {
    name: string;
    label: string;
    control: any;
    options: SelectOption[];
    error?: { message?: string };
    required?: boolean;
    trigger?: any;
    onValueChange?: (value: string) => void;
}

export const FormField = ({ label, required = false, error, warning, helpText, labelHelp, statusIndicator, children }: FormFieldProps) => (
    <div className="grid grid-cols-[200px_1fr] items-start gap-4">
        {labelHelp ? (
            <div className="flex items-center gap-1">
                <PageLabel required={required}>{label}</PageLabel>
                <HelpPopover label={`${label} help`} content={labelHelp} size="16" side="right" align="start" />
            </div>
        ) : (
            <PageLabel required={required}>{label}</PageLabel>
        )}
        <div className="flex items-start gap-2">
            <div className="flex-1">
                {children}
                {(error || warning || helpText) && (
                    <div className="mt-1">
                        {error && <ErrorLabel message={error.message} />}
                        {warning && <WarningLabel>{warning}</WarningLabel>}
                        {helpText && <div className="text-xs text-(--secondary-text-wMain)">{helpText}</div>}
                    </div>
                )}
            </div>
            {statusIndicator}
        </div>
    </div>
);

export const TextInput = ({ name, label, register, error, warning, required = false, helpText, disabled = false, readOnly = false, statusIndicator, onBlur }: TextInputProps) => (
    <FormField label={label} required={required} error={error} warning={warning} helpText={helpText} statusIndicator={statusIndicator}>
        <Input {...register(name)} type="text" autoComplete="off" disabled={disabled} readOnly={readOnly} className={readOnly ? "bg-(--secondary-w10)" : undefined} onBlur={onBlur} aria-invalid={!!error} data-testid={`externalAgentInput-${name}`} />
    </FormField>
);

export const SelectField = ({ name, label, control, options, error, required = false, trigger, onValueChange }: SelectFieldProps) => {
    const labelMap = useMemo(() => {
        return options.reduce((acc, opt) => ({ ...acc, [opt.value]: opt.label }), {} as Record<string, string>);
    }, [options]);

    return (
        <FormField label={label} required={required} error={error}>
            <Controller
                name={name}
                control={control}
                render={({ field }) => (
                    <Select
                        value={field.value || ""}
                        onValueChange={value => {
                            field.onChange(value);
                            onValueChange?.(value);
                            trigger?.(name);
                        }}
                    >
                        <SelectTrigger className="min-h-[38px] w-full" data-testid={`externalAgentSelect-${name}`}>
                            <SelectValue>{field.value ? labelMap[field.value] : undefined}</SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                            {options.map(option => (
                                <SelectItem key={option.value} value={option.value} data-testid={`externalAgentSelect-${name}-option-${option.value}`}>
                                    <div className="flex flex-col">
                                        <div>{option.label}</div>
                                        {option.description && <div className="text-xs text-(--secondary-text-wMain)">{option.description}</div>}
                                    </div>
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            />
        </FormField>
    );
};
