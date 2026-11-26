// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useFormContext } from "react-hook-form";

import { EmptySection, PageLabel, PageLabelWithValue } from "@/common/layout";

import type { ConnectorSchema } from "@/types";
import { AccordionCard, Badge } from "@/ui";

import { EventMeshAdvancedSettingsSummary } from "./components/EventMeshAdvancedSettingsSummary";
import { ConnectorDisplayField } from "./ConnectorDisplayField";
import { EVENT_MESH_SECTION_TITLE } from "./eventMeshStyles";
import { EVENT_MESH_RESPONSE_MODE_OPTIONS, type EventMeshOperation, eventMeshParametersFromSchema, responseModeValue } from "./eventMeshUtils";

interface EventMeshConnectorReviewProps {
    connectorType: string;
    schemaInfo: ConnectorSchema;
}

function ToolSummary({ operation, index }: Readonly<{ operation: EventMeshOperation; index: number }>) {
    const parameters = eventMeshParametersFromSchema(operation.input_schema);
    const responseMode = responseModeValue(operation.wait_for_response);

    return (
        <AccordionCard
            value={`tool-${index}`}
            trigger={
                <span className="min-w-0 text-left">
                    <span className="block truncate text-sm font-medium text-(--primary-wMain)">{operation.tool_name || "Untitled tool"}</span>
                    <span className="block truncate font-mono text-xs text-(--secondary-text-wMain)">{operation.topic || "—"}</span>
                </span>
            }
        >
            <div className="space-y-8 p-4">
                <div>
                    <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Tool Definition</div>
                    <div className="space-y-6">
                        <ConnectorDisplayField label="Tool Name" value={operation.tool_name} fieldName="tool_name" />
                        <ConnectorDisplayField label="Tool Description" value={operation.tool_description} fieldType="textarea" fieldName="tool_description" />
                    </div>
                </div>

                <div>
                    <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Message Routing and Delivery</div>
                    <div className="space-y-6">
                        <ConnectorDisplayField label="Topic Address" value={operation.topic} fieldName="topic" />
                        <ConnectorDisplayField label="Response Mode" value={responseMode} fieldType="select" options={EVENT_MESH_RESPONSE_MODE_OPTIONS} fieldName="response_mode" />
                        {parameters.length > 0 && (
                            <PageLabelWithValue className="!gap-1">
                                <PageLabel>Inputs</PageLabel>
                                <div className="flex flex-wrap gap-1">
                                    {parameters.map(p => (
                                        <Badge key={p.name} type={p.context_expression ? "info" : undefined}>
                                            {p.name}
                                            {p.required ? " *" : ""}
                                        </Badge>
                                    ))}
                                </div>
                            </PageLabelWithValue>
                        )}
                    </div>
                </div>

                <div>
                    <EventMeshAdvancedSettingsSummary operation={operation} />
                </div>
            </div>
        </AccordionCard>
    );
}

export function EventMeshConnectorReview({ connectorType }: Readonly<EventMeshConnectorReviewProps>) {
    const { getValues } = useFormContext();
    const formValues = getValues();
    const operations = (formValues.values?.operations ?? []) as EventMeshOperation[];

    return (
        <div className="max-w-5xl space-y-8">
            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Connector Details</div>
                <div className="space-y-6">
                    <ConnectorDisplayField label="Connector Type" value={connectorType} fieldName="connector_type" />
                    <ConnectorDisplayField label="Connector Name" value={formValues.name} fieldName="name" />
                    <ConnectorDisplayField label="Description" value={formValues.description} fieldType="textarea" fieldName="description" />
                </div>
            </div>

            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Broker Connection</div>
                <div className="space-y-6">
                    <ConnectorDisplayField label="Host URI" value={formValues.values?.broker_url} fieldName="broker_url" />
                    <ConnectorDisplayField label="Message VPN" value={formValues.values?.broker_vpn} fieldName="broker_vpn" />
                    <ConnectorDisplayField label="Username" value={formValues.values?.broker_username} fieldName="broker_username" />
                </div>
            </div>

            <div>
                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Tools</div>
                <div className="mb-6 flex items-center justify-between text-sm text-(--secondary-text-wMain)">
                    <span>These tools will be available to agents using this connector.</span>
                    {operations.length > 0 && (
                        <span>
                            {operations.length} tool{operations.length === 1 ? "" : "s"} configured
                        </span>
                    )}
                </div>
                {operations.length === 0 ? (
                    <EmptySection message="No tools configured." />
                ) : (
                    <div className="space-y-3">
                        {operations.map((operation, index) => (
                            <ToolSummary key={operation._uid ?? operation.tool_name} operation={operation} index={index} />
                        ))}
                    </div>
                )}
            </div>
        </div>
    );
}
