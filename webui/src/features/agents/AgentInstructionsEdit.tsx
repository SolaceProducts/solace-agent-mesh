// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { zodResolver } from "@hookform/resolvers/zod";
import { BookOpenText } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { getSectionTitle, PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Textarea } from "@/ui/textarea";

import type { Agent } from "@/types";

import { agentInstructionsSchema } from "./agentEditSchemas";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentInstructionsHelp } from "./AgentInstructionsHelp";
import { AgentEditSection, AgentEditWrapper } from "./common";

type AgentInstructionsFormData = z.infer<typeof agentInstructionsSchema>;

export const AgentInstructionsEdit = ({ agent, onApply, onDiscard }: { agent: Agent; onApply: (agent: Agent) => void; onDiscard: () => void }) => {
    const {
        register,
        handleSubmit,
        formState: { errors },
    } = useForm<AgentInstructionsFormData>({
        resolver: zodResolver(agentInstructionsSchema),
        defaultValues: {
            systemPrompt: agent.systemPrompt || "",
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const onSubmit = (data: AgentInstructionsFormData) => {
        onApply({ ...agent, ...data });
    };

    return (
        <AgentEditWrapper sectionTitle={getSectionTitle(BookOpenText, "Instructions")} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard} hints={<AgentInstructionsHelp />}>
            <AgentEditSection fullHeight>
                <div>Give your agent instructions to tell it how to behave and respond to get the results you expect.</div>

                <div className="mt-8 flex min-h-0 flex-1 flex-col">
                    <PageLabel required className="mb-2 text-(--primary-text-wMain)">
                        Instructions
                    </PageLabel>
                    <Textarea data-testid="agent-instructions" {...register("systemPrompt")} className="flex-1 resize-none whitespace-pre-wrap" aria-invalid={!!errors.systemPrompt} />
                    {errors.systemPrompt && <ErrorLabel message={errors.systemPrompt.message} />}
                </div>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
