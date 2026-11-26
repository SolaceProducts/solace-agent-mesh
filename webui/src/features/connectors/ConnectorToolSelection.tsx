// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useFormContext } from "react-hook-form";

import { PageSection } from "@/common/layout";

import { MessageBanner } from "@/common";

import { ToolsSelection } from "./components/ToolsSelection";

export interface ConnectorToolSelectionProps {
    fieldPath: string;
    description: string;
}

export const ConnectorToolSelection = ({ fieldPath, description }: ConnectorToolSelectionProps) => {
    const {
        formState: { errors },
    } = useFormContext();

    const fieldName = fieldPath.includes(".") ? fieldPath.split(".").pop() : fieldPath;
    const toolsError = fieldName ? (errors?.values as any)?.[fieldName]?.message : undefined;

    return (
        <PageSection>
            {toolsError && <MessageBanner variant="error" message={String(toolsError)} className="mb-4" />}

            <div className="font-semibold">Select Tools</div>
            <div className="text-sm text-(--secondary-text-wMain)">{description}</div>

            <ToolsSelection fieldPath={fieldPath} />
        </PageSection>
    );
};
