// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Badge } from "@/ui/badge";

export function DefaultBudgetBadge({ withTooltip = true }: Readonly<{ withTooltip?: boolean }>) {
    return <Badge tooltip={withTooltip ? "This budget applies to users and service accounts with no budget assignment." : undefined}>Default</Badge>;
}
