// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Outlet } from "react-router-dom";

import { useUserContext } from "@/providers/contexts";

import { AccessDenied } from "@/common";
import { useBudgetsEnabled } from "@/hooks";

export function BudgetsGate() {
    const { capabilitiesLoaded } = useUserContext();
    const allowed = useBudgetsEnabled();

    if (!capabilitiesLoaded) return null;
    if (!allowed) return <AccessDenied action="view budgets" />;

    return <Outlet />;
}
