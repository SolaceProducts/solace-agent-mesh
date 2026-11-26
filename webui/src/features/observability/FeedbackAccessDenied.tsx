// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { EmptyState } from "@/common";

export function FeedbackAccessDenied() {
    return <EmptyState variant="error" title="Access Denied" subtitle="You don't have permission to view feedback. Contact your administrator to request access." />;
}
