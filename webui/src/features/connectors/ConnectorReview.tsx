// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { useFormContext } from "react-hook-form";

import { PageLabel, PageLabelWithValue, PageSection } from "@/common/layout";

import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";

import type { ConnectorFieldSchema, ConnectorSchema, Tool } from "@/types";

import { ToolCard } from "./components/ToolCard";
import { ConnectorDisplayField } from "./ConnectorDisplayField";
import { sortFieldsByOrder } from "./utils/schemaGenerator";

export interface ConnectorReviewProps {
    connectorType: string;
    schemaInfo: ConnectorSchema;
    toolsFieldPath: string;
    selectedToolsFieldPath: string;
    excludedFields: readonly string[];
    parseTools: (tools: any[], selectedToolIds: any) => Tool[];
}

export const ConnectorReview = ({ connectorType, schemaInfo, toolsFieldPath, selectedToolsFieldPath, excludedFields, parseTools }: ConnectorReviewProps) => {
    const { watch } = useFormContext();

    const connectorName = watch("name");
    const description = watch("description");
    const formValues = watch("values");
    const tools = watch(toolsFieldPath);
    const selectedToolIds = watch(selectedToolsFieldPath);

    const selectedTools = useMemo((): Tool[] => {
        if (!tools || !selectedToolIds) {
            return [];
        }
        return parseTools(tools, selectedToolIds);
    }, [tools, selectedToolIds, parseTools]);

    const shouldDisplayField = (fieldConfig: ConnectorFieldSchema): boolean => matchesConditionalDisplay(fieldConfig.conditionalDisplay, formValues);

    const renderConfigFields = () => {
        if (!schemaInfo.fieldSchema) {
            return null;
        }

        return sortFieldsByOrder(schemaInfo.fieldSchema)
            .filter(([_, fieldConfig]) => shouldDisplayField(fieldConfig))
            .map(([fieldName, fieldConfig]) => {
                if (excludedFields.includes(fieldName)) {
                    return null;
                }

                const value = formValues[fieldName];
                return <ConnectorDisplayField key={fieldName} fieldName={fieldName} label={fieldConfig.label || fieldName} value={value} fieldType={fieldConfig.type} options={fieldConfig.options} />;
            });
    };

    return (
        <div className="flex flex-col gap-6">
            <PageSection className="gap-4">
                <div className="text-lg font-semibold">Connector Details</div>

                <PageLabelWithValue>
                    <PageLabel>Type</PageLabel>
                    <div>{connectorType}</div>
                </PageLabelWithValue>

                <PageLabelWithValue>
                    <PageLabel>Connector Name</PageLabel>
                    <div>{connectorName}</div>
                </PageLabelWithValue>

                <PageLabelWithValue>
                    <PageLabel>Description</PageLabel>
                    <div className="whitespace-pre-wrap">{description}</div>
                </PageLabelWithValue>
            </PageSection>

            <PageSection className="gap-4">
                <div className="text-lg font-semibold">Configuration</div>
                {renderConfigFields()}
            </PageSection>

            <PageSection>
                <div className="flex items-center justify-between font-semibold">
                    <span>Tools</span>
                    {tools && tools.length > 0 && (
                        <span className="text-sm font-normal text-(--secondary-text-wMain)">
                            {selectedTools.length} out of {tools.length} enabled
                        </span>
                    )}
                </div>
                <div className="text-sm text-(--secondary-text-wMain)">These tools will be available for this connector.</div>

                {selectedTools.length > 0 ? (
                    <div className="mt-4 flex flex-col gap-[11px]">
                        {selectedTools.map(tool => (
                            <ToolCard key={tool.operationId} tool={tool} />
                        ))}
                    </div>
                ) : (
                    <div className="mt-4 text-(--secondary-text-wMain)">No tools selected.</div>
                )}
            </PageSection>
        </div>
    );
};
