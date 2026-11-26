// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CharacterCountLabel } from "@/common/form";

import { Textarea } from "@/ui/textarea";

import { type FieldProps, FieldShell } from "../FieldShell";

export interface TextAreaFieldProps extends FieldProps {
    maxLength?: number;
    rows?: number;
}

export function TextAreaField({ placeholder, disabled, maxLength, rows, ...shell }: Readonly<TextAreaFieldProps>) {
    const counter = maxLength === undefined ? undefined : (value: unknown) => <CharacterCountLabel currentLength={typeof value === "string" ? value.length : 0} maxLength={maxLength} />;
    return (
        <FieldShell {...shell} aside={counter}>
            {({ field, controlProps }) => <Textarea {...field} {...controlProps} value={field.value ?? ""} placeholder={placeholder} disabled={disabled} maxLength={maxLength} rows={rows} />}
        </FieldShell>
    );
}
