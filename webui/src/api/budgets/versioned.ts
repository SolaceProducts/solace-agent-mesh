// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "../common";

import type {
    Budget,
    BudgetAssignment,
    BudgetSubject,
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
import {
    cancelBudgetTopUpV2,
    createBudgetAssignmentV2,
    createBudgetV2,
    deleteBudgetAssignmentV2,
    deleteBudgetV2,
    findDefaultBudgetV2,
    getBudgetV2,
    grantBudgetTopUpV2,
    listBudgetAssignmentsV2,
    listBudgetSubjectsV2,
    listBudgetsV2,
    makeDefaultBudgetV2,
    moveBudgetAssignmentV2,
    updateBudgetAssignmentsV2,
    updateBudgetV2,
} from "./v2";

export interface BudgetsApi {
    list: (params: ListBudgetsParams) => Promise<PaginatedResponse<Budget>>;
    findDefault: (subjectType: Budget["subjectType"]) => Promise<Budget | null>;
    get: (id: string) => Promise<Budget>;
    create: (req: CreateBudgetRequest) => Promise<Budget>;
    update: (id: string, req: UpdateBudgetRequest) => Promise<Budget>;
    delete: (id: string) => Promise<void>;
    makeDefault: (id: string) => Promise<Budget>;
    listSubjects: (params: ListBudgetSubjectsParams) => Promise<PaginatedResponse<BudgetSubject>>;
    createAssignment: (req: CreateBudgetAssignmentRequest) => Promise<BudgetAssignment>;
    moveAssignment: (id: string, budgetId: string) => Promise<BudgetAssignment>;
    deleteAssignment: (id: string) => Promise<void>;
    listAssignments: (params: ListBudgetAssignmentsParams) => Promise<PaginatedResponse<BudgetAssignment>>;
    updateAssignments: (budgetId: string, req: UpdateBudgetAssignmentsRequest) => Promise<UpdateBudgetAssignmentsResponse>;
    grantTopUp: (req: GrantBudgetTopUpRequest) => Promise<void>;
    cancelTopUp: (subjectId: string) => Promise<void>;
}

export const budgetsApi: BudgetsApi = {
    list: listBudgetsV2,
    findDefault: findDefaultBudgetV2,
    get: getBudgetV2,
    create: createBudgetV2,
    update: updateBudgetV2,
    delete: deleteBudgetV2,
    makeDefault: makeDefaultBudgetV2,
    listSubjects: listBudgetSubjectsV2,
    createAssignment: createBudgetAssignmentV2,
    moveAssignment: moveBudgetAssignmentV2,
    deleteAssignment: deleteBudgetAssignmentV2,
    listAssignments: listBudgetAssignmentsV2,
    updateAssignments: updateBudgetAssignmentsV2,
    grantTopUp: grantBudgetTopUpV2,
    cancelTopUp: cancelBudgetTopUpV2,
};
