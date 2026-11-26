// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { ChevronDown, ChevronRight, ExternalLink, Loader2, Plus, Search, Wrench } from "lucide-react";
import { useForm } from "react-hook-form";
import { Link } from "react-router-dom";
import { z } from "zod";

import { toolsetsApi, useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { CustomCheckbox } from "@/common/CustomCheckbox";
import { EmptySection, getSectionTitle } from "@/common/layout";
import { reportErrorDisplayed } from "@/common/product-analytics/reportErrorDisplayed";

import { getErrorMessage } from "@/utils/api";
import { toolsetRefLabel } from "@/utils/toolsetRefLabel";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";

import { ToolSelectList } from "@/features/toolsets/ToolSelectList";
import { ToolsetCreateDialog } from "@/features/toolsets/ToolsetCreateDialog";
import { ToolsetZipUploadDialog } from "@/features/toolsets/ToolsetZipUploadDialog";
import { saveToolset } from "@/features/toolsets/utils";

import { LearningBanner, NumberedStep } from "@/common";
import type { Agent, ToolsetConfigBinding } from "@/types";
import { DOC_LINKS } from "@/utils";

import { agentToolsetsSchema } from "./agentEditSchemas";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentEditSection, AgentEditWrapper } from "./common";

type AgentToolsetsFormData = z.infer<typeof agentToolsetsSchema>;

interface AgentToolsetsEditProps {
    agent: Agent;
    onApply: (agent: Agent) => void;
    onDiscard: () => void;
}

