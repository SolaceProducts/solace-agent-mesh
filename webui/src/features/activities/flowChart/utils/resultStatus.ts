// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const LEGACY_PYTHON_FAILURE_STATUS = "failure";

export function isFailedResultStatus(status: string | undefined): boolean {
    return status === "error" || status === "failed" || status === "timed_out" || status === LEGACY_PYTHON_FAILURE_STATUS;
}

const DELEGATION_PLACEHOLDER_KEYS = new Set(["sub_task_id", "peer_name", "workflow_name", "pending_kind"]);

const REDACTED_VALUE = "<REDACTED>";

export function isPlaceholderToolResult(resultData: unknown): boolean {
    if (!isRecord(resultData)) return false;
    const authMarker = resultData._pending_auth_marker;
    if (authMarker === true || authMarker === REDACTED_VALUE) return true;

    const statesPending = resultData.status === "pending";
    if (isNonEmptyString(resultData.hil_request_id)) {
        return statesPending || resultData.pending_kind === "hil_approval";
    }
    if (isNonEmptyString(resultData.sub_task_id)) {
        return statesPending || Object.keys(resultData).every(key => DELEGATION_PLACEHOLDER_KEYS.has(key));
    }
    return false;
}

export function toolResultStatus(resultData: unknown): string | undefined {
    return isRecord(resultData) && typeof resultData.status === "string" ? resultData.status : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
    return typeof value === "string" && value !== "";
}
