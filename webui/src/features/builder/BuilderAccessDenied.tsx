// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EmptyState } from "@/common";

export function BuilderAccessDenied() {
    return <EmptyState variant="error" title="Access Denied" subtitle="You don't have permission to use the Builder. Contact your administrator to request access." />;
}
