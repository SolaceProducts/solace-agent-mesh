// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { BookOpen, Download, MessageCircle, MoreHorizontal, Pencil, Share2, Trash2 } from "lucide-react";

import { useChatContext, useProjectContext } from "@/providers/contexts";

import { formatTimestamp } from "@/utils/format";

import type { Project, UpdateProjectData } from "@/types/projects";

import { useIndexingSSE } from "@/features/projects/hooks";
import { ProjectDetailsFields } from "@/features/projects/ProjectDetailsFields";
import { isProjectTextOverLimit, useProjectFieldLimits, validateProjectName } from "@/features/projects/validation";

import { Footer, Header, MessageBanner } from "@/common";
import { useIsMobile, useIsProjectOwner, useIsProjectSharingEnabled, useSessionStorage } from "@/hooks";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Spinner, Tabs, TabsList, TabsTrigger } from "@/ui";
import { cn } from "@/utils";

import { DefaultAgentSection } from "./DefaultAgentSection";
import { DeleteProjectDialog } from "./DeleteProjectDialog";
import { KnowledgeSection } from "./KnowledgeSection";
import { ProjectChatsSection } from "./ProjectChatsSection";
import { SystemPromptSection } from "./SystemPromptSection";

const getIndexingBanner = (isIndexing: boolean, indexingError: string | null, onDismiss: () => void) => {
    if (indexingError) {
        return <MessageBanner variant="error" dismissible onDismiss={onDismiss} message={indexingError} className="m-6" />;
    }
    if (isIndexing) {
        return (
            <MessageBanner
                variant="info"
                message={
                    <div className="flex gap-2">
                        <div>Processing project files for faster access...</div>
                        <Spinner size="small" />
                    </div>
                }
                className="m-6"
            />
        );
    }
    return null;
};

interface ProjectDetailViewProps {
    project: Project;
    onBack: () => void;
    onStartNewChat?: () => void;
    onChatClick?: (sessionId: string, agentId: string | null | undefined) => void;
    onShare?: () => void;
    onExport?: () => void;
}

