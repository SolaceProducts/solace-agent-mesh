// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ChevronsUpDown, X } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { useBuiltinSkills, useSkills } from "@/api/skills";

import { useUserContext } from "@/providers/contexts";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { PageSidePanelWrapper } from "@/common/layout";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { DiscoveryIndicator, EmptyState, FileDropZone, MessageBanner, MorePopoverButton, OnboardingBanner, PaginationControls } from "@/common";
import type { Skill } from "@/types";
import { DOC_LINKS } from "@/utils";

import { SkillMorePopover } from "./SkillMorePopover";
import { skillAssetCount, skillReferenceCount, skillToolCount } from "./skillResources";
import { SkillSidePanel } from "./SkillSidePanel";

const SKILLS_STORAGE_KEY = "sam-skills-onboarding-dismissed";
const SKILLS_HEADER = "Skills are specialized capability bundles of instructions, reference files, and tools that your agent loads on demand only when a conversation calls for it. ";
const SKILLS_DESCRIPTION = "Use skills for less frequent, more complex tasks where the agent benefits from detailed instructions and reference material alongside its tools.";
const SKILLS_LEARN_MORE_TEXT = "Learn about Skills";
const SKILLS_URL = DOC_LINKS.buildingSkills;

type SkillsSortKey = "name" | "agentCount" | "createdBy" | "toolCount" | "referenceCount" | "assetCount";

const TYPE_OPTIONS: { value: string; label: string }[] = [
    { value: "all", label: "All Types" },
    { value: "custom", label: "Custom" },
    { value: "builtin", label: "Built-in" },
];

const isBuiltin = (skill: Skill): boolean => skill.origin === "builtin";

