// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useRoles } from "@/api/rbac";

import type { RBACRole } from "@/types";

const NO_ROLES: RBACRole[] = [];

export type RoleLookup = ReadonlyMap<string, RBACRole> | "pending" | undefined;

export function useRoleLookup(): { roles: RBACRole[]; roleById: RoleLookup } {
    const { data, isPending } = useRoles();
    const roleById = useMemo<RoleLookup>(() => {
        if (data) return new Map(data.map(r => [r.id, r]));
        return isPending ? "pending" : undefined;
    }, [data, isPending]);
    return { roles: data ?? NO_ROLES, roleById };
}
