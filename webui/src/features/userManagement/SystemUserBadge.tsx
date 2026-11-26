// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Badge } from "@/ui/badge";

export function SystemUserBadge() {
    return <Badge tooltip="Entrypoints can run as this service account, and its roles set what it can access.">Service Account</Badge>;
}
