// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ApiV2Error, fetchAllPages, instantToIso, type PaginatedResponse, platformV2, withoutNulls } from "../common";

import type {
    Budget,
    BudgetAssignment,
    BudgetAssignmentIdentity,
    BudgetAssignmentUser,
    BudgetSubject,
    BudgetSubjectUserIdentity,
    CreateBudgetAssignmentRequest,
    CreateBudgetRequest,
    GrantBudgetTopUpRequest,
    ListBudgetAssignmentsParams,
    ListBudgetsParams,
    ListBudgetSubjectsParams,
    UpdateBudgetAssignmentsRequest,
    UpdateBudgetAssignmentsResponse,
    UpdateBudgetRequest,
} from "./types";

interface AuditTimesV2 {
    createdTime: string | null;
    updatedTime: string | null;
}

interface BudgetV2 extends Omit<Budget, "createdTime" | "updatedTime">, AuditTimesV2 {}

interface BudgetSubjectV2 extends Omit<BudgetSubject, "user"> {
    user: BudgetSubjectUserIdentity | null;
}

interface BudgetAssignmentUserV2 extends BudgetSubjectUserIdentity {
    emailShared: boolean | null;
    sub: string | null;
    issuer: string | null;
}

interface BudgetAssignmentV2 extends Omit<BudgetAssignment, "subject" | "createdTime" | "updatedTime">, AuditTimesV2 {
    subject: {
        type: "user";
        id: string | null;
        displayName: string | null;
        user: BudgetAssignmentUserV2 | null;
        identity: (Omit<BudgetAssignmentIdentity, "issuer"> & { issuer: string | null }) | null;
    };
}

const BUDGETS = "/budgets";
const BUDGET_SUBJECTS = "/budgetSubjects";
const BUDGET_ASSIGNMENTS = "/budgetAssignments";

const budgetPath = (id: string) => `${BUDGETS}/${encodeURIComponent(id)}`;
const budgetAssignmentPath = (id: string) => `${BUDGET_ASSIGNMENTS}/${encodeURIComponent(id)}`;
const topUpPath = (subjectId: string) => `${BUDGET_SUBJECTS}/${encodeURIComponent(subjectId)}/topUp`;

function toBudget({ createdTime, updatedTime, ...rest }: BudgetV2): Budget {
    return { ...rest, createdTime: instantToIso(createdTime), updatedTime: instantToIso(updatedTime) };
}

function toBudgetSubject({ user, ...rest }: BudgetSubjectV2): BudgetSubject {
    return { ...rest, user: user ?? { email: null, isSystemUser: false } };
}

function toBudgetAssignment({ subject, createdTime, updatedTime, ...rest }: BudgetAssignmentV2): BudgetAssignment {
    const { user, identity, ...subjectRest } = subject;
    return {
        ...rest,
        subject: {
            ...subjectRest,
            ...(user && { user: withoutNulls(user) as BudgetAssignmentUser }),
            ...(identity && { identity: withoutNulls(identity) }),
        },
        createdTime: instantToIso(createdTime),
        updatedTime: instantToIso(updatedTime),
    };
}

const toPage = <W, T>(page: PaginatedResponse<W>, map: (wire: W) => T): PaginatedResponse<T> => ({ ...page, data: page.data.map(map) });

export async function listBudgetsV2(params: ListBudgetsParams): Promise<PaginatedResponse<Budget>> {
    const query = new URLSearchParams({ pageNumber: String(params.pageNumber), pageSize: String(params.pageSize) });
    const search = params.name?.trim();
    if (params.subjectType) query.set("subjectType", params.subjectType);
    if (search) query.set("search", search);
    if (params.sortBy) query.set("sort", `${params.sortBy}:${params.sortDir ?? "asc"}`);
    return toPage(await platformV2.list<BudgetV2>(`${BUDGETS}?${query.toString()}`), toBudget);
}

export async function findDefaultBudgetV2(subjectType: Budget["subjectType"]): Promise<Budget | null> {
    const budgets = await fetchAllPages(page => listBudgetsV2({ ...page, subjectType }));
    return budgets.find(budget => budget.isDefault) ?? null;
}

