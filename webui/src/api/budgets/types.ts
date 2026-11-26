// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface Budget {
    id: string;
    type: "budget";
    name: string;
    subjectType: "mesh" | "user";
    amount: string | null;
    warningPercentage: number | null;
    isDefault: boolean;
    assignedCount: number;
    hiddenAssignedCount: number;
    pendingCount: number;
    createdBy: string;
    createdTime: string;
    updatedBy: string;
    updatedTime: string;
}

export interface CreateBudgetRequest {
    name: string;
    subjectType: "user" | "mesh";
    amount: string | null;
    warningPercentage?: number | null;
}

export interface UpdateBudgetRequest {
    name?: string;
    amount?: string | null;
    warningPercentage?: number | null;
}

export interface ListBudgetsParams {
    pageNumber: number;
    pageSize: number;
    name?: string;
    subjectType?: Budget["subjectType"];
    sortBy?: BudgetSortKey;
    sortDir?: "asc" | "desc";
}

export type BudgetSortKey = "name" | "amount";

export interface AppliedBudget {
    id: string;
    name: string;
    amount: string | null;
    source: "assigned" | "default";
    assignmentId: string | null;
}

export interface BudgetSubjectUserIdentity {
    email: string | null;
    isSystemUser: boolean;
}

export interface AppliedTopUp {
    amount: string;
    expiresTime: string;
}

export interface BudgetSubject {
    id: string;
    type: "budgetSubject";
    subjectType: "user";
    displayName: string | null;
    user: BudgetSubjectUserIdentity;
    budget: AppliedBudget | null;
    topUp: AppliedTopUp | null;
    effectiveAmount: string | null;
    spend?: string;
}

export interface ListBudgetSubjectsParams {
    subjectType: BudgetSubject["subjectType"];
    pageNumber: number;
    pageSize: number;
    search?: string;
    sortDir?: "asc" | "desc";
}

export interface BudgetAssignmentIdentity {
    type: "email" | "sub" | "system";
    value: string;
    issuer?: string;
}

export interface BudgetAssignmentUser extends BudgetSubjectUserIdentity {
    emailShared?: boolean;
    sub?: string;
    issuer?: string;
}

export interface BudgetAssignment {
    id: string;
    type: "budgetAssignment";
    state: "concrete" | "pending";
    subject: {
        type: "user";
        id: string | null;
        displayName: string | null;
        user?: BudgetAssignmentUser;
        identity?: BudgetAssignmentIdentity;
    };
    budget: { id: string; name: string };
    createdBy: string;
    createdTime: string;
    updatedBy: string;
    updatedTime: string;
}

export type CreateBudgetAssignmentRequest = { subjectType: "user"; budgetId: string } & ({ subjectId: string; identity?: never } | { identity: BudgetAssignmentIdentity; subjectId?: never });

export interface ListBudgetAssignmentsParams {
    budgetId: string;
    pageNumber: number;
    pageSize: number;
    search?: string;
    state?: BudgetAssignment["state"];
}

export interface UpdateBudgetAssignmentsRequest {
    subjectType: "user";
    add: string[];
    remove: string[];
}

interface ChangedSubject {
    assignmentId: string;
    subject: { id: string };
}

export interface UpdateBudgetAssignmentsResponse {
    added: ChangedSubject[];
    moved: (ChangedSubject & { fromBudget: { id: string; name: string } })[];
    removed: ChangedSubject[];
    unchanged: { assignmentId: string | null; subject: { id: string }; reason: "already_assigned" | "not_assigned" }[];
}

export interface GrantBudgetTopUpRequest {
    subjectType: "user";
    subjectId: string;
    amount: string;
}

export interface BudgetTopUp {
    id: string;
    type: "budgetTopUp";
    subject: { type: "user" | "mesh"; id: string; displayName: string | null; user?: BudgetSubjectUserIdentity };
    amount: string;
    expiresTime: string;
    createdBy: string;
    createdTime: string;
    updatedBy: string;
    updatedTime: string;
}
