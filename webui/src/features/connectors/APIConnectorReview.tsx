// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ConnectorSchema } from "@/types";

import { ConnectorReview } from "./ConnectorReview";
import { parseAPITools } from "./utils/toolParsers";

const API_EXCLUDED_FIELDS = ["file_s3_key", "allow_list", "specification_url"] as const;

export interface APIConnectorReviewProps {
    connectorType: string;
    schemaInfo: ConnectorSchema;
}

export const APIConnectorReview = ({ connectorType, schemaInfo }: APIConnectorReviewProps) => {
    return <ConnectorReview connectorType={connectorType} schemaInfo={schemaInfo} toolsFieldPath="tools" selectedToolsFieldPath="values.allow_list" excludedFields={API_EXCLUDED_FIELDS} parseTools={parseAPITools} />;
};
