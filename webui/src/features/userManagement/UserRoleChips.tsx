// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CornerDownRight, FileCog, type LucideIcon, Shield } from "lucide-react";
import { validate as validateUuid } from "uuid";

import { Badge } from "@/ui/badge";
import { Skeleton } from "@/ui/skeleton";

import type { UserRoleSource } from "@/types";

import type { RoleLookup } from "./useRoleLookup";

interface RoleChip {
    roleId: string;
    source?: UserRoleSource;
}

interface UserRoleChipsProps {
    roles: readonly RoleChip[];
    roleById: RoleLookup;
    max?: number;
}

const SOURCE_META: Record<Exclude<UserRoleSource, "direct">, { icon: LucideIcon; tooltip: string }> = {
    claim: { icon: Shield, tooltip: "Granted through a claim mapping" },
    yaml: { icon: FileCog, tooltip: "Granted through Agent Mesh configuration" },
    default: { icon: CornerDownRight, tooltip: "Granted through the default role because no other assignment matched" },
};

const sourceMeta = (source: UserRoleSource | undefined) => (source && source !== "direct" ? SOURCE_META[source] : undefined);

const isInherited = (role: RoleChip): boolean => sourceMeta(role.source) !== undefined;

const UNKNOWN_ROLE = "Unknown role";
const UNKNOWN_ROLE_ID_PREFIX_LENGTH = 8;

function roleLabel(roleId: string, roleById: ReadonlyMap<string, { name: string }> | undefined): { name: string; tooltip?: string } {
    if (roleById === undefined) return { name: roleId };
    const name = roleById.get(roleId)?.name;
    if (name !== undefined) return { name };
    return validateUuid(roleId) ? { name: `${UNKNOWN_ROLE} ${roleId.slice(0, UNKNOWN_ROLE_ID_PREFIX_LENGTH)}`, tooltip: `No role has the id ${roleId}` } : { name: roleId };
}

function dedupeByRole(roles: readonly RoleChip[]): RoleChip[] {
    const byId = new Map<string, RoleChip>();
    for (const role of roles) {
        const existing = byId.get(role.roleId);
        if (!existing || (isInherited(existing) && !isInherited(role))) byId.set(role.roleId, role);
    }
    return [...byId.values()];
}

export function UserRoleChips({ roles, roleById, max = 3 }: Readonly<UserRoleChipsProps>) {
    if (roles.length === 0) return "None";
    if (roleById === "pending") return <Skeleton role="status" className="h-5 w-24" aria-label="Loading roles" />;

    const resolved = dedupeByRole(roles).map(role => {
        const meta = sourceMeta(role.source);
        const label = roleLabel(role.roleId, roleById);
        return { ...role, name: label.name, meta, tooltip: [meta?.tooltip, label.tooltip].filter(Boolean).join(". ") };
    });
    const shown = resolved.slice(0, max);
    const overflow = resolved.slice(max);

    return (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {shown.map(role => {
                const Icon = role.meta?.icon;
                return (
                    <Badge key={role.roleId} className="max-w-full min-w-0" tooltip={role.tooltip}>
                        {Icon && <Icon aria-hidden className="text-(--secondary-text-wMain)" />}
                        <span className="truncate" title={role.tooltip ? undefined : role.name}>
                            {role.name}
                        </span>
                    </Badge>
                );
            })}
            {overflow.length > 0 && (
                <span className="text-xs text-(--secondary-text-wMain)" title={overflow.map(r => r.name).join(", ")}>
                    + {overflow.length}
                </span>
            )}
        </div>
    );
}
