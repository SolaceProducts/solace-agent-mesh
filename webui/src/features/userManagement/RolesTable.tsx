// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Badge } from "@/ui/badge";
import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import type { DefaultRolesResponse, RBACRole } from "@/types";

import { RoleRowActions } from "./RoleRowActions";
import { originLabel } from "./utils";

type SortDir = "asc" | "desc";

interface RolesTableProps {
    roles: readonly RBACRole[];
    defaults: DefaultRolesResponse | undefined;
    sortKey: string;
    sortDir: SortDir;
    onView: (role: RBACRole) => void;
    onEdit: (role: RBACRole) => void;
    onSort: (column: string) => void;
}

export function RolesTable({ roles, defaults, onView, onEdit, sortKey, sortDir, onSort }: Readonly<RolesTableProps>) {
    const defaultRoleIds = defaults?.roleIds ?? [];
    return (
        <Table className="table-fixed" containerClassName="overflow-x-visible">
            <TableHeader sticky>
                <TableRow className="hover:bg-transparent">
                    <SortableTableHead column="name" currentSortKey={sortKey} sortDir={sortDir} onSort={onSort}>
                        Name
                    </SortableTableHead>
                    <TableHead className="w-[14%] font-semibold">Type</TableHead>
                    <TableHead className="w-[22%] font-semibold">Created By</TableHead>
                    <TableHead className="w-16" aria-label="Actions" />
                </TableRow>
            </TableHeader>
            <TableBody borderLastRow>
                {roles.map(role => (
                    <TableRow key={role.id} className="h-15">
                        <TableCell>
                            <div className="flex min-w-0 flex-col">
                                <div className="flex h-6 min-w-0 items-center gap-2">
                                    <span className="truncate font-medium">{role.name}</span>
                                    {defaultRoleIds.includes(role.id) && <Badge tooltip="Users who match no other role assignment receive this role.">Default</Badge>}
                                </div>
                                {role.description && <span className="truncate text-sm text-(--secondary-text-wMain)">{role.description}</span>}
                            </div>
                        </TableCell>
                        <TableCell>
                            <div>{originLabel(role.origin)}</div>
                        </TableCell>
                        <TableCell className="truncate text-sm" title={role.origin === "custom" ? role.createdBy : "—"}>
                            {role.origin === "custom" ? role.createdBy : "—"}
                        </TableCell>
                        <TableCell className="text-right">
                            <RoleRowActions role={role} defaults={defaults} onView={onView} onEdit={onEdit} />
                        </TableCell>
                    </TableRow>
                ))}
            </TableBody>
        </Table>
    );
}
