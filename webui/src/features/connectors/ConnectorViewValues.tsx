// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useChatContext } from "@/providers/contexts";

import { PageSection } from "@/common/layout";

import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";

import type { Connector, ConnectorFieldSchema } from "@/types";

import { ConnectorDisplayField } from "./ConnectorDisplayField";
import { sortFieldsByOrder } from "./utils/schemaGenerator";

interface ConnectorViewValuesProps {
    connector: Connector;
}

export const ConnectorViewValues: React.FC<ConnectorViewValuesProps> = ({ connector }) => {
    const { getConnectorSchema } = useChatContext();
    const { type, subtype, values } = connector;

    const schemaInfo = getConnectorSchema(type, subtype);

    const shouldDisplayField = (fieldConfig: ConnectorFieldSchema): boolean => matchesConditionalDisplay(fieldConfig.conditionalDisplay, values);

    const renderValuesFields = () => {
        if (!schemaInfo || !schemaInfo.fieldSchema) {
            return (
                <PageSection>
                    <div className="p-4 text-center">
                        No schema found for {type}/{subtype} connector. This connector type may no longer be supported. Please contact your administrator or remove this connector.
                    </div>
                </PageSection>
            );
        }

        const fieldEntries = sortFieldsByOrder(schemaInfo.fieldSchema);

        return (
            <PageSection>
                <div className="pt-6 font-semibold">Connector Configuration</div>
                <div className="space-y-4">
                    {fieldEntries
                        .filter(([_, fieldConfig]) => shouldDisplayField(fieldConfig))
                        .filter(([_, fieldConfig]) => !fieldConfig.secret || fieldConfig.type === "password")
                        .map(([fieldName, fieldConfig]) => {
                            if (fieldName === "manifest" || fieldName === "selected_tools" || fieldName === "allow_list" || fieldName === "operations") {
                                return null;
                            }

                            return (
                                <ConnectorDisplayField
                                    key={fieldName}
                                    fieldName={fieldName}
                                    label={fieldConfig.label}
                                    value={values?.[fieldName]}
                                    fieldType={fieldConfig.type === "password" ? "password" : fieldConfig.type === "number" ? "number" : fieldConfig.type}
                                    options={fieldConfig.options}
                                />
                            );
                        })}
                </div>
            </PageSection>
        );
    };

    return renderValuesFields();
};
