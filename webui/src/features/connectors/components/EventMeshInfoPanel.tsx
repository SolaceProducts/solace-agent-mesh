// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink } from "lucide-react";

import { DOC_LINKS } from "@/utils/docs";

import { LearningBanner } from "@/common";
import { Button } from "@/ui";

export function EventMeshInfoPanel() {
    return (
        <aside className="hidden h-full w-[380px] shrink-0 border-l bg-(--background-w10) p-4 xl:block 2xl:w-[420px]">
            <LearningBanner title="Define the messages an AI agent can publish" storageKey="event-mesh-tool-config-guide-dismissed" showIcon={false}>
                <p>Connector tools define message topics, payloads, and delivery options for messages that AI agents can publish to the connected event broker.</p>
                <p className="mt-3">One connector can hold many tools, each tool targeting a different topic address.</p>
                <Button type="button" variant="link" className="mt-3 h-auto !p-0 text-sm font-semibold" onClick={() => window.open(DOC_LINKS.eventMeshConnector, "_blank", "noopener,noreferrer")}>
                    Learn more about connector tools
                    <ExternalLink className="ml-1 size-3" />
                </Button>

                <div className="mt-8">
                    <p className="mb-2 font-semibold">How it connects to the LLM</p>
                    <p>When the agent runs, the LLM decides which tool to use based on the tool names and descriptions.</p>
                    <p className="mt-3">Well-named tools with clear descriptions give the LLM the context to act accurately without extra prompting.</p>
                </div>
            </LearningBanner>
        </aside>
    );
}
