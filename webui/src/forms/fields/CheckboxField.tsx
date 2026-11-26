// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Checkbox } from "@/ui/checkbox";

import { type FieldProps, FieldShell } from "../FieldShell";

export function CheckboxField({ disabled, ...shell }: Readonly<Omit<FieldProps, "placeholder">>) {
    return (
        <FieldShell {...shell} inline>
            {({ field, controlProps }) => <Checkbox {...controlProps} checked={Boolean(field.value)} onCheckedChange={field.onChange} disabled={disabled} />}
        </FieldShell>
    );
}
