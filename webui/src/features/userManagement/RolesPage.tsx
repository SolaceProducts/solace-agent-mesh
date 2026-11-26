// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";

import { useDefaultRoles, useRoles } from "@/api/rbac";

import { MessageBanner } from "@/common/banners";
import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";

import { getErrorMessage } from "@/utils/api";
import { DOC_LINKS } from "@/utils/docs";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import type { RBACRole } from "@/types";
import { useSortableTable } from "@/ui";

import { RoleFormDialog } from "./RoleFormDialog";
import { RolesTable } from "./RolesTable";
import { RoleViewDialog } from "./RoleViewDialog";
import { originLabel, ROLE_ORIGINS } from "./utils";

const PAGE_SIZE = 20;
const ANY_TYPE = "__any__";

export function RolesPage() {
    const { data: roles = [], isLoading, error } = useRoles();
    const { data: defaults, error: defaultsError } = useDefaultRoles();

    const [query, setQuery] = useState("");
    const [typeFilter, setTypeFilter] = useState<string>(ANY_TYPE);
    const [page, setPage] = useState(1);
    const [createOpen, setCreateOpen] = useState(false);
    const [editRole, setEditRole] = useState<RBACRole | null>(null);
    const [viewRole, setViewRole] = useState<RBACRole | null>(null);

    const filtered = useMemo(() => {
        const q = query.trim().toLowerCase();
        return roles.filter(role => {
            if (q !== "" && !role.name.toLowerCase().includes(q)) return false;
            if (typeFilter !== ANY_TYPE && role.origin !== typeFilter) return false;
            return true;
        });
    }, [roles, query, typeFilter]);

    const { sortedData: sortedRoles, sortKey, sortDir, handleSort } = useSortableTable(filtered, ["name"]);

    useEffect(() => setPage(1), [query, typeFilter, sortKey, sortDir]);

    const totalPages = Math.max(1, Math.ceil(sortedRoles.length / PAGE_SIZE));
    useEffect(() => {
        if (page > totalPages) setPage(totalPages);
    }, [page, totalPages]);

    const pageStart = (page - 1) * PAGE_SIZE;
    const pageItems = sortedRoles.slice(pageStart, pageStart + PAGE_SIZE);

    const hasActiveFilters = query.trim() !== "" || typeFilter !== ANY_TYPE;
    const clearFilters = () => {
        setQuery("");
        setTypeFilter(ANY_TYPE);
    };

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-4 p-6 md:p-8">
            <OnboardingBanner
                storageKey="sam-roles-onboarding-dismissed"
                header="Define what users can do with roles."
                description="Roles are collections of scopes you assign to users to control what they can see and do across Solace Agent Mesh. Create custom roles or use the built-in roles."
                learnMoreText="Learn more about user management"
                learnMoreUrl={DOC_LINKS.userManagementRoles}
            />

            {defaultsError && <MessageBanner variant="warning" message={`The default roles could not be loaded, so no role can be added to or removed from them. ${getErrorMessage(defaultsError, "Failed to load the default roles.")}`} />}

            <div className="flex flex-wrap items-center gap-3">
                <SearchInput value={query} onChange={setQuery} placeholder="Filter by name" testid="roleNameSearch" />
                <Select value={typeFilter} onValueChange={setTypeFilter}>
                    <SelectTrigger className="w-56" aria-label="Filter by type" data-testid="roleTypeFilter">
                        <SelectValue placeholder="Any Type" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={ANY_TYPE}>Any Type</SelectItem>
                        {ROLE_ORIGINS.map(origin => (
                            <SelectItem key={origin} value={origin}>
                                {originLabel(origin)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Button variant="outline" className="ml-auto" testid="createRole" onClick={() => setCreateOpen(true)}>
                    <Plus />
                    Create Role
                </Button>
            </div>

            <TableStateBoundary
                resourceName="Roles"
                isLoading={isLoading}
                error={error}
                itemCount={filtered.length}
                hasActiveFilters={hasActiveFilters}
                onClearFilters={clearFilters}
                emptyDescription="Roles are collections of scopes you assign to users to control what they can see and do. Create custom roles or use the built-in roles."
                emptyActions={[{ text: "Create Role", variant: "default", onClick: () => setCreateOpen(true) }]}
            >
                <div className="min-h-0 flex-1 overflow-auto rounded-xs border">
                    <RolesTable roles={pageItems} defaults={defaults} onView={setViewRole} onEdit={setEditRole} sortKey={sortKey} sortDir={sortDir} onSort={handleSort} />
                </div>
                <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}–${Math.min(pageStart + PAGE_SIZE, filtered.length)} of ${filtered.length}`} />
            </TableStateBoundary>

            <RoleFormDialog open={createOpen} onOpenChange={setCreateOpen} />
            <RoleFormDialog open={editRole !== null} role={editRole} onOpenChange={open => !open && setEditRole(null)} />
            <RoleViewDialog
                role={viewRole}
                onOpenChange={open => !open && setViewRole(null)}
                onEdit={role => {
                    setViewRole(null);
                    setEditRole(role);
                }}
            />
        </div>
    );
}
