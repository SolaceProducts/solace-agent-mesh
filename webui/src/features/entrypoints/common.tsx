// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { PageLabel } from "@/common/layout";
import { SyncStatusTooltip as SharedSyncStatusTooltip } from "@/common/status";

import type { EventRule, Gateway, GatewayApiPath, GatewayFieldSchema, GatewaySchema } from "@/types/models";

import { ToolCard } from "@/features/connectors/components/ToolCard";

import { EventRuleCard } from "./EventRuleCard";
import { GatewayDisplayField } from "./GatewayDisplayField";
import { useAgentOptions, useWorkflowOptions } from "./hooks";
import { sortFieldsByOrder } from "./utils/sortFieldsByOrder";
import { buildEndpointUrl } from "./utils/teams";

interface GatewayApiPathListProps {
    baseUrl: string;
    apiPaths: GatewayApiPath[];
}

export const GatewayApiPathList = ({ baseUrl, apiPaths }: GatewayApiPathListProps) => {
    if (apiPaths.length === 0) return null;
    return (
        <div className="flex flex-col gap-4">
            <PageLabel>API Paths</PageLabel>
            <div className="flex flex-col gap-[11px]">
                {apiPaths.map(({ path, description }) => (
                    <ToolCard
                        key={path}
                        tool={{
                            operationId: path,
                            name: path,
                            endpoint: buildEndpointUrl(baseUrl, path),
                            description,
                            method: "",
                        }}
                    />
                ))}
            </div>
        </div>
    );
};

export const SyncStatusTooltip = ({ gateway }: { gateway: Gateway }) => {
    return <SharedSyncStatusTooltip entity={gateway} />;
};

interface GatewayConfigurationRendererProps {
    schemaInfo: GatewaySchema;
    values: Record<string, any>;
    shouldDisplayField?: (fieldConfig: GatewayFieldSchema) => boolean;
    onViewRule?: (rule: EventRule) => void;
    className?: string;
}

export const GatewayConfigurationRenderer = ({ schemaInfo, values, shouldDisplayField, onViewRule, className = "" }: GatewayConfigurationRendererProps) => {
    const agentOptions = useAgentOptions();
    const agentDisplayOptions = useMemo(() => agentOptions.map(o => ({ value: o.name, label: o.displayName })), [agentOptions]);
    const { options: workflowOptions } = useWorkflowOptions();
    const workflowDisplayOptions = useMemo(() => workflowOptions.map(o => ({ value: o.name, label: o.displayName })), [workflowOptions]);

    const displayOptionsFor = (fieldConfig: GatewayFieldSchema) => {
        if (fieldConfig.type === "agent_select") return agentDisplayOptions;
        if (fieldConfig.type === "workflow_select") return workflowDisplayOptions;
        return fieldConfig.options;
    };

    return (
        <>
            {sortFieldsByOrder(schemaInfo.schema)
                .filter(([_, fieldConfig]) => !shouldDisplayField || shouldDisplayField(fieldConfig))
                .map(([fieldName, fieldConfig]) => {
                    if (fieldConfig.type === "event_rules") {
                        const rules = (values[fieldName] as EventRule[]) || [];
                        return (
                            <div key={fieldName} className={`pt-2 ${className}`}>
                                <div>{fieldConfig.label}</div>
                                {rules.length > 0 ? (
                                    rules.map((rule, index) => <EventRuleCard key={index} rule={rule} onView={onViewRule ? () => onViewRule(rule) : undefined} />)
                                ) : (
                                    <div className="bg-(--secondary-w10) py-2 text-center text-sm text-(--secondary-text-wMain)">No event rules configured</div>
                                )}
                            </div>
                        );
                    }

                    return <GatewayDisplayField key={fieldName} label={fieldConfig.label} value={values[fieldName]} fieldType={fieldConfig.type} options={displayOptionsFor(fieldConfig)} itemKey={fieldConfig.itemKey} />;
                })}
        </>
    );
};
