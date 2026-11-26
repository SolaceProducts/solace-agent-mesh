// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import type { User } from "@/types";

import { SystemUserBadge } from "./SystemUserBadge";
import { UserBudgetCell } from "./UserBudgetCells";
import { UserNameCell } from "./UserNameCell";
import type { RoleLookup } from "./useRoleLookup";
import { UserRoleChips } from "./UserRoleChips";
import { UserRowActions } from "./UserRowActions";
import { emailLine, isSystemUser } from "./utils";

const stripSystemPrefixForRunAs = (sub: string): string => sub.replace(/^system:/, "");

const secondaryLine = (user: User): string => {
    if (isSystemUser(user)) return `Run as: ${stripSystemPrefixForRunAs(user.sub ?? "")}`;
    return emailLine(user.displayName, user.email);
};

interface UsersTableProps {
    users: readonly User[];
    roleById: RoleLookup;
    sortKey: string;
    sortDir: "asc" | "desc";
    onSort: (column: string) => void;
    showBudgets?: boolean;
    canEditBudget?: boolean;
}

export function UsersTable({ users, roleById, sortKey, sortDir, onSort, showBudgets = false, canEditBudget = false }: Readonly<UsersTableProps>) {
    return (
        <Table className="table-fixed" containerClassName="overflow-x-visible">
            <TableHeader sticky>
                <TableRow className="hover:bg-transparent">
                    <SortableTableHead column="displayName" currentSortKey={sortKey} sortDir={sortDir} onSort={onSort} className={showBudgets ? "w-[35%]" : "w-1/2"}>
                        User
                    </SortableTableHead>
                    <TableHead className="font-semibold">Roles</TableHead>
                    {showBudgets && <TableHead className="w-[20%] font-semibold">Budget</TableHead>}
                    <TableHead className="w-16" aria-label="Actions" />
                </TableRow>
            </TableHeader>
            <TableBody borderLastRow>
                {users.map(user => (
                    <TableRow key={user.id}>
                        <TableCell>
                            <UserNameCell displayName={user.displayName} secondaryLine={secondaryLine(user)} badge={isSystemUser(user) && <SystemUserBadge />} />
                        </TableCell>
                        <TableCell>
                            <UserRoleChips roles={user.roles} roleById={roleById} />
                        </TableCell>
                        {showBudgets && (
                            <TableCell>
                                <UserBudgetCell budget={user.budget} topUp={user.budgetTopUp} effectiveAmount={user.effectiveBudgetAmount} />
                            </TableCell>
                        )}
                        <TableCell className="text-right">
                            <UserRowActions user={user} canEditBudget={canEditBudget} />
                        </TableCell>
                    </TableRow>
                ))}
            </TableBody>
        </Table>
    );
}
