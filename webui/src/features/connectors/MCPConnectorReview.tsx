// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ConnectorSchema } from "@/types";

import { ConnectorReview } from "./ConnectorReview";
import { parseMCPTools } from "./utils/toolParsers";

const MCP_EXCLUDED_FIELDS = ["file_s3_key", "selected_tools", "manifest"] as const;

export interface MCPConnectorReviewProps {
    connectorType: string;
    schemaInfo: ConnectorSchema;
}

export const MCPConnectorReview = ({ connectorType, schemaInfo }: MCPConnectorReviewProps) => {
    return <ConnectorReview connectorType={connectorType} schemaInfo={schemaInfo} toolsFieldPath="mcpTools" selectedToolsFieldPath="values.selected_tools" excludedFields={MCP_EXCLUDED_FIELDS} parseTools={parseMCPTools} />;
};
