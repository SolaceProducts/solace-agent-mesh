// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";

import { useChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { type ArtifactInfo } from "@/types/chat";

import type { ConfigType } from "../builderUtils";
import { detectConfigType, isBuilderConfigMime } from "../builderUtils";

export interface BuilderConfigState {
    configYaml: string | null;
    configType: ConfigType;
    artifactInfo: ArtifactInfo | null;
    isLoading: boolean;
}

export function useBuilderConfig(): BuilderConfigState {
    const { downloadAndResolveArtifact, sessionId } = useChatContext();
    const { data: artifacts } = useChatArtifacts();
    const [configYaml, setConfigYaml] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const fetchingRef = useRef(false);
    const lastVersionRef = useRef<number | undefined>(undefined);

    const lastSessionRef = useRef(sessionId);
    const sessionJustChangedRef = useRef(false);
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        setConfigYaml(null);
        setIsLoading(false);
        fetchingRef.current = false;
        lastVersionRef.current = undefined;
        sessionJustChangedRef.current = true;
    }

    const samConfigArtifact = useMemo(() => {
        if (!artifacts || artifacts.length === 0) return null;

        for (let i = artifacts.length - 1; i >= 0; i--) {
            const mime = artifacts[i].mimeType;
            if (isBuilderConfigMime(mime)) {
                return artifacts[i];
            }
        }
        return null;
    }, [artifacts]);

    useEffect(() => {
        if (sessionJustChangedRef.current) {
            sessionJustChangedRef.current = false;
            return;
        }

        if (!samConfigArtifact) {
            setConfigYaml(null);
            setIsLoading(false);
            return;
        }

        const versionChanged = samConfigArtifact.version !== undefined && lastVersionRef.current !== undefined && samConfigArtifact.version !== lastVersionRef.current;

        if (versionChanged) {
            lastVersionRef.current = samConfigArtifact.version;
            setConfigYaml(null);
            fetchingRef.current = false;
        } else {
            lastVersionRef.current = samConfigArtifact.version;
        }

        if (samConfigArtifact.accumulatedContent) {
            setConfigYaml(samConfigArtifact.accumulatedContent);
            setIsLoading(false);
            fetchingRef.current = false;
            return;
        }

        if (configYaml && !fetchingRef.current) {
            setIsLoading(false);
            return;
        }

        if (fetchingRef.current) return;
        fetchingRef.current = true;
        setIsLoading(true);

        const filename = samConfigArtifact.filename;
        downloadAndResolveArtifact(filename)
            .then((fileAttachment: { content?: string } | null) => {
                if (fileAttachment?.content) {
                    try {
                        const binary = atob(fileAttachment.content);
                        const bytes = Uint8Array.from(binary, c => c.charCodeAt(0));
                        const decoded = new TextDecoder().decode(bytes);
                        setConfigYaml(decoded);
                    } catch {
                        setConfigYaml(fileAttachment.content);
                    }
                }
            })
            .catch((err: unknown) => {
                console.error("Failed to fetch builder config artifact:", err);
            })
            .finally(() => {
                setIsLoading(false);
                fetchingRef.current = false;
            });
    }, [samConfigArtifact, downloadAndResolveArtifact, configYaml]);

    const configType = useMemo(() => {
        if (!configYaml) return "unknown" as ConfigType;
        return detectConfigType(configYaml);
    }, [configYaml]);

    return useMemo(
        () => ({
            configYaml,
            configType,
            artifactInfo: samConfigArtifact,
            isLoading,
        }),
        [configYaml, configType, samConfigArtifact, isLoading]
    );
}
