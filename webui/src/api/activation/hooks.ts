// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { modelKeys } from "@/api/models/keys";

import { activationKeys } from "./keys";
import * as activationService from "./service";
import type { AcceptActivationRequest, Activation, ActivationFreeModel } from "./types";
import { applyUsedUpMark } from "./usedUpMark";

const SIGN_IN_POLL_MS = 2_000;
const FREE_CREDITS_POLL_MS = 5 * 60_000;

export function useActivation(enabled: boolean) {
    return useQuery({
        queryKey: activationKeys.status(),
        queryFn: activationService.getActivation,
        enabled,
        staleTime: Infinity,
        retry: 2,
        refetchInterval: query => (query.state.data?.signingIn ? SIGN_IN_POLL_MS : false),
    });
}

function useStoreActivation() {
    const queryClient = useQueryClient();
    return async (activation: Activation) => {
        await queryClient.cancelQueries({ queryKey: activationKeys.status() });
        queryClient.setQueryData(activationKeys.status(), activation);
    };
}

export function useSignIn() {
    const queryClient = useQueryClient();
    const store = useStoreActivation();
    return useMutation({
        mutationFn: () => activationService.signIn(),
        onSuccess: store,
        onError: () => queryClient.invalidateQueries({ queryKey: activationKeys.status() }),
    });
}

export function useCancelSignIn() {
    const store = useStoreActivation();
    return useMutation({ mutationFn: () => activationService.cancelSignIn(), onSuccess: store });
}

export class FreeModelSetupError extends Error {
    constructor(cause: unknown) {
        super("Solace Agent Mesh couldn't set up the free model. Try again.", { cause });
        this.name = "FreeModelSetupError";
    }
}

async function acceptAndSetUp(request: AcceptActivationRequest): Promise<Activation> {
    const activation = await activationService.accept(request);
    if (request.freeModel && activation.freeModel) {
        try {
            await activationService.setUpFreeModels(activation.freeModel, true);
        } catch (error_) {
            throw new FreeModelSetupError(error_);
        }
    }
    return activation;
}

export function useAcceptActivation() {
    const queryClient = useQueryClient();
    const store = useStoreActivation();
    return useMutation({
        mutationFn: acceptAndSetUp,
        onSuccess: async (activation, request) => {
            if (request.freeModel) await queryClient.invalidateQueries({ queryKey: modelKeys.all });
            await store(activation);
        },
        onError: error => {
            if (error instanceof FreeModelSetupError) return;
            return queryClient.invalidateQueries({ queryKey: activationKeys.status() });
        },
        onSettled: () => queryClient.invalidateQueries({ queryKey: activationKeys.terms() }),
    });
}

export function useActivationTerms(enabled: boolean) {
    return useQuery({
        queryKey: activationKeys.terms(),
        queryFn: activationService.getTerms,
        enabled,
        staleTime: 0,
        gcTime: 0,
        retry: false,
        refetchOnWindowFocus: false,
    });
}

export function useAgreeToFreeModelTerms() {
    const queryClient = useQueryClient();
    const store = useStoreActivation();
    return useMutation({
        mutationFn: (openedDocumentIds: string[]) => activationService.accept({ freeModel: true, openedDocumentIds }),
        onSuccess: store,
        onSettled: () => queryClient.invalidateQueries({ queryKey: activationKeys.terms() }),
    });
}

export function useFreeModelStatus(enabled: boolean, { readOnOpen = false }: { readOnOpen?: boolean } = {}) {
    return useQuery({
        queryKey: activationKeys.freeModel(),
        queryFn: async () => applyUsedUpMark(await activationService.getFreeModel()),
        enabled,
        refetchOnMount: readOnOpen ? "always" : true,
        refetchOnWindowFocus: true,
        refetchInterval: FREE_CREDITS_POLL_MS,
        retry: 1,
    });
}

export function useFreeModelSignIn() {
    const queryClient = useQueryClient();
    const store = useStoreActivation();
    return useMutation({
        mutationFn: () => activationService.signInFreeModel(),
        onSuccess: store,
        onError: () => queryClient.invalidateQueries({ queryKey: activationKeys.status() }),
    });
}

export function useRefreshFreeModel() {
    const queryClient = useQueryClient();
    return useMutation({ mutationFn: () => activationService.refreshFreeModel(), onSuccess: status => queryClient.setQueryData(activationKeys.freeModel(), applyUsedUpMark(status)) });
}

export function useModelsConfigured() {
    return useQuery({ queryKey: activationKeys.modelsConfigured(), queryFn: activationService.getModelsConfigured, staleTime: Infinity, retry: 1 });
}

export function useEnsureFreeModels(activation: Activation | undefined) {
    const queryClient = useQueryClient();
    const { mutate } = useMutation({
        mutationFn: (freeModel: ActivationFreeModel) => activationService.setUpFreeModels(freeModel, false),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelKeys.all }),
        retry: 2,
    });
    const ensured = useRef(false);
    const freeModel = activation?.state === "active" && activation.freeModelChosen ? activation.freeModel : undefined;
    useEffect(() => {
        if (!freeModel || ensured.current) return;
        ensured.current = true;
        mutate(freeModel);
    }, [freeModel, mutate]);
}
