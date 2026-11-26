// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef } from "react";
import { useController, useFormContext, useWatch } from "react-hook-form";

import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Input } from "@/ui/input";

import type { ConnectorFieldSchema, OpenAPISpecFile } from "@/types";

interface ConnectorBaseUrlFieldProps {
    fieldPath: string;
    fieldConfig: ConnectorFieldSchema;
    fieldError: any;
}

export const ConnectorBaseUrlField: React.FC<ConnectorBaseUrlFieldProps> = ({ fieldPath, fieldConfig: { required, label, hint }, fieldError }) => {
    const { control } = useFormContext();
    const { field } = useController({ name: fieldPath, control });
    const openAPISpec: OpenAPISpecFile = useWatch({ control, name: "openAPISpec" });
    const previousSpecRef = useRef<OpenAPISpecFile | undefined>(undefined);

    useEffect(() => {
        const specChanged = (previousSpecRef.current ?? null) !== (openAPISpec ?? null);
        previousSpecRef.current = openAPISpec;
        if (!specChanged || !openAPISpec) return;

        const firstUrl = openAPISpec.servers?.[0]?.url;
        if (firstUrl && !field.value) {
            field.onChange(firstUrl);
        }
    }, [openAPISpec]);

    return (
        <PageLabelWithValue>
            <PageLabel required={required}>{label}</PageLabel>
            {hint && <div>{hint}</div>}
            <Input id={fieldPath} value={field.value ?? ""} onChange={field.onChange} onBlur={field.onBlur} data-testid="connector-field-values-base_url" />
            {fieldError && <ErrorLabel message={fieldError.message} />}
        </PageLabelWithValue>
    );
};
