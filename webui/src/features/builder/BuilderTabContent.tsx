// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";

import { useFilteredChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { BuilderPanel } from "./BuilderPanel";
import { extractConfigName, isBuilderConfigMime } from "./builderUtils";
import { fetchArtifactDirect, resolveArtifactContent } from "./hooks/artifactUtils";
import { useBuilderConfig } from "./hooks/useBuilderConfig";
import { useBuilderManifest } from "./hooks/useBuilderManifest";

interface BuilderTabContentProps {
    userAgentsRefetch?: () => Promise<void>;
}

export function BuilderTabContent({ userAgentsRefetch }: BuilderTabContentProps) {
    const config = useBuilderConfig();
    const manifest = useBuilderManifest();
    const { downloadAndResolveArtifact, sessionId } = useChatContext();
    const { artifacts } = useFilteredChatArtifacts();

    const configArtifacts = useMemo(() => artifacts?.filter((a: { mimeType: string }) => isBuilderConfigMime(a.mimeType)) ?? [], [artifacts]);

    const fetchConfigByFilename = useCallback(
        async (filename: string): Promise<string | null> => {
            const artifact = configArtifacts.find((a: { filename: string }) => a.filename === filename);
            if (!artifact) {
                return sessionId ? fetchArtifactDirect(sessionId, filename) : null;
            }
            return resolveArtifactContent(artifact, downloadAndResolveArtifact, sessionId);
        },
        [configArtifacts, downloadAndResolveArtifact, sessionId]
    );

    const findConfigByAgentName = useCallback(
        async (agentName: string): Promise<string | null> => {
            const component = manifest.manifest?.components?.find(c => c.name === agentName || c.name.toLowerCase() === agentName.toLowerCase());
            if (component) {
                const yaml = await fetchConfigByFilename(component.filename);
                if (yaml) return yaml;
            }

            for (const artifact of configArtifacts) {
                const yaml = await resolveArtifactContent(artifact, downloadAndResolveArtifact, sessionId);
                if (yaml) {
                    const name = extractConfigName(yaml);
                    if (name === agentName || name.toLowerCase() === agentName.toLowerCase()) {
                        return yaml;
                    }
                }
            }

            return null;
        },
        [manifest.manifest, configArtifacts, fetchConfigByFilename, downloadAndResolveArtifact, sessionId]
    );

    return <BuilderPanel config={config} manifest={manifest} findConfigByAgentName={findConfigByAgentName} userAgentsRefetch={userAgentsRefetch} />;
}