export const SkillsView: React.FC = () => {
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreateSkills = userHasCapability(CAPABILITIES.createSkills);

    const typeParam = searchParams.get("type") ?? "all";

    const { data: customSkills, isLoading, error, refetch: skillsRefetch } = useSkills();
    const { data: builtinSkills } = useBuiltinSkills();

    const skills = useMemo(() => [...(builtinSkills ?? []), ...(customSkills ?? [])], [builtinSkills, customSkills]);

    useEffect(() => {
        const handler = () => {
            skillsRefetch();
        };
        window.addEventListener("skills-refetch", handler);
        return () => window.removeEventListener("skills-refetch", handler);
    }, [skillsRefetch]);

    const [searchTerm, setSearchTerm] = useState("");
    const [currentPage, setCurrentPage] = useState(1);
    const [sortBy, setSortBy] = useState<SkillsSortKey>("name");
    const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");
    const [screenHeight, setScreenHeight] = useState<number>(globalThis.window === undefined ? 768 : globalThis.window.innerHeight);
    const [selectedSkill, setSelectedSkill] = useState<Skill | null>(null);
    const selectedSkillIdRef = useRef<string | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    useEffect(() => {
        const handleResize = () => {
            if (globalThis.window === undefined) return;
            setScreenHeight(globalThis.window.innerHeight);
        };

        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm, typeParam]);

    useEffect(() => {
        const id = selectedSkillIdRef.current;
        if (!id) return;
        const fresh = skills?.find(s => s.id === id) ?? null;
        if (!fresh) {
            deferPanelClose(() => setIsSidePanelOpen(false));
        }
        setSelectedSkill(fresh);
    }, [skills]);

    const handleColumnSort = (column: SkillsSortKey) => {
        if (sortBy === column) {
            setSortDirection(sortDirection === "asc" ? "desc" : "asc");
        } else {
            setSortBy(column);
            setSortDirection("asc");
        }
        setCurrentPage(1);
    };

    const setTypeFilter = (value: string) => {
        const next = new URLSearchParams(searchParams);
        if (value === "all") {
            next.delete("type");
        } else {
            next.set("type", value);
        }
        setSearchParams(next, { replace: true });
    };

    const hasActiveFilters = typeParam !== "all";

    const clearFilters = () => {
        const next = new URLSearchParams(searchParams);
        next.delete("type");
        setSearchParams(next, { replace: true });
    };

    const filteredSkills = useMemo(() => {
        if (!skills || skills.length === 0) return [];

        let result = skills;

        if (typeParam === "builtin") {
            result = result.filter(s => isBuiltin(s));
        } else if (typeParam === "custom") {
            result = result.filter(s => !isBuiltin(s));
        }

        if (searchTerm.trim()) {
            result = result.filter(s => s.name.toLowerCase().includes(searchTerm.toLowerCase()));
        }

        const numericKey: Partial<Record<SkillsSortKey, (s: Skill) => number>> = {
            agentCount: s => s.agents?.length ?? 0,
            toolCount: skillToolCount,
            referenceCount: skillReferenceCount,
            assetCount: skillAssetCount,
        };

        result = result.slice().sort((a, b) => {
            const getNum = numericKey[sortBy];
            if (getNum) {
                return sortDirection === "asc" ? getNum(a) - getNum(b) : getNum(b) - getNum(a);
            }

            let aVal: string = "";
            let bVal: string = "";

            if (sortBy === "name") {
                aVal = a.name;
                bVal = b.name;
            } else if (sortBy === "createdBy") {
                aVal = a.createdBy ?? "";
                bVal = b.createdBy ?? "";
            }

            return sortDirection === "asc" ? aVal.localeCompare(bVal) : bVal.localeCompare(aVal);
        });

        return result;
    }, [skills, searchTerm, sortBy, sortDirection, typeParam]);

    const totalPages = Math.ceil(filteredSkills.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const currentSkills = filteredSkills.slice(startIndex, startIndex + itemsPerPage);

    const handlePageChange = (page: number) => {
        if (page >= 1 && page <= totalPages) {
            setCurrentPage(page);
        }
    };

    const getSortIcon = (column: SkillsSortKey) => {
        if (sortBy !== column) return <ChevronsUpDown className="ml-2 h-4 w-4 opacity-50" />;
        return sortDirection === "asc" ? <ArrowUp className="ml-2 h-4 w-4" /> : <ArrowDown className="ml-2 h-4 w-4" />;
    };

    const handleSelectSkill = (skill: Skill) => {
        selectedSkillIdRef.current = skill.id;
        setSelectedSkill(skill);
        setIsSidePanelOpen(true);
    };

    const handleCloseSidePanel = () => {
        selectedSkillIdRef.current = null;
        setSelectedSkill(null);
        setIsSidePanelOpen(false);
    };

    if (isLoading) {
        return <EmptyState variant="loading" title="Loading Skills..." />;
    }

    if (error) {
        return (
            <div className="flex h-96 items-center justify-center">
                <MessageBanner variant="error" message={`Error loading skills. ${error instanceof Error ? error.message : String(error)}`} />
            </div>
        );
    }

    const showToolbar = (skills && skills.length > 0) || !!searchTerm || hasActiveFilters;

    return (
        <FileDropZone overlayLabel="Drop skill ZIP to upload" disabled={!canCreateSkills} onZipDropped={zip => navigate("/skills/upload", { state: { droppedFile: zip } })}>
            <div className="flex min-w-0 flex-1 flex-col">
                <OnboardingBanner storageKey={SKILLS_STORAGE_KEY} header={SKILLS_HEADER} description={SKILLS_DESCRIPTION} learnMoreText={SKILLS_LEARN_MORE_TEXT} learnMoreUrl={SKILLS_URL} className="mx-6 mt-6" />

                {showToolbar && (
                    <div className="mx-6 mt-6 flex flex-wrap items-center gap-2">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="skillsSearchInput" />
                        <Select value={typeParam} onValueChange={setTypeFilter}>
                            <SelectTrigger className="h-9 w-40" data-testid="skillsTypeFilter">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {TYPE_OPTIONS.map(opt => (
                                    <SelectItem key={opt.value} value={opt.value}>
                                        {opt.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {hasActiveFilters && (
                            <Button variant="ghost" size="sm" className="h-9 gap-1 px-2 text-sm" onClick={clearFilters} data-testid="skillsClearFilters">
                                <X className="h-3.5 w-3.5" />
                                Clear filters
                            </Button>
                        )}
                    </div>
                )}

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                    <div className="flex min-h-0 flex-1 flex-col pt-6">
                        {currentSkills.length > 0 ? (
                            <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border" style={{ scrollbarGutter: "stable" }}>
                                <Table containerClassName="overflow-x-visible">
                                    <TableHeader sticky>
                                        <TableRow>
                                            <TableHead className="cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:w-1/4" onClick={() => handleColumnSort("name")}>
                                                <div className="flex items-center">
                                                    <div className="pl-5">Name</div>
                                                    {getSortIcon("name")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden w-24 font-semibold sm:table-cell">Type</TableHead>
                                            <TableHead className="hidden w-24 cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell" onClick={() => handleColumnSort("toolCount")}>
                                                <div className="flex items-center">
                                                    Tools
                                                    {getSortIcon("toolCount")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden w-28 cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell" onClick={() => handleColumnSort("referenceCount")}>
                                                <div className="flex items-center" title="Bundled reference docs (references/)">
                                                    References
                                                    {getSortIcon("referenceCount")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden w-24 cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell" onClick={() => handleColumnSort("assetCount")}>
                                                <div className="flex items-center" title="Bundled asset files (assets/)">
                                                    Assets
                                                    {getSortIcon("assetCount")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell sm:w-1/5" onClick={() => handleColumnSort("agentCount")}>
                                                <div className="flex items-center">
                                                    Agent Usage
                                                    {getSortIcon("agentCount")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell sm:w-1/4" onClick={() => handleColumnSort("createdBy")}>
                                                <div className="flex items-center">
                                                    Created By
                                                    {getSortIcon("createdBy")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="w-12 text-right" />
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody borderLastRow>
                                        {currentSkills.map(skill => (
                                            <TableRow
                                                key={skill.id}
                                                data-testid={`skillRow-${skill.id}`}
                                                className="cursor-pointer hover:bg-(--secondary-w10)/50"
                                                onClick={() => handleSelectSkill(skill)}
                                                data-state={selectedSkill?.id === skill.id ? "selected" : undefined}
                                            >
                                                <TableCell className="sm:w-1/4">
                                                    <div className="flex min-w-0 items-center gap-2 pl-5">
                                                        <Button
                                                            title={skill.name}
                                                            variant="link"
                                                            className="min-w-0 p-0"
                                                            onClick={e => {
                                                                e.stopPropagation();
                                                                if (isBuiltin(skill)) {
                                                                    handleSelectSkill(skill);
                                                                } else {
                                                                    navigate(`/skills/${skill.id}`);
                                                                }
                                                            }}
                                                        >
                                                            <span className="truncate">{skill.name}</span>
                                                        </Button>
                                                        <DiscoveryIndicator status={skill.discoveryStatus} isBuiltin={isBuiltin(skill)} testIdPrefix="skillStatus" inProgressLabel="Processing" />
                                                    </div>
                                                </TableCell>
                                                <TableCell data-testid={`skillTypeCell-${skill.id}`} className="hidden w-24 sm:table-cell">
                                                    {isBuiltin(skill) ? "Built-in" : "Custom"}
                                                </TableCell>
                                                <TableCell data-testid={`toolCountCell-${skill.id}`} className="hidden w-24 sm:table-cell">
                                                    {skillToolCount(skill)}
                                                </TableCell>
                                                <TableCell data-testid={`referenceCountCell-${skill.id}`} className="hidden w-28 sm:table-cell">
                                                    {skillReferenceCount(skill)}
                                                </TableCell>
                                                <TableCell data-testid={`assetCountCell-${skill.id}`} className="hidden w-24 sm:table-cell">
                                                    {skillAssetCount(skill)}
                                                </TableCell>
                                                <TableCell data-testid={`agentCountCell-${skill.id}`} className="hidden sm:table-cell sm:w-1/5">
                                                    {skill.agents?.length ?? 0}
                                                </TableCell>
                                                <TableCell className="hidden sm:table-cell sm:w-1/4">{skill.createdBy === "" ? "—" : skill.createdBy}</TableCell>
                                                <TableCell className="w-12 text-right">
                                                    {!isBuiltin(skill) && (
                                                        <SkillMorePopover key="more-popover" skill={skill}>
                                                            {MorePopoverButton}
                                                        </SkillMorePopover>
                                                    )}
                                                </TableCell>
                                            </TableRow>
                                        ))}
                                    </TableBody>
                                </Table>
                            </div>
                        ) : (
                            <div className="flex min-h-[300px] flex-1 items-center justify-center">
                                {searchTerm || hasActiveFilters ? (
                                    <EmptyState
                                        variant="noImage"
                                        title="No skills match your filters."
                                        subtitle="Try adjusting your search terms or filters."
                                        buttons={[
                                            {
                                                text: "Clear filters",
                                                variant: "default",
                                                onClick: () => {
                                                    setSearchTerm("");
                                                    clearFilters();
                                                },
                                            },
                                        ]}
                                    />
                                ) : (
                                    <div className="flex flex-col items-center gap-4">
                                        <EmptyState
                                            className="max-w-2xl text-center"
                                            variant="noImage"
                                            title="Upload skills to extend your agents"
                                            subtitle="Skills are ZIP archives containing SKILL.md instructions, reference files, and bundled tools. Agents load skills at runtime to gain new capabilities."
                                        />
                                        {canCreateSkills && (
                                            <Button className="w-min" onClick={() => navigate("/skills/upload")}>
                                                Upload Skill
                                            </Button>
                                        )}
                                    </div>
                                )}
                            </div>
                        )}
                    </div>
                </div>
                <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={handlePageChange} />
            </div>

            {selectedSkill && (
                <PageSidePanelWrapper
                    isSidePanelOpen={isSidePanelOpen}
                    onClose={handleCloseSidePanel}
                    sidepanel={<SkillSidePanel key={selectedSkill.id} skill={selectedSkill} onClose={handleCloseSidePanel} onOpen={isBuiltin(selectedSkill) ? undefined : () => navigate(`/skills/${selectedSkill.id}`)} />}
                />
            )}
        </FileDropZone>
    );
};
