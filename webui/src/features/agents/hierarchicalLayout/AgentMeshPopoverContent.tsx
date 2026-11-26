// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Wrench } from "lucide-react";

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/ui/accordion";
import { type AgentCardInfoFE } from "@/types/agent";

export function AgentMeshPopoverContent({
    agent,
}: Readonly<{
    agent: AgentCardInfoFE | null;
}>) {
    if (!agent) {
        return (
            <div className="p-4">
                <div className={`text-center text-(--secondary-text-wMain)`}>No agent information available.</div>
            </div>
        );
    }

    return (
        <div className="p-4">
            <div className="mb-3 flex items-center justify-between">
                <h3 className={`text-lg font-semibold`}>{agent.displayName ?? "Agent Information"}</h3>
            </div>
            <div className={`flex flex-col gap-1 space-y-2 text-(--secondary-text-wMain)`}>
                <div>{agent.description ?? "No description available."}</div>
                <div>
                    Name: <span>{agent.name}</span>
                </div>
                <div>
                    Version: <span>{agent.version}</span>
                </div>
                {agent.skills && agent.skills.length > 0 && (
                    <div>
                        <div className={`font-semibold`}>Abilities:</div>
                        <div key={`skills-${agent.name}`} className={`max-h-40 overflow-x-hidden overflow-y-auto`}>
                            <ul className="list-disc pl-5">
                                {agent.skills.map(skill => (
                                    <li key={`${agent.name}-${skill.name}`} className="text-sm">
                                        {skill.name}: {skill.description}
                                    </li>
                                ))}
                            </ul>
                        </div>
                    </div>
                )}
                {agent.tools && agent.tools.length > 0 && (
                    <div>
                        <Accordion type="single" collapsible className="w-full">
                            <AccordionItem value="tools-section">
                                <AccordionTrigger className={`font-semibold`}>Tools:</AccordionTrigger>
                                <AccordionContent>
                                    <div key={`tools-${agent.name}`} className={`max-h-48 overflow-x-hidden overflow-y-auto`}>
                                        <Accordion type="single" collapsible className="w-full">
                                            {agent.tools.map(tool => (
                                                <AccordionItem value={tool.name} key={tool.name}>
                                                    <AccordionTrigger className="text-sm">
                                                        <div className="flex items-center gap-2">
                                                            <Wrench size={14} className="text-(--brand-w100)" />
                                                            <span>{tool.name}</span>
                                                        </div>
                                                    </AccordionTrigger>
                                                    <AccordionContent className="pl-8 text-sm">{tool.description ?? "No description available."}</AccordionContent>
                                                </AccordionItem>
                                            ))}
                                        </Accordion>
                                    </div>
                                </AccordionContent>
                            </AccordionItem>
                        </Accordion>
                    </div>
                )}
            </div>
        </div>
    );
}
