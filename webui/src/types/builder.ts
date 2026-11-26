// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type BuilderComponentState = "queued" | "active" | "completed" | "failed" | "skipped" | "input-required";

export interface BuilderComponentProgress {
    id: string;
    type: "gateway" | "workflow" | "agent" | "skill" | "connector" | "a2a_agent";
    name: string;
    state: BuilderComponentState;
    statusText?: string;
    lastUpdated: number;
    progressPercent?: number;
    parentId?: string;
}

export interface BuilderCreationState {
    isBuilding: boolean;
    components: BuilderComponentProgress[];
    manifestFilename?: string;
    overallStatus: "idle" | "planning" | "building" | "completed" | "failed";
}

export interface BuilderComponentProgressEvent {
    type: "builder_component_progress";
    component_id: string;
    component_type: "gateway" | "workflow" | "agent" | "skill" | "connector" | "a2a_agent";
    component_name: string;
    state: BuilderComponentState;
    status_text?: string;
    progress_percent?: number;
    parent_id?: string;
}

export const INITIAL_BUILDER_CREATION_STATE: BuilderCreationState = {
    isBuilding: false,
    components: [],
    overallStatus: "idle",
};
