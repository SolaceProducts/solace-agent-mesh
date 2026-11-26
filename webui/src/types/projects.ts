// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface Project {
    type?: "project";
    id: string;
    name: string;
    userId: string;
    createdBy?: string;
    description?: string | null;
    systemPrompt?: string | null;
    defaultAgentId?: string | null;
    artifactCount?: number | null;
    createdAt: string;
    updatedAt?: string | null;
    isPinned?: boolean;
}

export interface CreateProjectRequest {
    name: string;
    description?: string;
    systemPrompt?: string;
    files?: FileList | null;
}

export interface CopyProjectRequest {
    name: string;
    description?: string;
}

export interface UpdateProjectData {
    name?: string;
    description?: string;
    systemPrompt?: string;
    defaultAgentId?: string | null;
}

export interface ProjectFormData {
    name: string;
    description: string;
    systemPrompt: string;
    defaultAgentId?: string;
    files?: FileList | null;
    fileDescriptions?: Record<string, string>;
}

export interface UseProjectsReturn {
    projects: Project[];
    isLoading: boolean;
    error: string | null;
    createProject: (data: FormData) => Promise<Project>;
    refetch: () => Promise<void>;
}

export interface ProjectContextValue extends UseProjectsReturn {
    currentProject: Project | null;
    setCurrentProject: (project: Project | null) => void;
    selectedProject: Project | null;
    setSelectedProject: (project: Project | null) => void;
    activeProject: Project | null;
    setActiveProject: (project: Project | null) => void;
    addFilesToProject: (projectId: string, formData: FormData) => Promise<{ sseLocation: string | null }>;
    removeFileFromProject: (projectId: string, filename: string) => Promise<{ sseLocation: string | null }>;
    updateFileMetadata: (projectId: string, filename: string, description: string) => Promise<void>;
    updateProject: (projectId: string, data: UpdateProjectData) => Promise<Project>;
    deleteProject: (projectId: string) => Promise<void>;
    searchQuery: string;
    setSearchQuery: (query: string) => void;
    filteredProjects: Project[];
}

export interface ShareResponse {
    id: string;
    projectId: string;
    userEmail: string;
    accessLevel: string;
    sharedByEmail: string;
    createdAt: string;
    updatedAt: string;
}

export interface ProjectSharesResponse {
    projectId: string;
    ownerEmail: string;
    shares: ShareResponse[];
}

export interface BatchShareRequest {
    shares: {
        userEmail: string;
        accessLevel: "resource_viewer";
    }[];
}

export interface BatchShareResponse {
    projectId: string;
    created: ShareResponse[];
    updated: ShareResponse[];
    totalProcessed: number;
}

export interface BatchDeleteRequest {
    userEmails: string[];
}

export interface BatchDeleteResponse {
    projectId: string;
    deletedCount: number;
    deletedEmails: string[];
}

export type AccessLevel = "resource_viewer";
