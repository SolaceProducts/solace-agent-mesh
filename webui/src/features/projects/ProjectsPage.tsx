// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { RefreshCcw, Upload } from "lucide-react";
import { useLoaderData, useNavigate } from "react-router-dom";

import { useExportProject, useFetchProjectsOnMount, useImportProject, useTogglePinProject } from "@/api/projects/hooks";

import { useAuthContext, useChatContext, useConfigContext, useProjectContext } from "@/providers/contexts";

import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";

import { Button } from "@/ui/button";
import type { Project } from "@/types/projects";

import { useStartIndexing } from "@/features/projects/hooks";

import { useIsProjectSharingEnabled } from "@/hooks";
import { downloadBlob, getErrorMessage, resolveAgentSessionRoute } from "@/utils";

import { CreateProjectDialog } from "./CreateProjectDialog";
import { DeleteProjectDialog } from "./DeleteProjectDialog";
import { selectOwnedProjectNames } from "./ownership";
import { ProjectCards } from "./ProjectCards";
import { ProjectDetailView } from "./ProjectDetailView";
import { ProjectImportDialog } from "./ProjectImportDialog";
import { ShareProjectDialog } from "./ShareProjectDialog";

export const ProjectsPage: React.FC = () => {
    useFetchProjectsOnMount();
    const navigate = useNavigate();
    const loaderData = useLoaderData<{ projectId?: string }>();

    const { projects, isLoading, createProject, activeProject, setActiveProject, refetch, searchQuery, setSearchQuery, filteredProjects, deleteProject } = useProjectContext();
    const { handleNewSession, handleSwitchSession, addNotification, displayError, agentSessionRoutes } = useChatContext();
    const isSharingEnabled = useIsProjectSharingEnabled();
    const { userInfo } = useAuthContext();
    const { configUseAuthorization } = useConfigContext();

    const ownedProjectNames = useMemo(() => selectOwnedProjectNames(projects, { username: userInfo?.username as string | undefined, configUseAuthorization, isSharingEnabled }), [projects, userInfo, configUseAuthorization, isSharingEnabled]);
    const startIndexing = useStartIndexing();
    const exportProjectMutation = useExportProject();
    const importProjectMutation = useImportProject();
    const togglePinMutation = useTogglePinProject();

    const [showCreateDialog, setShowCreateDialog] = useState(false);
    const [isCreating, setIsCreating] = useState(false);
    const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);
    const [projectToDelete, setProjectToDelete] = useState<Project | null>(null);
    const [isDeleting, setIsDeleting] = useState(false);
    const [showImportDialog, setShowImportDialog] = useState(false);
    const [selectedProject, setSelectedProject] = useState<Project | null>(null);
    const [isShareDialogOpen, setIsShareDialogOpen] = useState(false);
    const [projectToShare, setProjectToShare] = useState<Project | null>(null);

    useEffect(() => {
        if (loaderData?.projectId) {
            const project = projects.find(p => p.id === loaderData.projectId) || null;
            setSelectedProject(project);
        } else if (activeProject) {
            navigate(`/projects/${activeProject.id}`, { replace: true });
        } else {
            setSelectedProject(null);
            setActiveProject(null);
        }
    }, [loaderData?.projectId, activeProject, projects, navigate, setActiveProject]);

    const handleCreateProject = async (data: { name: string; description: string }) => {
        setIsCreating(true);
        try {
            const formData = new FormData();
            formData.append("name", data.name);
            if (data.description) {
                formData.append("description", data.description);
            }

            const newProject = await createProject(formData);
            setShowCreateDialog(false);

            navigate(`/projects/${newProject.id}`);

            refetch().catch(() => {});
        } finally {
            setIsCreating(false);
        }
    };

    const handleProjectSelect = (project: Project) => {
        navigate(`/projects/${project.id}`);
    };

    const handleBackToList = () => {
        setActiveProject(null);
        navigate("/projects");
    };

    const handleChatClick = async (sessionId: string, agentId: string | null | undefined) => {
        if (selectedProject) {
            setActiveProject(selectedProject);
        }
        const route = resolveAgentSessionRoute(agentId, agentSessionRoutes) || "/chat";
        if (route !== "/chat") {
            window.dispatchEvent(
                new CustomEvent("agent-session-navigate", {
                    detail: { sessionId, route, agentId },
                })
            );
            return;
        }
        await handleSwitchSession(sessionId);
        navigate("chat");
    };

    const handleCreateNew = () => {
        setShowCreateDialog(true);
    };

    const handleDeleteClick = (project: Project) => {
        setProjectToDelete(project);
        setIsDeleteDialogOpen(true);
    };

    const handleDeleteConfirm = async () => {
        if (!projectToDelete) return;

        setIsDeleting(true);
        try {
            await deleteProject(projectToDelete.id);
            setIsDeleteDialogOpen(false);
            setProjectToDelete(null);
        } catch (error) {
            console.error("Failed to delete project:", error);
        } finally {
            setIsDeleting(false);
        }
    };

    const handleStartNewChat = useCallback(async () => {
        if (selectedProject) {
            setActiveProject(selectedProject);
            await handleNewSession(true);
            navigate("chat");
        }
    }, [selectedProject, setActiveProject, handleNewSession, navigate]);

    const handleExport = async (project: Project) => {
        try {
            const blob = await exportProjectMutation.mutateAsync(project.id);
            const filename = `project-${project.name.replace(/[^a-z0-9]/gi, "-").toLowerCase()}-${Date.now()}.zip`;
            downloadBlob(blob, filename);

            addNotification("Project exported", "success");
        } catch (error) {
            console.error("Failed to export project:", error);
            displayError({ title: "Failed to Export Project", error: getErrorMessage(error, "An unknown error occurred while exporting the project."), errorSource: "api" });
        }
    };

    const handleImport = async (file: File, options: { preserveName: boolean; customName?: string }) => {
        try {
            const result = await importProjectMutation.mutateAsync({ file, options });

            if (result.warnings && result.warnings.length > 0) {
                const warningMessage = result.warnings.length === 1 ? result.warnings[0] : `Import completed with ${result.warnings.length} warnings:\n${result.warnings.join("\n")}`;
                addNotification(warningMessage, "info");
            }

            if (result.sseLocation) {
                startIndexing(result.sseLocation, result.projectId, "import");
            }

            navigate(`/projects/${result.projectId}`);
            addNotification(`Project imported with ${result.artifactsImported} artifacts`, "success");
        } catch (error) {
            console.error("Failed to import project:", error);
            throw error;
        }
    };

    const handleShareClick = (project: Project) => {
        setProjectToShare(project);
        setIsShareDialogOpen(true);
    };

    const handleTogglePin = async (project: Project) => {
        if (togglePinMutation.isPending) return;
        try {
            await togglePinMutation.mutateAsync(project.id);
        } catch (error) {
            console.error("Failed to toggle pin status:", error);
            displayError({ title: "Failed to Update Favorite", error: "An error occurred while updating the favorite status.", errorSource: "api" });
        }
    };

    const handleShareDialogClose = () => {
        setIsShareDialogOpen(false);
        setProjectToShare(null);
    };

    const showDetailView = selectedProject !== null;

    return (
        <PageLayout>
            {!showDetailView && (
                <Header
                    title="Projects"
                    buttons={[
                        <Button key="importProject" variant="ghost" title="Import Project" onClick={() => setShowImportDialog(true)}>
                            <Upload className="size-4" />
                            <span className="hidden sm:inline">Import Project</span>
                        </Button>,
                        <Button key="refreshProjects" data-testid="refreshProjects" disabled={isLoading} variant="ghost" title="Refresh Projects" onClick={() => refetch()}>
                            <RefreshCcw className="size-4" />
                            <span className="hidden sm:inline">Refresh Projects</span>
                        </Button>,
                    ]}
                />
            )}

            <div className="min-h-0 flex-1">
                {showDetailView ? (
                    <ProjectDetailView
                        project={selectedProject}
                        onBack={handleBackToList}
                        onStartNewChat={handleStartNewChat}
                        onChatClick={handleChatClick}
                        onShare={isSharingEnabled ? () => handleShareClick(selectedProject) : undefined}
                        onExport={() => handleExport(selectedProject)}
                    />
                ) : (
                    <ProjectCards
                        projects={filteredProjects}
                        searchQuery={searchQuery}
                        onSearchChange={setSearchQuery}
                        onProjectClick={handleProjectSelect}
                        onCreateNew={handleCreateNew}
                        onDelete={handleDeleteClick}
                        onExport={handleExport}
                        isLoading={isLoading}
                        onShare={isSharingEnabled ? handleShareClick : undefined}
                        onTogglePin={handleTogglePin}
                        isPinToggling={togglePinMutation.isPending}
                    />
                )}
            </div>

            <CreateProjectDialog isOpen={showCreateDialog} onClose={() => setShowCreateDialog(false)} onSubmit={handleCreateProject} isSubmitting={isCreating} existingNames={ownedProjectNames} />

            <DeleteProjectDialog
                isOpen={isDeleteDialogOpen}
                onClose={() => {
                    setIsDeleteDialogOpen(false);
                    setProjectToDelete(null);
                }}
                onConfirm={handleDeleteConfirm}
                project={projectToDelete}
                isDeleting={isDeleting}
                isProjectSharingEnabled={isSharingEnabled}
            />

            <ProjectImportDialog open={showImportDialog} onOpenChange={setShowImportDialog} onImport={handleImport} />

            {projectToShare && <ShareProjectDialog isOpen={isShareDialogOpen} onClose={handleShareDialogClose} project={projectToShare} />}
        </PageLayout>
    );
};
