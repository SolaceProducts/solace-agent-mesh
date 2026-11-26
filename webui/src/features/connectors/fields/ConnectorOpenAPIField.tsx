// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { useFormContext } from "react-hook-form";

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { ErrorDialog, FileUpload } from "@/common";

import { type ParsedSpecification, parseSpecificationText, PARSING_ERROR_TITLE, type SpecificationFailure } from "../utils/parseSpecification";

const textOf = (value: unknown, maxLength?: number): string => (typeof value === "string" ? value.substring(0, maxLength) : "");

const clearAllAuthValues = (getValues: () => any, setValue: (name: string, value: any) => void): void => {
    const allValues = getValues();
    if (allValues.values) {
        Object.keys(allValues.values)
            .filter(key => key.startsWith("auth_"))
            .forEach(key => setValue(`values.${key}`, null));
    }
};

const MAX_NAME_LENGTH = 100;

const SUPPORTED_CONTENT_TYPES = ["application/json", "application/x-yaml", "application/yaml", "text/yaml", "text/json"];

interface ConnectorOpenAPIFieldProps {
    isNew?: boolean;
}

export const ConnectorOpenAPIField: React.FC<ConnectorOpenAPIFieldProps> = ({ isNew = true }) => {
    const { getValues, setValue } = useFormContext();
    const resources = getValues("resources");
    const [files, setFiles] = useState<FileList | null>(resources ?? null);
    const [errorObject, setErrorObject] = useState<SpecificationFailure | null>(null);

    const specData = useMemo<Promise<ParsedSpecification | null>>(async (): Promise<ParsedSpecification | null> => {
        if (!files || files.length === 0) {
            return null;
        }

        let failure: SpecificationFailure;
        try {
            const result = parseSpecificationText(await files[0].text(), files[0].type.endsWith("yaml"));
            if (result.ok) {
                return result;
            }
            failure = result.failure;
        } catch (error) {
            failure = { title: PARSING_ERROR_TITLE, message: `Failed to read the specification file: ${error instanceof Error ? error.message : String(error)}` };
        }
        setErrorObject(failure);
        setFiles(null);
        return null;
    }, [files]);

    useEffect(() => {
        let isMounted = true;
        specData
            .then(parsed => {
                if (isMounted) {
                    const spec = parsed?.spec ?? null;
                    const specIsPresentInForm = !!spec;

                    setValue("openAPISpec", spec);
                    setValue("tools", parsed?.tools ?? []);
                    setValue("resources", files);

                    if (specIsPresentInForm) {
                        const currentName = getValues("name");
                        const currentDescription = getValues("description");
                        const currentBaseUrl = getValues("values.base_url");

                        setValue("values.base_url", currentBaseUrl ?? null);
                        setValue("name", currentName || textOf(spec?.info?.title, MAX_NAME_LENGTH));
                        setValue("description", currentDescription || textOf(spec?.info?.description));
                    } else if (!specIsPresentInForm && isNew) {
                        setValue("values.base_url", null);
                        setValue("name", "");
                        setValue("description", "");
                        clearAllAuthValues(getValues, setValue);
                    }
                }
            })
            .catch(error => {
                if (isMounted) {
                    setErrorObject({ title: PARSING_ERROR_TITLE, message: `Failed to apply the specification to the form: ${error instanceof Error ? error.message : String(error)}` });
                    setFiles(null);
                }
            });
        return () => {
            isMounted = false;
        };
    }, [specData, setValue, files, isNew, getValues]);

    const handleChange = (files: FileList | null) => {
        setFiles(files);
    };

    const handleValidate = (files: FileList): { valid: boolean; error?: string } => {
        if (!SUPPORTED_CONTENT_TYPES.some(ct => files[0].type.includes(ct))) {
            return { valid: false, error: "Please upload a valid OpenAPI specification file (JSON or YAML)." };
        }
        return { valid: true };
    };

    return (
        <PageLabelWithValue>
            <PageLabel required>OpenAPI Specification</PageLabel>
            <FileUpload name="openapi-spec" accept=".yaml,.yml,.json" value={files} onChange={handleChange} onValidate={handleValidate} />
            {errorObject ? <ErrorDialog title={errorObject.title} open={!!errorObject} error={errorObject.message} errorSource="client" onOpenChange={open => !open && setErrorObject(null)} /> : null}
        </PageLabelWithValue>
    );
};
