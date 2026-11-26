// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { describeInputShape, formatInputField } from "./workflowInput";

interface WorkflowInputHintProps {
    schema: Record<string, unknown>;
}

export const WorkflowInputHint: React.FC<WorkflowInputHintProps> = ({ schema }) => {
    const { required, optional } = describeInputShape(schema);

    return (
        <div className="space-y-2 rounded-md border bg-(--secondary-w10) p-3 text-sm" data-testid="workflow-input-hint">
            <p>This workflow takes structured input. Write the instructions as a JSON object: the workflow receives it as its input each time the task runs.</p>
            {required.length > 0 && (
                <p>
                    <span className="font-semibold">Required:</span> {required.map(formatInputField).join(", ")}
                </p>
            )}
            {optional.length > 0 && (
                <p>
                    <span className="font-semibold">Optional:</span> {optional.map(formatInputField).join(", ")}
                </p>
            )}
        </div>
    );
};