export const ProjectDetailView = ({ project, onBack, onStartNewChat, onChatClick, onShare, onExport }: ProjectDetailViewProps) => {
    const isOwner = useIsProjectOwner(project.userId);
    const { updateProject, projects, deleteProject } = useProjectContext();
    const { maxNameLength, maxDescriptionLength } = useProjectFieldLimits();
    const { addNotification } = useChatContext();
    const isProjectSharingEnabled = useIsProjectSharingEnabled();
    const isMobile = useIsMobile();
    const [mobileTab, setMobileTab] = useState<"chats" | "context">("chats");

    const [indexingError, setIndexingError] = useSessionStorage<string | null>(`sam_indexing_error_${project.id}`, null);
    const { isIndexing } = useIndexingSSE({
        resourceId: project.id,
        onComplete: (failedFiles = [], errors = []) => {
            const messages: string[] = [...errors];
            if (failedFiles.length > 0) {
                messages.push(`Unable to process: ${failedFiles.join(", ")}`);
            }
            if (messages.length > 0) {
                setIndexingError(`${messages.join(" ")}. Please ensure all files are valid and try again.`);
            } else {
                setIndexingError(null);
                addNotification("Project files processed", "success");
            }
        },
    });

    const [isSaving, setIsSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [isEditing, setIsEditing] = useState(false);
    const [editedName, setEditedName] = useState(project.name);
    const [editedDescription, setEditedDescription] = useState(project.description || "");
    const [nameError, setNameError] = useState<string | null>(null);
    const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);
    const [isDeleting, setIsDeleting] = useState(false);

    const isDescriptionOverLimit = isProjectTextOverLimit(editedDescription, maxDescriptionLength);

    const handleSaveSystemPrompt = async (systemPrompt: string) => {
        setError(null);
        setIsSaving(true);
        try {
            const updateData: UpdateProjectData = { systemPrompt };
            await updateProject(project.id, updateData);
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : "Failed to update instructions";
            setError(errorMessage);
            throw err;
        } finally {
            setIsSaving(false);
        }
    };

    const handleSaveDefaultAgent = async (defaultAgentId: string | null) => {
        setIsSaving(true);
        try {
            const updateData: UpdateProjectData = { defaultAgentId };
            await updateProject(project.id, updateData);
        } catch (err) {
            console.error("Failed to update default agent:", err);
            throw err;
        } finally {
            setIsSaving(false);
        }
    };

    const handleSave = async () => {
        const trimmedName = editedName.trim();
        const trimmedDescription = editedDescription.trim();

        const validationError = validateProjectName(trimmedName);
        if (validationError) {
            setNameError(validationError);
            return;
        }

        const isDuplicate = projects.some(p => p.id !== project.id && p.name.toLowerCase() === trimmedName.toLowerCase());

        if (isDuplicate) {
            setNameError("A project with this name already exists");
            return;
        }

        setNameError(null);
        setIsSaving(true);
        try {
            const updateData: UpdateProjectData = {};
            if (trimmedName !== project.name) {
                updateData.name = trimmedName;
            }
            if (trimmedDescription !== (project.description || "")) {
                updateData.description = trimmedDescription;
            }

            if (Object.keys(updateData).length > 0) {
                await updateProject(project.id, updateData);
            }
            setIsEditing(false);
        } catch (err) {
            console.error("Failed to update project:", err);
            setNameError(err instanceof Error ? err.message : "Failed to update project");
        } finally {
            setIsSaving(false);
        }
    };

    const handleCancelEdit = () => {
        setEditedName(project.name);
        setEditedDescription(project.description || "");
        setIsEditing(false);
        setNameError(null);
    };

    const handleDeleteClick = () => {
        setIsDeleteDialogOpen(true);
    };

    const handleDeleteConfirm = async () => {
        setIsDeleting(true);
        try {
            await deleteProject(project.id);
            setIsDeleteDialogOpen(false);
            onBack();
        } catch (error) {
            console.error("Failed to delete project:", error);
        } finally {
            setIsDeleting(false);
        }
    };

    return (
        <div className="flex h-full flex-col">
            <Header
                title={project.name}
                breadcrumbs={[{ label: "Projects", onClick: onBack }, { label: project.name }]}
                buttons={[
                    ...(isOwner
                        ? [
                              <Button key="edit" variant="ghost" size="sm" onClick={() => setIsEditing(true)} testid="editDetailsButton" className="gap-2" disabled={isIndexing}>
                                  <Pencil className="h-4 w-4" />
                                  Edit Details
                              </Button>,
                              ...(onShare
                                  ? [
                                        <Button key="share" variant="ghost" size="sm" onClick={onShare} testid="shareButton" className="gap-2" disabled={isIndexing}>
                                            <Share2 className="h-4 w-4" />
                                            Share
                                        </Button>,
                                    ]
                                  : []),
                              <DropdownMenu key="more">
                                  <DropdownMenuTrigger asChild>
                                      <Button variant="ghost" size="sm" className="h-8 w-8 p-0" disabled={isIndexing} tooltip="More options">
                                          <MoreHorizontal className="h-4 w-4" />
                                      </Button>
                                  </DropdownMenuTrigger>
                                  <DropdownMenuContent align="end">
                                      {onExport ? (
                                          <DropdownMenuItem onClick={onExport} data-testid="exportProjectButton">
                                              <Download className="mr-2 h-4 w-4" />
                                              Export Project
                                          </DropdownMenuItem>
                                      ) : null}
                                      <DropdownMenuItem onClick={handleDeleteClick}>
                                          <Trash2 className="mr-2 h-4 w-4" />
                                          Delete
                                      </DropdownMenuItem>
                                  </DropdownMenuContent>
                              </DropdownMenu>,
                          ]
                        : []),
                ]}
            />

            {isMobile && getIndexingBanner(isIndexing, indexingError, () => setIndexingError(null))}

            {isMobile && (
                <Tabs value={mobileTab} onValueChange={v => setMobileTab(v as "chats" | "context")} className="@container px-4 pt-4">
                    <TabsList className="flex w-full bg-transparent p-0">
                        <TabsTrigger value="chats" title="Chats" className="relative min-w-0 flex-1 rounded-none rounded-l-md px-2 data-[state=active]:z-10">
                            <MessageCircle className="h-4 w-4 shrink-0" />
                            <span className="ml-1.5 hidden truncate @[240px]:inline">Chats</span>
                        </TabsTrigger>
                        <TabsTrigger value="context" title="Context" className="relative min-w-0 flex-1 rounded-none rounded-r-md border-x-0 border-y border-r px-2 data-[state=active]:z-10">
                            <BookOpen className="h-4 w-4 shrink-0" />
                            <span className="ml-1.5 hidden truncate @[240px]:inline">Context</span>
                        </TabsTrigger>
                    </TabsList>
                </Tabs>
            )}

            <div className={cn("flex min-h-0 flex-1", isMobile ? "flex-col" : "flex-row")}>
                <div className={cn("overflow-y-auto", isMobile ? "min-h-0 flex-1" : "w-[60%] border-r", isMobile && mobileTab !== "chats" && "hidden")}>
                    {!isMobile && getIndexingBanner(isIndexing, indexingError, () => setIndexingError(null))}
                    {project.description && (
                        <div className={isMobile ? "px-4 pt-4" : "px-8 py-4"}>
                            <p className="text-sm text-(--secondary-text-wMain)">{project.description}</p>
                        </div>
                    )}
                    {onChatClick && <ProjectChatsSection project={project} onChatClick={onChatClick} onStartNewChat={onStartNewChat} isDisabled={isIndexing} />}
                </div>

                <div className={cn("flex flex-col overflow-y-auto", isMobile ? "min-h-0 flex-1" : "min-h-0 w-[40%]", isMobile && mobileTab !== "context" && "hidden")}>
                    <SystemPromptSection project={project} onSave={handleSaveSystemPrompt} isSaving={isSaving} isDisabled={isIndexing} error={error} />
                    <DefaultAgentSection project={project} onSave={handleSaveDefaultAgent} isSaving={isSaving} isDisabled={isIndexing} />
                    <KnowledgeSection project={project} isDisabled={isIndexing} onFileChange={() => setIndexingError(null)} />
                </div>
            </div>

            <Footer className="justify-between">
                <div className="text-sm text-(--secondary-text-wMain)">Created on {formatTimestamp(project.createdAt, "date")}</div>
                <Button variant="outline" data-testid="closeButton" title="Close" onClick={onBack}>
                    Close
                </Button>
            </Footer>

            <Dialog open={isEditing} onOpenChange={setIsEditing}>
                <DialogContent onOpenAutoFocus={e => e.preventDefault()}>
                    <DialogHeader>
                        <DialogTitle>Edit Project Details</DialogTitle>
                        <DialogDescription>Update the name and description for this project.</DialogDescription>
                    </DialogHeader>
                    <ProjectDetailsFields
                        idPrefix="edit-project"
                        name={editedName}
                        onNameChange={value => {
                            setEditedName(value);
                            setNameError(null);
                        }}
                        nameError={nameError}
                        description={editedDescription}
                        onDescriptionChange={setEditedDescription}
                        maxNameLength={maxNameLength}
                        maxDescriptionLength={maxDescriptionLength}
                        isDescriptionOverLimit={isDescriptionOverLimit}
                        disabled={isSaving}
                    />
                    <DialogFooter>
                        <Button variant="outline" onClick={handleCancelEdit} disabled={isSaving}>
                            Discard Changes
                        </Button>
                        <Button onClick={handleSave} disabled={isSaving || isDescriptionOverLimit}>
                            Save
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <DeleteProjectDialog isOpen={isDeleteDialogOpen} onClose={() => setIsDeleteDialogOpen(false)} onConfirm={handleDeleteConfirm} project={project} isProjectSharingEnabled={isProjectSharingEnabled} isDeleting={isDeleting} />
        </div>
    );
};