export function AgentToolsetsEdit({ agent, onApply, onDiscard }: AgentToolsetsEditProps) {
    const { addNotification } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreateToolsets = userHasCapability(CAPABILITIES.createToolsets);
    const { data: toolsets = [], isLoading: toolsetsLoading, isSuccess: toolsetsLoaded } = useBuiltinToolsets();
    const { data: customToolsets = [], isLoading: customToolsetsLoading, isSuccess: customToolsetsLoaded, refetch: customToolsetsRefetch } = useCustomToolsets();
    const [excludedTools, setExcludedTools] = useState<Record<string, string[]>>(agent.toolsetExcludedTools || {});
    const [toolsetConfigs, setToolsetConfigs] = useState<Record<string, ToolsetConfigBinding>>(() => {
        const map: Record<string, ToolsetConfigBinding> = {};
        for (const binding of agent.toolsetConfigs || []) {
            map[binding.toolsetName] = binding;
        }
        return map;
    });
    const [searchTerm, setSearchTerm] = useState("");

    const [expandedToolsets, setExpandedToolsets] = useState<Set<string>>(new Set());

    const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
    const [isUploadDialogOpen, setIsUploadDialogOpen] = useState(false);
    const [selectedFile, setSelectedFile] = useState<File | null>(null);

    const { handleSubmit, watch, setValue } = useForm<AgentToolsetsFormData>({
        resolver: zodResolver(agentToolsetsSchema),
        defaultValues: {
            selectedToolsets: agent.toolsets || [],
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const selectedToolsets = watch("selectedToolsets");

    const onSubmit = (data: AgentToolsetsFormData) => {
        const bindings: ToolsetConfigBinding[] = Object.entries(toolsetConfigs)
            .filter(([name]) => data.selectedToolsets.includes(name))
            .map(([toolsetName, binding]) => ({ ...binding, toolsetName }));

        const excluded = Object.fromEntries(Object.entries(excludedTools).filter(([name]) => data.selectedToolsets.includes(name)));

        const mustSendToolsetConfigs = bindings.length > 0 || (agent.toolsetConfigs?.length ?? 0) > 0;
        onApply({
            ...agent,
            toolsets: data.selectedToolsets,
            toolsetExcludedTools: excluded,
            toolsetConfigs: mustSendToolsetConfigs ? bindings : undefined,
        });
    };

    const handleToolConfigSave = useCallback((toolsetName: string, toolName: string, values: Record<string, unknown>) => {
        setToolsetConfigs(prev => {
            const existing = prev[toolsetName];
            return {
                ...prev,
                [toolsetName]: {
                    ...existing,
                    toolsetName,
                    configValues: { ...existing?.configValues, [toolName]: values },
                },
            };
        });
    }, []);

    const setToolsetExclusions = useCallback((toolsetId: string, excluded: string[]) => {
        setExcludedTools(prev => {
            const next = { ...prev };
            if (excluded.length > 0) {
                next[toolsetId] = excluded;
            } else {
                delete next[toolsetId];
            }
            return next;
        });
    }, []);

    const bindToolset = useCallback(
        (toolsetId: string) => {
            if (!selectedToolsets.includes(toolsetId)) {
                setValue("selectedToolsets", [...selectedToolsets, toolsetId]);
            }
            setToolsetExclusions(toolsetId, []);
        },
        [selectedToolsets, setValue, setToolsetExclusions]
    );

    const unbindToolset = useCallback(
        (toolsetId: string) => {
            setValue(
                "selectedToolsets",
                selectedToolsets.filter(tsId => tsId !== toolsetId)
            );
            setToolsetExclusions(toolsetId, []);
        },
        [selectedToolsets, setValue, setToolsetExclusions]
    );

    const grantAllTools = useCallback(
        (toolsetId: string, allTools: string[]) => {
            if (!selectedToolsets.includes(toolsetId)) {
                bindToolset(toolsetId);
                return;
            }
            const unlistedExclusions = (excludedTools[toolsetId] ?? []).filter(name => !allTools.includes(name));
            setToolsetExclusions(toolsetId, unlistedExclusions);
        },
        [selectedToolsets, excludedTools, bindToolset, setToolsetExclusions]
    );

    const handleToggleToolset = useCallback(
        (id: string) => {
            if (selectedToolsets.includes(id)) {
                unbindToolset(id);
            } else {
                bindToolset(id);
            }
        },
        [selectedToolsets, bindToolset, unbindToolset]
    );

    const toggleToolsetExpanded = useCallback((toolsetId: string) => {
        setExpandedToolsets(prev => {
            const next = new Set(prev);
            if (next.has(toolsetId)) {
                next.delete(toolsetId);
            } else {
                next.add(toolsetId);
            }
            return next;
        });
    }, []);

    const handleToggleTool = useCallback(
        (toolsetId: string, toolName: string, allTools: string[]) => {
            const isBound = selectedToolsets.includes(toolsetId);
            const excluded = new Set(isBound ? excludedTools[toolsetId] : allTools);
            const isGrantedAfterToggle = (name: string) => (name === toolName ? excluded.has(name) : !excluded.has(name));
            const nextExcludedListed = allTools.filter(name => !isGrantedAfterToggle(name));

            if (nextExcludedListed.length === allTools.length) {
                unbindToolset(toolsetId);
                return;
            }
            const unlistedExclusions = [...excluded].filter(name => !allTools.includes(name));
            bindToolset(toolsetId);
            setToolsetExclusions(toolsetId, [...nextExcludedListed, ...unlistedExclusions]);
        },
        [selectedToolsets, excludedTools, bindToolset, unbindToolset, setToolsetExclusions]
    );

    const handleCreateToolset = useCallback(() => {
        setIsUploadDialogOpen(true);
    }, []);

    const handleFileSelected = useCallback((file: File) => {
        setSelectedFile(file);
        setIsUploadDialogOpen(false);
    }, []);

    useEffect(() => {
        if (selectedFile && !isUploadDialogOpen && !isCreateDialogOpen) {
            setIsCreateDialogOpen(true);
        }
    }, [selectedFile, isUploadDialogOpen, isCreateDialogOpen]);

    const handleCreateSubmit = useCallback(
        async (name: string, description: string) => {
            if (!selectedFile) {
                addNotification("Please select a file first");
                return;
            }

            try {
                await saveToolset(toolsetsApi, name, description, selectedFile);

                setIsCreateDialogOpen(false);
                setSelectedFile(null);

                await customToolsetsRefetch();
                setValue("selectedToolsets", [...selectedToolsets, name]);
                addNotification("Toolset created successfully");
            } catch (error) {
                const message = getErrorMessage(error, "Failed to create toolset");
                reportErrorDisplayed(message, "api");
                addNotification(message);
            }
        },
        [selectedFile, addNotification, customToolsetsRefetch, selectedToolsets, setValue]
    );

    if (toolsetsLoading || customToolsetsLoading) {
        return (
            <div className="flex flex-1 items-center justify-center">
                <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
            </div>
        );
    }

    if (!toolsetsLoaded || !customToolsetsLoaded) {
        return (
            <div className="flex flex-1 items-center justify-center p-8 text-center text-sm">
                Couldn't load the toolset catalog — you may not have permission to view toolsets, or the request failed. This agent's toolset assignments are unaffected; try again later to edit them here.
            </div>
        );
    }

    const searchLower = searchTerm.toLowerCase();
    const filteredBuiltinToolsets = (toolsets || []).filter(ts => !searchTerm || ts.name.toLowerCase().includes(searchLower));
    const filteredCustomToolsets = (customToolsets || []).filter(ts => !searchTerm || ts.name.toLowerCase().includes(searchLower));

    const catalogBuiltinIds = new Set((toolsets || []).map(ts => ts.id));
    const catalogCustomNames = new Set((customToolsets || []).map(ts => ts.name));
    const filteredUnresolvedRefs = selectedToolsets.filter(ref => !catalogBuiltinIds.has(ref) && !catalogCustomNames.has(ref) && (!searchTerm || ref.toLowerCase().includes(searchLower) || toolsetRefLabel(ref).toLowerCase().includes(searchLower)));

    const hasNoResults = searchTerm && filteredBuiltinToolsets.length === 0 && filteredCustomToolsets.length === 0 && filteredUnresolvedRefs.length === 0;

    const learningBannerHint = !hasNoResults ? (
        <LearningBanner title="Extend Agent with Custom Toolsets" storageKey="sam-custom-tools-learning-2-dismissed" showIcon={false}>
            <p className="mb-4">Custom toolsets are code packages that extend your agent beyond built-in capabilities—adding unique features your organization needs.</p>

            <div className="space-y-4">
                <NumberedStep
                    number={1}
                    title="Before Getting Started"
                    description="Before uploading, the toolset code must be developed, packaged, and ready for upload."
                    button={{ label: "Learn about Custom Tool Development", href: DOC_LINKS.buildingToolsets }}
                />
                <NumberedStep
                    number={2}
                    title="Create Custom Toolset"
                    description="Create and upload a custom toolset directly from the agent builder through the Create Toolset button."
                    button={{ label: "Open Custom Toolset page", href: "/toolsets" }}
                />
                <NumberedStep number={3} title="Add Tool from Agent Mesh" description="The new custom toolset appears in the Custom Toolsets list and can be selected to add it to the agent." />
            </div>
        </LearningBanner>
    ) : undefined;

    return (
        <>
            <AgentEditWrapper sectionTitle={getSectionTitle(Wrench, "Toolsets")} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard} hints={learningBannerHint}>
                <AgentEditSection>
                    <div className="pb-4">Select a built-in tool or a custom toolset to accomplish tasks.</div>

                    <div className="mb-6">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" />
                    </div>

                    {hasNoResults ? (
                        <div className="absolute top-1/2 left-1/2 flex min-h-[400px] w-screen -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center py-20">
                            <div className="mb-6 flex h-24 w-24 items-center justify-center rounded-full bg-(--secondary-w10)">
                                <Search className="size-12 text-(--secondary-text-wMain)" />
                            </div>
                            <h3 className="mb-3 text-lg font-semibold">No results found</h3>
                            <p className="mb-6 max-w-md text-center text-sm">Adjust the search criteria or create a custom toolset to extend capabilities beyond the built-in catalog.</p>
                            <Button variant="default" onClick={() => setSearchTerm("")} className="mb-3">
                                Clear All
                            </Button>
                            <Link to={DOC_LINKS.buildingToolsets} target="_blank" rel="noopener noreferrer">
                                <Button variant="link" className="p-0 has-[>svg]:px-0" title="Learn about Custom Tools">
                                    <div className="truncate">Learn about Custom Toolsets</div>
                                    <ExternalLink />
                                </Button>
                            </Link>
                        </div>
                    ) : (
                        <>
                            <div className="mb-8">
                                <h3 className="mb-2 text-sm font-semibold">Built-in Toolsets</h3>
                                <p className="mb-4 text-sm">Select toolsets from the catalog to give an agent tools to accomplish tasks.</p>

                                <div className="space-y-2">
                                    {filteredBuiltinToolsets.map(toolset => {
                                        const isSelected = selectedToolsets.includes(toolset.id);
                                        const isExpanded = expandedToolsets.has(toolset.id);
                                        const excluded = excludedTools[toolset.id] || [];
                                        const allTools = (toolset.tools ?? []).map(t => t.toolName);
                                        const grantedTools = allTools.filter(name => !excluded.includes(name));
                                        const canNarrow = allTools.length > 0;
                                        const checkedTools = new Set(isSelected ? grantedTools : []);

                                        return (
                                            <div key={toolset.id} className="rounded-sm border">
                                                <div className="flex items-center gap-3 p-4">
                                                    <CustomCheckbox checked={isSelected} onChange={() => handleToggleToolset(toolset.id)} ariaLabel={toolset.name} />

                                                    <div className="min-w-0 flex-1">
                                                        <div className="flex items-center justify-between">
                                                            <div className="flex items-center gap-2">
                                                                <div className="mr-2 flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-(--secondary-w10)">
                                                                    <Wrench className="size-5" />
                                                                </div>
                                                                <div>
                                                                    <div className="flex items-center gap-2">
                                                                        <span className="font-medium">{toolset.name}</span>
                                                                        {isSelected && grantedTools.length < allTools.length && (
                                                                            <span data-testid={`builtin-narrowed-${toolset.id}`} className="rounded-full bg-(--secondary-w10) px-2 py-0.5 text-xs">
                                                                                {grantedTools.length} of {allTools.length} tools
                                                                            </span>
                                                                        )}
                                                                    </div>
                                                                    <div className="text-sm">{toolset.description}</div>
                                                                </div>
                                                            </div>
                                                            {canNarrow && (
                                                                <button type="button" aria-label={`${isExpanded ? "Collapse" : "Expand"} ${toolset.name}`} data-testid={`expand-toolset-${toolset.id}`} onClick={() => toggleToolsetExpanded(toolset.id)}>
                                                                    {isExpanded ? <ChevronDown className="size-5" /> : <ChevronRight className="size-5" />}
                                                                </button>
                                                            )}
                                                        </div>
                                                    </div>
                                                </div>

                                                {canNarrow && isExpanded && (
                                                    <div className="border-t p-4">
                                                        <ToolSelectList
                                                            tools={(toolset.tools ?? []).map(t => ({ name: t.toolName, description: t.toolDescription }))}
                                                            checkedTools={checkedTools}
                                                            onToggle={toolName => handleToggleTool(toolset.id, toolName, allTools)}
                                                            onSelectAll={() => grantAllTools(toolset.id, allTools)}
                                                            onDeselectAll={() => unbindToolset(toolset.id)}
                                                            testId={`tool-select-list-${toolset.id}`}
                                                            selectAllId={`select-all-${toolset.id}`}
                                                        />
                                                    </div>
                                                )}
                                            </div>
                                        );
                                    })}
                                    {filteredBuiltinToolsets.length === 0 && <EmptySection message="No built-in toolsets found" />}
                                </div>
                            </div>

                            <div>
                                <div className="mb-2 flex items-center justify-between">
                                    <h3 className="text-sm font-semibold">Custom Toolsets</h3>
                                    {canCreateToolsets && (
                                        <Button variant="ghost" size="sm" onClick={handleCreateToolset}>
                                            <Plus className="size-4" />
                                            Create Toolset
                                        </Button>
                                    )}
                                </div>
                                <p className="mb-4 text-sm">Select custom toolsets uploaded by organization members to extend an agent's capabilities beyond the built-in catalog.</p>

                                <div className="space-y-2">
                                    {filteredCustomToolsets.map(toolset => {
                                        const isSelected = selectedToolsets.includes(toolset.name);
                                        const isExpanded = expandedToolsets.has(toolset.name);
                                        const excluded = excludedTools[toolset.name] || [];
                                        const allTools = toolset.files.map(f => f.toolName);
                                        const grantedTools = allTools.filter(name => !excluded.includes(name));
                                        const checkedTools = new Set(isSelected ? grantedTools : []);

                                        return (
                                            <div key={toolset.name} className="rounded-sm border">
                                                <div className="flex items-center gap-3 p-4">
                                                    <CustomCheckbox checked={isSelected} onChange={() => handleToggleToolset(toolset.name)} ariaLabel={toolset.name} />

                                                    <div className="min-w-0 flex-1">
                                                        <div className="flex items-center justify-between">
                                                            <div className="flex items-center gap-2">
                                                                <div className="mr-2 flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-(--secondary-w10)">
                                                                    <Wrench className="size-5" />
                                                                </div>
                                                                <div>
                                                                    <div className="font-medium">{toolset.name}</div>
                                                                    <div className="text-sm">{toolset.description}</div>
                                                                </div>
                                                            </div>
                                                            <button type="button" data-testid={`expand-toolset-${toolset.name}`} onClick={() => toggleToolsetExpanded(toolset.name)}>
                                                                {isExpanded ? <ChevronDown className="size-5" /> : <ChevronRight className="size-5" />}
                                                            </button>
                                                        </div>
                                                    </div>
                                                </div>

                                                {isExpanded && (
                                                    <div className="border-t p-4">
                                                        <ToolSelectList
                                                            tools={allTools.map(toolName => {
                                                                const file = toolset.files.find(f => f.toolName === toolName);
                                                                return {
                                                                    name: toolName,
                                                                    description: file?.toolDescription,
                                                                    configSchema: file?.configSchema,
                                                                };
                                                            })}
                                                            checkedTools={checkedTools}
                                                            onToggle={toolName => handleToggleTool(toolset.name, toolName, allTools)}
                                                            onSelectAll={() => grantAllTools(toolset.name, allTools)}
                                                            onDeselectAll={() => unbindToolset(toolset.name)}
                                                            discoveryStatus={toolset.discoveryStatus}
                                                            testId={`tool-select-list-${toolset.name}`}
                                                            configValues={toolsetConfigs[toolset.name]?.configValues}
                                                            onConfigSave={(toolName, values) => handleToolConfigSave(toolset.name, toolName, values)}
                                                        />
                                                    </div>
                                                )}
                                            </div>
                                        );
                                    })}
                                    {filteredCustomToolsets.length === 0 && <EmptySection message={searchTerm ? "No custom toolsets found" : "No custom toolsets available"} />}
                                </div>
                            </div>

                            {filteredUnresolvedRefs.length > 0 && (
                                <div className="mt-8">
                                    <h3 className="mb-2 text-sm font-semibold">Other Toolsets</h3>
                                    <p className="mb-4 text-sm">Assigned to this agent but not available to pick from the catalog. Not editable here.</p>
                                    <div className="space-y-2">
                                        {filteredUnresolvedRefs.map(ref => (
                                            <div key={ref} className="flex items-center gap-3 rounded-sm border p-4">
                                                <CustomCheckbox checked={true} disabled={true} onChange={() => {}} ariaLabel={toolsetRefLabel(ref)} />
                                                <div className="mr-2 flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-(--secondary-w10)">
                                                    <Wrench className="size-5" />
                                                </div>
                                                <div className="font-medium">{toolsetRefLabel(ref)}</div>
                                            </div>
                                        ))}
                                    </div>
                                </div>
                            )}
                        </>
                    )}
                </AgentEditSection>
            </AgentEditWrapper>

            <ToolsetZipUploadDialog
                isOpen={isUploadDialogOpen}
                toolsetId={null}
                onFileSelected={handleFileSelected}
                onClose={() => {
                    setIsUploadDialogOpen(false);
                    setSelectedFile(null);
                }}
            />

            <ToolsetCreateDialog
                isOpen={isCreateDialogOpen}
                onCreate={handleCreateSubmit}
                filename={selectedFile?.name}
                onRemoveFile={() => {
                    setSelectedFile(null);
                    setIsCreateDialogOpen(false);
                    setIsUploadDialogOpen(true);
                }}
                onClose={() => {
                    setIsCreateDialogOpen(false);
                    setSelectedFile(null);
                }}
            />
        </>
    );
}
