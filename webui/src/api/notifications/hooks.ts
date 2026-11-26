// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";

import type { ApiError } from "@/api/client";

import { notificationKeys } from "./keys";
import * as notificationService from "./service";
import type { NotificationMatrix, NotificationMatrixCellUpdate } from "./types";

export function useNotificationInbox(pageNumber = 1, pageSize = 50, unreadOnly = false, alwaysRefetchOnFocus = false, category = "", enabled = true) {
    return useQuery({
        queryKey: notificationKeys.inbox({ pageNumber, pageSize, unreadOnly, ...(category && { category }) }),
        queryFn: () => notificationService.fetchInbox(pageNumber, pageSize, unreadOnly, category),
        enabled,
        refetchOnMount: "always",
        ...(alwaysRefetchOnFocus && { refetchOnWindowFocus: "always" as const }),
    });
}

export function useMarkNotificationRead() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => notificationService.markRead(id),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useMarkAllNotificationsRead() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (category?: string) => notificationService.markAllRead(category),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useMarkNotificationUnread() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => notificationService.markUnread(id),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useDeleteNotification() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => notificationService.deleteNotification(id),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useClearAllNotifications() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => notificationService.clearAllNotifications(),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useRespondToPrompt() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ id, status, data }: { id: string; status: "answered" | "cancelled"; data: Record<string, unknown> }) => notificationService.respondToPrompt(id, { status, data }),
        onSettled: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.all });
        },
    });
}

export function useNotificationPreferences() {
    return useQuery({
        queryKey: notificationKeys.preferences(),
        queryFn: () => notificationService.fetchPreferences(),
    });
}

export function useSetNotificationPreference() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ channelType, destinationKey = "", enabled }: { channelType: string; destinationKey?: string; enabled: boolean }) => notificationService.setPreference(channelType, destinationKey, enabled),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.preferences() });
            queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        },
    });
}

export function useNotificationCategoryPreferences() {
    return useQuery({
        queryKey: notificationKeys.categoryPreferences(),
        queryFn: () => notificationService.fetchCategoryPreferences(),
    });
}

export function useSetNotificationCategoryPreference() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ category, enabled }: { category: string; enabled: boolean }) => notificationService.setCategoryPreference(category, enabled),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.categoryPreferences() });
            queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        },
    });
}

export function useNotificationChannels() {
    return useQuery({
        queryKey: notificationKeys.channels(),
        queryFn: () => notificationService.fetchChannels(),
    });
}

export function useTurnOnChannel() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (channelType: string) => notificationService.turnOnChannel(channelType),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        },
        onSettled: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.channels() });
        },
    });
}

export function useTurnOffChannel() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (channelType: string) => notificationService.turnOffChannel(channelType),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        },
        onSettled: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.channels() });
        },
    });
}

export function useVerifyChannelCode() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ channelType, code }: { channelType: string; code: string }) => notificationService.verifyChannelCode(channelType, code),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
        },
        onSettled: () => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.channels() });
        },
    });
}

const MAX_MATRIX_LOAD_ATTEMPTS = 3;
const SERVICE_UNAVAILABLE = 503;
const REQUEST_TIMEOUT = 408;
const TOO_MANY_REQUESTS = 429;
const TRANSIENT_CLIENT_ERRORS = new Set([REQUEST_TIMEOUT, TOO_MANY_REQUESTS]);

function isPermanentLoadFailure(error: ApiError): boolean {
    const status = error.status;
    if (status === undefined || TRANSIENT_CLIENT_ERRORS.has(status)) {
        return false;
    }
    return status === SERVICE_UNAVAILABLE || (status >= 400 && status < 500);
}

const MATRIX_SAVE_KEY = [...notificationKeys.matrix(), "save"] as const;
const MATRIX_SAVE_SCOPE = { id: "notification-matrix-save" };
const MATRIX_SAVE_TIMEOUT_MS = 15_000;

function withCells(matrix: NotificationMatrix, updates: NotificationMatrixCellUpdate[]): NotificationMatrix {
    return {
        ...matrix,
        cells: matrix.cells.map(cell => {
            const update = updates.find(u => u.category === cell.category && u.channelType === cell.channelType && u.destinationKey === cell.destinationKey);
            return update && !cell.locked ? { ...cell, enabled: update.enabled } : cell;
        }),
    };
}

export function useNotificationMatrix() {
    const query = useQuery({
        queryKey: notificationKeys.matrix(),
        queryFn: () => notificationService.fetchNotificationMatrix(),
        retry: (failureCount, error: ApiError) => failureCount < MAX_MATRIX_LOAD_ATTEMPTS && !isPermanentLoadFailure(error),
    });
    const inFlight = useMutationState({
        filters: { mutationKey: MATRIX_SAVE_KEY, status: "pending" },
        select: mutation => mutation.state.variables as NotificationMatrixCellUpdate[],
    });
    return { ...query, data: query.data && inFlight.reduce(withCells, query.data) };
}

interface MatrixSaveOptions {
    onSaveError?: (error: Error) => void;
}

export function useUpdateNotificationMatrix({ onSaveError }: MatrixSaveOptions = {}) {
    const queryClient = useQueryClient();
    const isLastSave = () => queryClient.isMutating({ mutationKey: MATRIX_SAVE_KEY }) <= 1;
    return useMutation<NotificationMatrix, Error, NotificationMatrixCellUpdate[]>({
        mutationKey: MATRIX_SAVE_KEY,
        scope: MATRIX_SAVE_SCOPE,
        networkMode: "always",
        mutationFn: cells => notificationService.updateNotificationMatrix(cells, AbortSignal.timeout(MATRIX_SAVE_TIMEOUT_MS)),
        onError: error => onSaveError?.(error),
        onSuccess: async matrix => {
            await queryClient.cancelQueries({ queryKey: notificationKeys.matrix() });
            queryClient.setQueryData(notificationKeys.matrix(), matrix);
            queryClient.invalidateQueries({ queryKey: notificationKeys.categoryPreferences() });
            queryClient.invalidateQueries({ queryKey: notificationKeys.preferences() });
        },
        onSettled: () => {
            if (isLastSave()) {
                queryClient.invalidateQueries({ queryKey: notificationKeys.matrix() });
            }
        },
    });
}
