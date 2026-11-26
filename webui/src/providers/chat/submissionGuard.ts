// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface SubmissionState {
    hasContent: boolean;
    isResponding: boolean;
    isCancelling: boolean;
    selectedAgentName: string;
}

export function isSubmissionDropped({ hasContent, isResponding, isCancelling, selectedAgentName }: SubmissionState): boolean {
    return !hasContent || isResponding || isCancelling || !selectedAgentName;
}
