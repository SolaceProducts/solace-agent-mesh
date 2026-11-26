// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { connectorKeys, useCanCompareConnectorSecrets } from "@/api/connectors";
import { type ConnectorsApi, connectorsApi } from "@/api/connectors/versioned";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { type Connector, REDACTED_SENTINEL } from "@/types";

import { type CredentialCandidate, detectComponentsWithMissingCredentials, SAM_REQUIRED_PLACEHOLDER } from "../builderUtils";

type DetectArgs = Parameters<typeof detectComponentsWithMissingCredentials>;

export interface ConnectorCredentialCheck {
    componentName: string;
    platformName: string;
    type: string;
    subtype: string;
    placeholderFields: string[];
}

export function connectorsToCheck(missing: ReadonlyMap<string, number>, componentData: ReadonlyMap<string, CredentialCandidate>): ConnectorCredentialCheck[] {
    const checks: ConnectorCredentialCheck[] = [];
    for (const componentName of missing.keys()) {
        const metadata = componentData.get(componentName)?.connectorMetadata;
        if (!metadata?.name || !metadata.values) continue;
        const placeholderFields = Object.entries(metadata.values)
            .filter(([, value]) => value === SAM_REQUIRED_PLACEHOLDER)
            .map(([field]) => field);
        if (placeholderFields.length > 0) checks.push({ componentName, platformName: metadata.name, type: metadata.type ?? "", subtype: metadata.subtype ?? "", placeholderFields });
    }
    return checks;
}

const platformNameKey = (name: string) => name.toLowerCase().replaceAll(" ", "");

const SAM_PLACEHOLDER_PATTERN = /^<<__SAM_.*__>>$/;

function holdsRealStoredValue(value: unknown): boolean {
    if (value === undefined || value === null || value === "" || value === REDACTED_SENTINEL) return false;
    return typeof value !== "string" || !SAM_PLACEHOLDER_PATTERN.test(value);
}

export async function countPlaceholdersStillOnPlatform(check: ConnectorCredentialCheck, connector: Connector, api: Pick<ConnectorsApi, "diffSecrets"> = connectorsApi, keepsStoredPlainValues = false): Promise<number> {
    const stored: Record<string, unknown> = connector.values ?? {};
    const secretFields = check.placeholderFields.filter(field => stored[field] === REDACTED_SENTINEL);
    const filledPlain = keepsStoredPlainValues ? check.placeholderFields.filter(field => holdsRealStoredValue(stored[field])).length : 0;
    if (secretFields.length === 0) return check.placeholderFields.length - filledPlain;

    const candidate = Object.fromEntries(secretFields.map(field => [field, SAM_REQUIRED_PLACEHOLDER]));
    const { secrets } = await api.diffSecrets(connector.id, candidate);
    const filledSecrets = secretFields.filter(field => secrets[field]?.matches === false).length;
    return check.placeholderFields.length - filledPlain - filledSecrets;
}

async function resolveRemainingPlaceholdersOnPlatform(checks: ConnectorCredentialCheck[], api: ConnectorsApi, keepsStoredPlainValues: boolean): Promise<Map<string, number>> {
    const connectors = await api.getAll().catch(error => {
        console.warn("[useComponentsMissingCredentials] could not list connectors; treating credentials as unfilled", error);
        throw error;
    });
    const byName = new Map(connectors.map(connector => [platformNameKey(connector.name), connector]));
    const remaining = new Map<string, number>();
    await Promise.all(
        checks.map(async check => {
            const connector = byName.get(platformNameKey(check.platformName));
            if (!connector || connector.type !== check.type || (connector.subtype ?? "") !== check.subtype) return;
            try {
                remaining.set(check.componentName, await countPlaceholdersStillOnPlatform(check, connector, api, keepsStoredPlainValues));
            } catch (error) {
                console.warn("[useComponentsMissingCredentials] could not compare saved credentials; treating them as unfilled", check.componentName, error);
            }
        })
    );
    return remaining;
}

export function applyPlatformCredentialState(fromArtifacts: ReadonlyMap<string, number>, remainingOnPlatform: ReadonlyMap<string, number> | undefined): ReadonlyMap<string, number> {
    if (!remainingOnPlatform) return fromArtifacts;
    const result = new Map(fromArtifacts);
    for (const [componentName, remaining] of remainingOnPlatform) {
        if (remaining > 0) result.set(componentName, remaining);
        else result.delete(componentName);
    }
    return result;
}

export function useComponentsMissingCredentials(manifest: DetectArgs[0], componentData: DetectArgs[1]): ReadonlyMap<string, number> {
    const fromArtifacts = useMemo(() => detectComponentsWithMissingCredentials(manifest, componentData), [manifest, componentData]);
    const checks = useMemo(() => (componentData ? connectorsToCheck(fromArtifacts, componentData) : []), [fromArtifacts, componentData]);
    const canCompareSecrets = useCanCompareConnectorSecrets();
    const keepsStoredPlainValues = useIsBuilderServerApplyEnabled();

    const awaitingPlatformAnswer = canCompareSecrets && checks.length > 0;
    const { data, isError, refetch } = useQuery({
        queryKey: connectorKeys.builderCredentials(JSON.stringify({ checks, keepsStoredPlainValues })),
        queryFn: () => resolveRemainingPlaceholdersOnPlatform(checks, connectorsApi, keepsStoredPlainValues),
        enabled: awaitingPlatformAnswer,
        retry: 0,
        staleTime: 0,
        gcTime: 0,
        refetchOnMount: "always",
    });
    const remainingOnPlatform = awaitingPlatformAnswer && !isError ? data : undefined;

    useEffect(() => {
        const refetchOnWindowFocus = () => {
            if (awaitingPlatformAnswer) refetch({ cancelRefetch: false });
        };
        window.addEventListener("focus", refetchOnWindowFocus);
        return () => window.removeEventListener("focus", refetchOnWindowFocus);
    }, [awaitingPlatformAnswer, refetch]);

    return useMemo(() => applyPlatformCredentialState(fromArtifacts, remainingOnPlatform), [fromArtifacts, remainingOnPlatform]);
}
