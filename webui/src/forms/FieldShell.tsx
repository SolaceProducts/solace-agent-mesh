// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";
import { type ControllerFieldState, type ControllerRenderProps, type FieldValues, useFormContext } from "react-hook-form";

import { FormDescription, FormError, FormField, FormItem, useFormField } from "@/ui/form";

import { cn } from "@/utils";

export interface FieldProps {
    name: string;
    label: string;
    description?: string;
    docsHref?: string;
    required?: boolean;
    placeholder?: string;
    disabled?: boolean;
}

export interface ControlProps {
    id: string;
    "aria-describedby"?: string;
    "aria-invalid": boolean;
    "aria-required"?: boolean;
}

export interface FieldRenderArgs {
    field: ControllerRenderProps<FieldValues, string>;
    fieldState: ControllerFieldState;
    controlProps: ControlProps;
}

export interface FieldShellProps extends Pick<FieldProps, "name" | "label" | "description" | "docsHref" | "required"> {
    inline?: boolean;
    aside?: (value: unknown) => ReactNode;
    children: (args: FieldRenderArgs) => ReactNode;
}

type ShellBodyProps = Omit<FieldShellProps, "name" | "children"> & {
    field: ControllerRenderProps<FieldValues, string>;
    render: FieldShellProps["children"];
};

function FieldLabel({ htmlFor, label, required, className }: Readonly<{ htmlFor: string; label: string; required?: boolean; className?: string }>) {
    return (
        <label htmlFor={htmlFor} className={cn("text-sm font-medium text-(--secondary-text-wMain)", className)}>
            {label}
            {required && (
                <span aria-hidden="true" className="ml-1 text-(--brand-wMain)">
                    *
                </span>
            )}
        </label>
    );
}

function helpLineId(hasError: boolean, description: string | undefined, messageId: string, descriptionId: string): string | undefined {
    if (hasError) return messageId;
    return description ? descriptionId : undefined;
}

function helpText(hasError: boolean, description: string | undefined): ReactNode {
    if (hasError) return <FormError className="inline" />;
    return description ? <FormDescription className="inline text-xs">{description}</FormDescription> : null;
}

function ShellBody({ label, description, docsHref, required, inline, aside, field, render }: ShellBodyProps) {
    const { error, invalid, isDirty, isTouched, isValidating, formItemId, formDescriptionId, formMessageId } = useFormField();
    const docsLinkId = `${formDescriptionId}-docs`;
    const lineId = helpLineId(!!error, description, formMessageId, formDescriptionId);
    const describedBy = [lineId, docsHref ? docsLinkId : undefined].filter(Boolean).join(" ") || undefined;

    const controlProps: ControlProps = {
        id: formItemId,
        "aria-describedby": describedBy,
        "aria-invalid": !!error,
        "aria-required": required || undefined,
    };

    const text = helpText(!!error, description);
    const docsLink = docsHref && (
        <a id={docsLinkId} href={docsHref} target="_blank" rel="noopener noreferrer" className={cn("inline-flex items-center gap-0.5 text-xs text-(--primary-wMain) underline", text && "ml-1")}>
            Learn more
            <ExternalLink aria-hidden="true" className="size-3" />
            <span className="sr-only">(opens in a new tab)</span>
        </a>
    );
    const helpLine =
        text || docsLink ? (
            <div data-slot="field-help-line" className="text-xs">
                {text}
                {docsLink}
            </div>
        ) : null;
    const control = render({ field, fieldState: { error, invalid, isDirty, isTouched, isValidating }, controlProps });

    return (
        <>
            {inline ? (
                <div className="flex items-center gap-2">
                    {control}
                    <FieldLabel htmlFor={formItemId} label={label} required={required} className="cursor-pointer font-normal text-(--primary-text-wMain)" />
                </div>
            ) : (
                <>
                    <FieldLabel htmlFor={formItemId} label={label} required={required} />
                    {control}
                </>
            )}
            {aside ? (
                <div data-slot="field-help-row" className="flex items-start justify-between gap-2">
                    {helpLine ?? <span />}
                    <div className="shrink-0">{aside(field.value)}</div>
                </div>
            ) : (
                helpLine
            )}
        </>
    );
}

export function FieldShell({ name, label, description, docsHref, required, inline, aside, children }: Readonly<FieldShellProps>) {
    const { control } = useFormContext();
    return (
        <FormField
            control={control}
            name={name}
            render={({ field }) => (
                <FormItem data-testid={`field-${name}`} className={inline ? "gap-1" : undefined}>
                    <ShellBody label={label} description={description} docsHref={docsHref} required={required} inline={inline} aside={aside} field={field} render={children} />
                </FormItem>
            )}
        />
    );
}
