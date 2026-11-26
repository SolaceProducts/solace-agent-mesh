// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Plus } from "lucide-react";

import { useClaimMappings } from "@/api/rbac";

import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { type ButtonWithCallback, TableStateBoundary } from "@/common/status";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { useDebounce, usePageNumber } from "@/hooks";
import type { ClaimMapping } from "@/types";
import { useSortState } from "@/ui";
import { DOC_LINKS } from "@/utils";

import { ClaimMappingFormDialog } from "./ClaimMappingFormDialog";
import { ClaimMappingRowActions } from "./ClaimMappingRowActions";
import { ClaimMappingViewDialog } from "./ClaimMappingViewDialog";
import { useRoleLookup } from "./useRoleLookup";
import { UserRoleChips } from "./UserRoleChips";
import { originLabel } from "./utils";

const ANY_ROLE = "__any__";
const PAGE_SIZE = 20;
const SEARCH_DEBOUNCE_MS = 300;

export function ClaimMappingPage() {
    const [filter, setFilter] = useState("");
    const [roleFilter, setRoleFilter] = useState<string>(ANY_ROLE);
    const [createOpen, setCreateOpen] = useState(false);
    const [editMapping, setEditMapping] = useState<ClaimMapping | null>(null);
    const [viewMapping, setViewMapping] = useState<ClaimMapping | null>(null);

    const { sortKey, sortDir, handleSort } = useSortState("name");
    const debouncedFilter = useDebounce(filter, SEARCH_DEBOUNCE_MS);
    const search = debouncedFilter.trim() || undefined;
    const roleId = roleFilter === ANY_ROLE ? undefined : roleFilter;
    const sort = `${sortKey}:${sortDir}`;
    const [page, setPage] = usePageNumber(`${search ?? ""}|${roleId ?? ""}|${sort}`);

    const { data, isLoading, error } = useClaimMappings({ pageNumber: page, pageSize: PAGE_SIZE, search, roleId, sort });
    const { roles, roleById } = useRoleLookup();
    const emptyActions: ButtonWithCallback[] = [{ text: "Create Claim Mapping", variant: "default", onClick: () => setCreateOpen(true) }];

    const mappings = data?.data ?? [];
    const pagination = data?.meta.pagination;
    const totalCount = pagination?.count ?? 0;
    const totalPages = Math.max(1, pagination?.totalPages ?? 1);

    useEffect(() => {
        if (page > totalPages) setPage(totalPages);
    }, [page, totalPages, setPage]);

    const hasActiveFilters = filter.trim() !== "" || roleFilter !== ANY_ROLE;
    const clearFilters = () => {
        setFilter("");
        setRoleFilter(ANY_ROLE);
    };

    const pageStart = (page - 1) * PAGE_SIZE;

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-4 p-6 md:p-8">
            <OnboardingBanner
                storageKey="sam-claims-onboarding-dismissed"
                header="Grant roles by identity-provider claim."
                description="Claim mappings grant roles to everyone whose identity provider (IdP) claim matches a value, so you manage access in your IdP instead of per user. An administrator sets the claim key for the deployment."
                learnMoreText="Learn more about user management"
                learnMoreUrl={DOC_LINKS.userManagementClaimMappings}
            />

            <div className="flex flex-wrap items-center gap-3">
                <SearchInput value={filter} onChange={setFilter} placeholder="Filter by name or claim value" testid="claimMappingFilter" />
                <Select value={roleFilter} onValueChange={setRoleFilter}>
                    <SelectTrigger className="w-56" aria-label="Filter by role">
                        <SelectValue placeholder="Any Role" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={ANY_ROLE}>Any Role</SelectItem>
                        {roles.map(role => (
                            <SelectItem key={role.id} value={role.id}>
                                {role.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Button variant="outline" className="ml-auto" testid="createClaimMapping" onClick={() => setCreateOpen(true)}>
                    <Plus />
                    Create Claim Mapping
                </Button>
            </div>

            <TableStateBoundary
                resourceName="Claim Mappings"
                isLoading={isLoading}
                error={error}
                itemCount={totalCount}
                hasActiveFilters={hasActiveFilters}
                onClearFilters={clearFilters}
                emptyDescription="Claim mappings grant roles to everyone whose identity provider (IdP) claim matches a value, so you manage access in your IdP instead of per user."
                emptyActions={emptyActions}
            >
                <div className="min-h-0 flex-1 overflow-auto rounded-xs border">
                    <Table className="table-fixed" containerClassName="overflow-x-visible">
                        <TableHeader sticky>
                            <TableRow className="hover:bg-transparent">
                                <SortableTableHead column="name" currentSortKey={sortKey} sortDir={sortDir} onSort={handleSort} className="w-[22%]">
                                    Name
                                </SortableTableHead>
                                <TableHead className="w-[29%] font-semibold">Claim Value</TableHead>
                                <TableHead className="w-[12%] font-semibold">Type</TableHead>
                                <TableHead className="font-semibold">Roles</TableHead>
                                <TableHead className="w-16" aria-label="Actions" />
                            </TableRow>
                        </TableHeader>
                        <TableBody borderLastRow>
                            {mappings.map(mapping => (
                                <TableRow key={mapping.id}>
                                    <TableCell className="truncate font-medium" title={mapping.name || undefined}>
                                        {mapping.name || "—"}
                                    </TableCell>
                                    <TableCell className="truncate font-mono text-xs" title={mapping.claimValue}>
                                        {mapping.claimValue}
                                    </TableCell>
                                    <TableCell>{originLabel(mapping.origin)}</TableCell>
                                    <TableCell>
                                        <UserRoleChips roles={mapping.roleIds.map(roleId => ({ roleId }))} roleById={roleById} />
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <ClaimMappingRowActions mapping={mapping} onView={setViewMapping} onEdit={setEditMapping} />
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
                <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}–${pageStart + mappings.length} of ${totalCount}`} />
            </TableStateBoundary>

            <ClaimMappingFormDialog open={createOpen} onOpenChange={setCreateOpen} />
            <ClaimMappingFormDialog open={editMapping !== null} mapping={editMapping} onOpenChange={open => !open && setEditMapping(null)} />
            <ClaimMappingViewDialog
                mapping={viewMapping}
                roleById={roleById}
                onOpenChange={open => !open && setViewMapping(null)}
                onEdit={mapping => {
                    setViewMapping(null);
                    setEditMapping(mapping);
                }}
            />
        </div>
    );
}
