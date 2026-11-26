// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EmptyState } from "./EmptyState";

export function AccessDenied({ action }: Readonly<{ action: string }>) {
    return <EmptyState variant="error" title="Access Denied" subtitle={`You don't have permission to ${action}. Contact your administrator to request access.`} />;
}
