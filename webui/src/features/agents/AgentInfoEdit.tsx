// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { zodResolver } from "@hookform/resolvers/zod";
import { IdCard } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { getSectionTitle, PageLabel } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import type { Agent } from "@/types";

import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentEditSection, AgentEditWrapper, agentInfoSchema } from "./common";

type AgentInfoFormData = z.infer<typeof agentInfoSchema>;

export const AgentInfoEdit = ({ agent, onApply, onDiscard }: { agent: Agent; onApply: (agent: Agent) => void; onDiscard: () => void }) => {
    const {
        register,
        handleSubmit,
        formState: { errors },
    } = useForm<AgentInfoFormData>({
        resolver: zodResolver(agentInfoSchema),
        defaultValues: {
            name: agent.name || "",
            description: agent.description || "",
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const onSubmit = (data: AgentInfoFormData) => {
        onApply({ ...agent, ...data });
    };

    return (
        <AgentEditWrapper sectionTitle={getSectionTitle(IdCard, "Agent Details")} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard}>
            <AgentEditSection>
                <div>Give your agent a name and description to help people and other agents understand what your agent can do.</div>

                <div className="mt-8 space-y-6">
                    <div>
                        <PageLabel required>Name</PageLabel>
                        <Input data-testid="agent-name" {...register("name")} type="text" aria-invalid={!!errors.name} />
                        {errors.name && <ErrorLabel message={errors.name.message} />}
                    </div>

                    <div>
                        <PageLabel required>Description</PageLabel>
                        <Textarea data-testid="agent-description" {...register("description")} className="resize-none" aria-invalid={!!errors.description} rows={4} />
                        {errors.description && <ErrorLabel message={errors.description.message} />}
                    </div>
                </div>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
