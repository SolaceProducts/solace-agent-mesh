// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { HTMLInputAutoCompleteAttribute } from "react";

import { CopyButton } from "@/common/buttons";

import { Input } from "@/ui/input";

import { type FieldProps, FieldShell } from "../FieldShell";

export interface TextFieldProps extends FieldProps {
    autoComplete?: HTMLInputAutoCompleteAttribute;
    copyable?: boolean;
    prefix?: string;
}

function inputClass(copyable?: boolean, prefix?: string): string | undefined {
    if (prefix) return "min-w-0 flex-1 rounded-l-none";
    return copyable ? "pr-9" : undefined;
}

export function TextField({ placeholder, disabled, autoComplete, copyable, prefix, ...shell }: Readonly<TextFieldProps>) {
    return (
        <FieldShell {...shell}>
            {({ field, controlProps }) => {
                const value = typeof field.value === "string" ? field.value : "";
                const input = <Input {...field} {...controlProps} value={value} placeholder={placeholder} disabled={disabled} autoComplete={autoComplete} className={inputClass(copyable, prefix)} />;
                if (prefix) {
                    return (
                        <div className="flex">
                            <span title={prefix} className="flex max-w-1/2 min-w-0 shrink-0 items-center rounded-l-xs border border-r-0 bg-(--secondary-w10) px-2 text-sm text-(--secondary-text-wMain)">
                                <span className="truncate whitespace-nowrap">{prefix}</span>
                            </span>
                            {input}
                        </div>
                    );
                }
                if (!copyable) return input;
                return (
                    <div className="relative">
                        {input}
                        <CopyButton value={value} label={`Copy ${shell.label}`} aria-label={`Copy ${shell.label}`} disabled={value === ""} className="absolute top-1/2 right-1.5 -translate-y-1/2" />
                    </div>
                );
            }}
        </FieldShell>
    );
}
