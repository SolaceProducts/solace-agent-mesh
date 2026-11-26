// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ProjectContext } from "@/providers/contexts";

import type { Project, ProjectContextValue } from "@/types/projects";

interface MockProjectProviderProps {
    children: React.ReactNode;
    mockValues: Partial<ProjectContextValue>;
}

const defaultProjectValues: ProjectContextValue = {
    isLoading: false,
    projects: [],
    error: null,
    createProject: async () => ({}) as Project,
    refetch: async () => {},
    currentProject: null,
    setCurrentProject: () => {},
    selectedProject: null,
    setSelectedProject: () => {},
    activeProject: null,
    setActiveProject: () => {},
    addFilesToProject: async () => ({ sseLocation: null }),
    updateProject: async () => ({}) as Project,
    removeFileFromProject: async () => ({ sseLocation: null }),
    updateFileMetadata: async () => {},
    deleteProject: async () => {},
    searchQuery: "",
    setSearchQuery: () => {},
    filteredProjects: [],
};

export const MockProjectProvider: React.FC<MockProjectProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue = {
        ...defaultProjectValues,
        ...mockValues,
    };

    return <ProjectContext.Provider value={contextValue}>{children}</ProjectContext.Provider>;
};
