// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { keepPreviousData, type QueryKey, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { rbacKeys } from "@/api/rbac/keys";
import { usageKeys } from "@/api/usage/keys";

import { budgetKeys } from "./keys";
import type { Budget, CreateBudgetAssignmentRequest, CreateBudgetRequest, GrantBudgetTopUpRequest, ListBudgetsParams, ListBudgetSubjectsParams, UpdateBudgetAssignmentsRequest, UpdateBudgetRequest } from "./types";
import { budgetsApi } from "./versioned";

export function useBudgets(params: ListBudgetsParams, { enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: budgetKeys.list(params),
        queryFn: () => budgetsApi.list(params),
        placeholderData: keepPreviousData,
        enabled,
    });
}

export function useDefaultBudget(subjectType: Budget["subjectType"], { enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: budgetKeys.defaultBudget(subjectType),
        queryFn: () => budgetsApi.findDefault(subjectType),
        gcTime: 0,
        enabled,
    });
}

export function useBudget(id: string, { enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({ queryKey: budgetKeys.detail(id), queryFn: () => budgetsApi.get(id), retry: 0, enabled });
}

export function useBudgetSubjects(params: ListBudgetSubjectsParams, { enabled = true }: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: budgetKeys.subjects(params),
        queryFn: () => budgetsApi.listSubjects(params),
        placeholderData: keepPreviousData,
        enabled,
    });
}

function useInvalidatingMutation<TVars, TResult>(mutationFn: (vars: TVars) => Promise<TResult>, alsoInvalidate: readonly QueryKey[] = []) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn,
        onSettled: () => Promise.all([budgetKeys.all, ...alsoInvalidate].map(queryKey => queryClient.invalidateQueries({ queryKey }))),
    });
}

const USERS_LISTS = [rbacKeys.users.all(), usageKeys.all];

export function useCreateBudgetMutation() {
    return useInvalidatingMutation((req: CreateBudgetRequest) => budgetsApi.create(req), USERS_LISTS);
}

export function useUpdateBudgetMutation() {
    return useInvalidatingMutation(({ id, changes }: { id: string; changes: UpdateBudgetRequest }) => budgetsApi.update(id, changes), USERS_LISTS);
}

export function useDeleteBudgetMutation() {
    return useInvalidatingMutation((id: string) => budgetsApi.delete(id), USERS_LISTS);
}

export function useMakeDefaultBudgetMutation() {
    return useInvalidatingMutation((id: string) => budgetsApi.makeDefault(id), USERS_LISTS);
}

export function useCreateBudgetAssignmentMutation() {
    return useInvalidatingMutation((req: CreateBudgetAssignmentRequest) => budgetsApi.createAssignment(req), USERS_LISTS);
}

export function useMoveBudgetAssignmentMutation() {
    return useInvalidatingMutation(({ id, budgetId }: { id: string; budgetId: string }) => budgetsApi.moveAssignment(id, budgetId), USERS_LISTS);
}

export function useDeleteBudgetAssignmentMutation() {
    return useInvalidatingMutation((id: string) => budgetsApi.deleteAssignment(id), USERS_LISTS);
}

export function useGrantBudgetTopUpMutation() {
    return useInvalidatingMutation((req: GrantBudgetTopUpRequest) => budgetsApi.grantTopUp(req), USERS_LISTS);
}

export function useDeleteBudgetTopUpMutation() {
    return useInvalidatingMutation((subjectId: string) => budgetsApi.cancelTopUp(subjectId), USERS_LISTS);
}

export const BUDGET_ASSIGNMENTS_PAGE_SIZE = 20;

export const BUDGET_OPTIONS_PAGE_SIZE = 100;

const SHOWN_ASSIGNMENT_STATE = "concrete";

export function useBudgetAssignments({ budgetId, search }: { budgetId: string; search?: string }, { enabled = true }: { enabled?: boolean } = {}) {
    const trimmed = search?.trim() || undefined;
    const query = useInfiniteQuery({
        queryKey: budgetKeys.assignments({ budgetId, search: trimmed, pageSize: BUDGET_ASSIGNMENTS_PAGE_SIZE, state: SHOWN_ASSIGNMENT_STATE }),
        queryFn: ({ pageParam }) => budgetsApi.listAssignments({ budgetId, search: trimmed, pageNumber: pageParam, pageSize: BUDGET_ASSIGNMENTS_PAGE_SIZE, state: SHOWN_ASSIGNMENT_STATE }),
        initialPageParam: 1,
        getNextPageParam: last => last.meta.pagination.nextPage ?? undefined,
        enabled,
    });
    return {
        ...query,
        assignments: query.data?.pages.flatMap(page => page.data) ?? [],
        total: query.data?.pages[0]?.meta.pagination.count ?? 0,
    };
}

export function useUpdateBudgetAssignmentsMutation() {
    return useInvalidatingMutation(({ budgetId, changes }: { budgetId: string; changes: UpdateBudgetAssignmentsRequest }) => budgetsApi.updateAssignments(budgetId, changes), USERS_LISTS);
}
