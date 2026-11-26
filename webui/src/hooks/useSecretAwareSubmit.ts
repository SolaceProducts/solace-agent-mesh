// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

export interface FieldSchemaForSubmit {
    hidden?: boolean;
    type?: string;
}

export interface SecretAwareSubmitOptions<TFieldSchema extends FieldSchemaForSubmit> {
    onSubmit: (data: any) => void;
    isNew?: boolean;
    formState: {
        dirtyFields: any;
        touchedFields: any;
    };
    fieldsWithStoredSecrets: Set<string>;
    getFieldSchema: (fieldName: string) => TFieldSchema | undefined;
}

export const useSecretAwareSubmit = <TFieldSchema extends FieldSchemaForSubmit>({ onSubmit, isNew, formState, fieldsWithStoredSecrets, getFieldSchema }: SecretAwareSubmitOptions<TFieldSchema>) => {
    const { dirtyFields, touchedFields } = formState;

    return useMemo(() => {
        return (data: any) => {
            const cleanedData = { ...data };

            if (data.values) {
                cleanedData.values = {};

                Object.keys(data.values).forEach(key => {
                    const fieldSchema = getFieldSchema(key);
                    const isPasswordField = fieldSchema?.hidden || fieldSchema?.type === "password";
                    const hasStoredSecret = fieldsWithStoredSecrets.has(`values.${key}`);
                    const wasTouched = touchedFields.values?.[key];

                    if (data.values[key] === undefined) {
                        if (isPasswordField && hasStoredSecret && wasTouched) {
                            cleanedData.values[key] = "";
                        }
                    } else {
                        cleanedData.values[key] = data.values[key];
                    }
                });
            }

            onSubmit(cleanedData);
        };
    }, [onSubmit, isNew, dirtyFields, touchedFields, fieldsWithStoredSecrets, getFieldSchema]);
};
