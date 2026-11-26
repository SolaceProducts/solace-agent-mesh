// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import yaml from "js-yaml";

import { useChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

export interface BuildManifestComponent {
    filename?: string;
    type?: string;
    name?: string;
    status?: string;
    origin?: string;
    gateway_type?: string;
    connector_type?: string;
    connector_subtype?: string;
    depends_on?: string[];
}

export interface BuildManifest {
    name?: string;
    display_name?: string;
    description?: string;
    components?: BuildManifestComponent[];
}

export function trimToCompleteYaml(raw: string, isComplete: boolean): string {
    if (isComplete) return raw;
    const lastNewline = raw.lastIndexOf("\n");
    if (lastNewline <= 0) return "";
    return raw.slice(0, lastNewline + 1);
}

export function parseManifest(raw: string, isComplete: boolean): BuildManifest | null {
    const trimmed = trimToCompleteYaml(raw, isComplete);
    if (!trimmed) return null;
    try {
        const doc = yaml.load(trimmed);
        if (!doc || typeof doc !== "object") return null;
        return doc as BuildManifest;
    } catch {
        return null;
    }
}

interface UseBuildPlanArtifactArgs {
    filename: string;
    isComplete: boolean;
}

interface UseBuildPlanArtifactResult {
    manifest: BuildManifest | null;
    rawYaml: string;
    isLoading: boolean;
    setManifestOptimistic: (manifest: BuildManifest | null) => void;
    clearOptimisticManifest: () => void;
}

export function useBuildPlanArtifact({ filename, isComplete }: UseBuildPlanArtifactArgs): UseBuildPlanArtifactResult {
    const { downloadAndResolveArtifact, displayError } = useChatContext();
    const { data: allArtifacts } = useChatArtifacts();

    const [fetchedContent, setFetchedContent] = useState<string | null>(null);
    const [optimisticManifest, setOptimisticManifest] = useState<BuildManifest | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const isFetchingRef = useRef(false);

    const artifact = useMemo(() => allArtifacts.find(a => a.filename === filename), [allArtifacts, filename]);

    const rawYaml = fetchedContent ?? artifact?.accumulatedContent ?? "";

    useEffect(() => {
        if (!isComplete || fetchedContent || isFetchingRef.current) return;
        isFetchingRef.current = true;
        setIsLoading(true);

        downloadAndResolveArtifact(filename)
            .then(fileData => {
                if (fileData?.content) {
                    try {
                        const binary = atob(fileData.content);
                        const bytes = Uint8Array.from(binary, c => c.charCodeAt(0));
                        setFetchedContent(new TextDecoder().decode(bytes));
                    } catch {
                        setFetchedContent(fileData.content);
                    }
                }
            })
            .catch(err => {
                displayError({ title: "Failed to load build plan", error: err instanceof Error ? err.message : `Could not fetch ${filename}.`, errorSource: "api" });
            })
            .finally(() => {
                isFetchingRef.current = false;
                setIsLoading(false);
            });
    }, [isComplete, fetchedContent, filename, downloadAndResolveArtifact, displayError]);

    const parsedManifest = useMemo(() => parseManifest(rawYaml, isComplete), [rawYaml, isComplete]);

    const manifest = optimisticManifest ?? parsedManifest;

    const setManifestOptimistic = useCallback((next: BuildManifest | null) => {
        setOptimisticManifest(next);
    }, []);

    const clearOptimisticManifest = useCallback(() => {
        setOptimisticManifest(null);
    }, []);

    return {
        manifest,
        rawYaml,
        isLoading,
        setManifestOptimistic,
        clearOptimisticManifest,
    };
}
