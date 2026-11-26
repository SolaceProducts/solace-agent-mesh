// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { findSchemaByRef, generateRequestBodyExample } from "@/features/connectors/utils";

import { EmptyState } from "@/common";

export interface RequestBodyDisplayProps {
    requestBody: any;
    spec: any;
}

export const RequestBodyDisplay = ({ requestBody, spec }: RequestBodyDisplayProps) => {
    const schema = requestBody?.$ref ? findSchemaByRef(requestBody.$ref, spec) : requestBody;
    const example = generateRequestBodyExample(schema, spec);

    return schema ? (
        <>
            <div className="mb-4 text-sm">
                <span className="text-(--secondary-text-wMain)">Content Types:</span> {(schema.content && Object.keys(schema.content).length > 0 ? Object.keys(schema.content) : ["N/A"]).join(", ")}
            </div>
            <pre className="max-h-96 overflow-auto rounded bg-(--secondary-w10) p-4 text-xs">{example !== null ? JSON.stringify(example, null, 2) : "No example is available"}</pre>
        </>
    ) : (
        <div className="mb-4">
            <EmptyState title="No request body is defined." variant="noImage" />
        </div>
    );
};
