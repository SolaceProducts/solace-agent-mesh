// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type BuildApplyKind = "connector" | "a2a_agent" | "agent" | "workflow" | "gateway";

export interface BuildComponentInput {
    kind: BuildApplyKind;
    name: string;
    content: string;
    connectors?: string[];
    sourceFilename?: string;
    resourceId?: string;
}

export interface ApplyBuildRequest {
    dryRun?: boolean;
    buildName?: string;
    builderSessionId?: string;
    components: BuildComponentInput[];
}

export type BuildOperationAction = "create" | "update" | "unchanged";

export type BuildOperationStatus = "planned" | "succeeded" | "failed" | "skipped";

export interface BuildOperationError {
    message: string;
    errorId: string;
    httpStatus: number;
    validationDetails?: Record<string, string[]>;
}

export interface BuildOperationResult {
    kind: BuildApplyKind;
    name: string;
    resolvedName?: string;
    renamedFrom?: string;
    sourceFilename?: string;
    action: BuildOperationAction;
    status: BuildOperationStatus;
    resourceId?: string;
    changedFields?: string[];
    warnings?: string[];
    changesDeclined?: boolean;
    error?: BuildOperationError;
}

export interface BuildApplySummary {
    created: number;
    updated: number;
    unchanged: number;
    failed: number;
    skipped: number;
}

export type DroppedDeployment = "deployed" | "deploy_failed" | "undeploy_failed" | "not_deployed";

export interface DroppedResource {
    kind: "agent" | "workflow";
    id: string;
    name: string;
    createdBySession: boolean;
    deployment: DroppedDeployment;
}

export interface ApplyBuildResponse {
    dryRun: boolean;
    operations: BuildOperationResult[];
    summary: BuildApplySummary;
    dropped: DroppedResource[];
    droppedCheckFailed?: boolean;
}

export interface RemovalTarget {
    kind: DroppedResource["kind"];
    id: string;
}

export interface RemoveDroppedRequest {
    builderSessionId: string;
    resources: RemovalTarget[];
}

export type RemovalStatus = "removed" | "not_found" | "refused" | "failed";

export interface RemovalResult {
    kind: RemovalTarget["kind"];
    id: string;
    name?: string;
    status: RemovalStatus;
    error?: BuildOperationError;
}

export interface RemoveDroppedResponse {
    results: RemovalResult[];
}