export async function getBudgetV2(id: string): Promise<Budget> {
    return toBudget(await platformV2.get<BudgetV2>(budgetPath(id)));
}

const NAME_TAKEN_MESSAGE = "A budget with this name already exists.";

export class DuplicateBudgetNameError extends ApiV2Error {
    constructor(error: ApiV2Error) {
        super(error.status, error.message, { errorId: error.errorId, validationDetails: error.validationDetails, location: error.location });
        this.name = "DuplicateBudgetNameError";
    }
}

function asDuplicateName(error: unknown): unknown {
    return error instanceof ApiV2Error && error.status === 409 && error.message === NAME_TAKEN_MESSAGE ? new DuplicateBudgetNameError(error) : error;
}

export async function createBudgetV2(req: CreateBudgetRequest): Promise<Budget> {
    try {
        return toBudget(await platformV2.post<BudgetV2>(BUDGETS, req));
    } catch (error) {
        throw asDuplicateName(error);
    }
}

export async function updateBudgetV2(id: string, req: UpdateBudgetRequest): Promise<Budget> {
    try {
        return toBudget(await platformV2.patch<BudgetV2>(budgetPath(id), req));
    } catch (error) {
        throw asDuplicateName(error);
    }
}

export async function deleteBudgetV2(id: string): Promise<void> {
    await platformV2.delete(budgetPath(id));
}

export async function makeDefaultBudgetV2(id: string): Promise<Budget> {
    return toBudget(await platformV2.put<BudgetV2>(`${budgetPath(id)}/makeDefault`));
}

export async function listBudgetSubjectsV2(params: ListBudgetSubjectsParams): Promise<PaginatedResponse<BudgetSubject>> {
    const query = new URLSearchParams({ subjectType: params.subjectType, pageNumber: String(params.pageNumber), pageSize: String(params.pageSize) });
    const search = params.search?.trim();
    if (search) query.set("search", search);
    if (params.sortDir) query.set("sort", `displayName:${params.sortDir}`);
    return toPage(await platformV2.list<BudgetSubjectV2>(`${BUDGET_SUBJECTS}?${query.toString()}`), toBudgetSubject);
}

export async function createBudgetAssignmentV2(req: CreateBudgetAssignmentRequest): Promise<BudgetAssignment> {
    return toBudgetAssignment(await platformV2.post<BudgetAssignmentV2>(BUDGET_ASSIGNMENTS, req));
}

export async function moveBudgetAssignmentV2(id: string, budgetId: string): Promise<BudgetAssignment> {
    return toBudgetAssignment(await platformV2.patch<BudgetAssignmentV2>(budgetAssignmentPath(id), { budgetId }));
}

export async function deleteBudgetAssignmentV2(id: string): Promise<void> {
    await platformV2.delete(budgetAssignmentPath(id));
}

export async function listBudgetAssignmentsV2(params: ListBudgetAssignmentsParams): Promise<PaginatedResponse<BudgetAssignment>> {
    const query = new URLSearchParams({ budgetId: params.budgetId, pageNumber: String(params.pageNumber), pageSize: String(params.pageSize) });
    const search = params.search?.trim();
    if (search) query.set("search", search);
    if (params.state) query.set("state", params.state);
    return toPage(await platformV2.list<BudgetAssignmentV2>(`${BUDGET_ASSIGNMENTS}?${query.toString()}`), toBudgetAssignment);
}

export async function updateBudgetAssignmentsV2(budgetId: string, req: UpdateBudgetAssignmentsRequest): Promise<UpdateBudgetAssignmentsResponse> {
    return platformV2.put<UpdateBudgetAssignmentsResponse>(`${budgetPath(budgetId)}/updateAssignments`, req);
}

export async function grantBudgetTopUpV2({ subjectId, amount }: GrantBudgetTopUpRequest): Promise<void> {
    await platformV2.put(topUpPath(subjectId), { amount });
}

export async function cancelBudgetTopUpV2(subjectId: string): Promise<void> {
    await platformV2.put(topUpPath(subjectId), { amount: "0" });
}
