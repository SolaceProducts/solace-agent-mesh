// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabelWithValue } from "@/common/layout";

import { ToolsSelection } from "../components/ToolsSelection";

interface ConnectorAllowListFieldProps {
    fieldPath: string;
}

export function ConnectorAllowListField({ fieldPath }: ConnectorAllowListFieldProps) {
    return (
        <PageLabelWithValue>
            <ToolsSelection fieldPath={fieldPath} />
        </PageLabelWithValue>
    );
}
