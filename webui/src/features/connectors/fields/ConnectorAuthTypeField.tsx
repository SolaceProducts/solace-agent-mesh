// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useRef, useState } from "react";
import { useController, useFormContext, useWatch } from "react-hook-form";

import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import type { ConnectorFieldSchema } from "@/types";

import { applyDetectedAuthConfig, type DetectAuthResponse, detectOpenAPIAuth, mapDetectedAuthType } from "../utils/detectOpenAPIAuth";

interface ConnectorAuthTypeFieldProps {
    name: string;
    fieldConfig: ConnectorFieldSchema;
    error?: any;
    className?: string;
}

function selectAuthType(params: { currentAuth: string | null | undefined; detectedTypes: string[]; suggestedAuth?: string }): string {
    const { currentAuth, detectedTypes, suggestedAuth } = params;

    if (currentAuth && currentAuth !== "none" && detectedTypes.includes(currentAuth)) {
        return currentAuth;
    }

    if (suggestedAuth && detectedTypes.includes(suggestedAuth)) {
        return suggestedAuth;
    }

    return detectedTypes[0];
}

export const ConnectorAuthTypeField: React.FC<ConnectorAuthTypeFieldProps> = ({ name, error, className, fieldConfig }) => {
    const { hint, required, placeholder, options = [], label } = fieldConfig;
    const { field } = useController({ name });
    const { setValue, getValues, control } = useFormContext();
    const [detectedAuth, setDetectedAuth] = useState<DetectAuthResponse | null>(null);
    const lastProcessedFileRef = useRef<string | null>(null);

    const resources: FileList | undefined = useWatch({ control, name: "resources" });

    useEffect(() => {
        let isMounted = true;

        const detectAuth = async () => {
            if (!resources || resources.length === 0) {
                lastProcessedFileRef.current = null;
                setDetectedAuth(null);
                return;
            }

            const file = resources[0];

            const fileId = `${file.name}-${file.size}-${file.lastModified}`;

            if (lastProcessedFileRef.current === fileId) {
                return;
            }

            lastProcessedFileRef.current = fileId;

            try {
                const result = await detectOpenAPIAuth(file, undefined);

                if (isMounted && result) {
                    setDetectedAuth(result);
                }
            } catch (error) {
                console.error("Auth detection failed:", error);
            }
        };

        detectAuth();

        return () => {
            isMounted = false;
        };
    }, [resources]);

    const detectedAuthTypes = useMemo(() => {
        if (!detectedAuth || detectedAuth.schemes.length === 0) {
            return null;
        }

        const types = new Set<string>();
        detectedAuth.schemes.forEach(scheme => {
            const mappedType = mapDetectedAuthType(scheme.authType);
            types.add(mappedType);
        });

        return Array.from(types);
    }, [detectedAuth]);

    const displayOptions = useMemo(() => {
        if (!detectedAuthTypes) {
            return options;
        }

        return options.filter(opt => {
            const value = typeof opt === "string" ? opt : opt.value;
            return detectedAuthTypes.includes(value);
        });
    }, [options, detectedAuthTypes]);

    useEffect(() => {
        if (!detectedAuthTypes || detectedAuthTypes.length === 0) {
            const currentValue = getValues(name);
            if (!currentValue || currentValue === "") {
                setValue(name, "none");
            }
            return;
        }

        const suggestedAuthType = detectedAuth?.suggestedAuth?.authType ? mapDetectedAuthType(detectedAuth.suggestedAuth.authType) : undefined;

        const selectedAuth = selectAuthType({
            currentAuth: getValues(name),
            detectedTypes: detectedAuthTypes,
            suggestedAuth: suggestedAuthType,
        });

        setValue(name, selectedAuth);

        if (detectedAuth?.suggestedAuth && selectedAuth === suggestedAuthType) {
            applyDetectedAuthConfig(detectedAuth.suggestedAuth, setValue, selectedAuth, true);
        }
    }, [detectedAuth, detectedAuthTypes, name, getValues, setValue]);

    return (
        <PageLabelWithValue className={className}>
            <PageLabel required={required}>{label}</PageLabel>
            {hint && <div className="text-sm text-(--secondary-text-wMain)">{hint}</div>}

            <Select value={field.value} onValueChange={field.onChange}>
                <SelectTrigger id={name} data-testid={`connector-field-${name.replace(/\./g, "-")}`}>
                    <SelectValue placeholder={placeholder ?? "Select an authentication type"} />
                </SelectTrigger>
                <SelectContent>
                    {displayOptions.map(opt => {
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

            {error && <ErrorLabel message={error.message} />}
        </PageLabelWithValue>
    );
};
