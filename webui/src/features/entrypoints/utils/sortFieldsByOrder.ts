// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { GatewayFieldSchema } from "@/types/models";

export const sortFieldsByOrder = (fields: Record<string, GatewayFieldSchema>): [string, GatewayFieldSchema][] => {
    const entries = Object.entries(fields);
    const orderedFields = entries.filter(([, field]) => field.order !== undefined);
    const unorderedFields = entries.filter(([, field]) => field.order === undefined);
    orderedFields.sort(([, a], [, b]) => (a.order || 0) - (b.order || 0));
    return [...orderedFields, ...unorderedFields];
};
