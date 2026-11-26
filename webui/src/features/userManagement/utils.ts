// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RoleOrigin, User } from "@/types";

export const isSystemUser = (user: Pick<User, "provisionedVia">): boolean => user.provisionedVia === "system";

const ORIGIN_LABELS: Record<RoleOrigin, string> = {
    yaml: "System",
    builtin: "Built-in",
    custom: "Custom",
};

export const originLabel = (origin: RoleOrigin): string => ORIGIN_LABELS[origin];

export const ROLE_ORIGINS = Object.keys(ORIGIN_LABELS) as RoleOrigin[];

export function emailLine(displayName: string | undefined, email: string | undefined): string {
    return email && email !== displayName ? email : "";
}
