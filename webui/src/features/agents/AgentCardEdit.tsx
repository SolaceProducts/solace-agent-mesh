// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Bot, Plus, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";
import { getSectionTitle, PageLabel } from "@/common/layout";
import { MultiSelect } from "@/common/MultiSelect";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { Textarea } from "@/ui/textarea";

import type { Agent, AgentSkillFE } from "@/types";

import { AgentAbilityCard } from "./AgentAbilityCard";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentEditSection, AgentEditWrapper, AgentEmptySection } from "./common";

const createSkillSchema = (existingSkills: AgentSkillFE[]) =>
    z.object({
        name: z
            .string()
            .transform(val => val.trim())
            .pipe(
                z
                    .string()
                    .min(3, "Name must be at least 3 characters")
                    .max(40, "Name must be 40 characters or less")
                    .refine(val => SAFE_NAME_PATTERN.test(val), {
                        message: SAFE_NAME_DESCRIPTION,
                    })
                    .refine(val => !existingSkills.some(skill => skill.name.toLowerCase() === val.toLowerCase()), {
                        message: "An ability with this name (case insensitive) already exists",
                    })
            ),
        description: z
            .string()
            .transform(val => val.trim())
            .pipe(
                z
                    .string()
                    .min(10, "Value must be at least 10 characters")
                    .max(250, "Value must be 250 characters or less")
                    .refine(val => SAFE_TEXT_PATTERN.test(val), {
                        message: SAFE_TEXT_DESCRIPTION,
                    })
            ),
    });

type SkillFormData = z.infer<ReturnType<typeof createSkillSchema>>;

const MAX_SKILLS = 10;

const INPUT_MODE_OPTIONS = ["file", "text"];
const OUTPUT_MODE_OPTIONS = ["file", "text"];

export const AgentCardEdit = ({ agent, onApply, onDiscard }: { agent: Agent; onApply: (agent: Agent) => void; onDiscard: () => void }) => {
    const [skills, setSkills] = useState<AgentSkillFE[]>(agent?.skills || []);
    const [isAddingSkill, setIsAddingSkill] = useState(false);
    const [inputModes, setInputModes] = useState<string[]>(agent?.inputModes || []);
    const [outputModes, setOutputModes] = useState<string[]>(agent?.outputModes || []);

    const {
        register,
        handleSubmit: handleFormSubmit,
        formState: { errors },
        reset,
    } = useForm<SkillFormData>({
        resolver: zodResolver(createSkillSchema(skills)),
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const handleSaveSkill = (data: SkillFormData) => {
        const newSkill: AgentSkillFE = {
            name: data.name,
            description: data.description,
        };

        setSkills([...skills, newSkill]);
        reset();
        setIsAddingSkill(false);
    };

    const handleCancelAddSkill = () => {
        reset();
        setIsAddingSkill(false);
    };

    const handleRemoveSkill = (indexToRemove: number) => {
        setSkills(skills.filter((_, index) => index !== indexToRemove));
    };

    const handleSubmit = () => {
        onApply({
            ...agent,
            skills,
            inputModes: inputModes,
            outputModes: outputModes,
        });
    };

    return (
        <AgentEditWrapper sectionTitle={getSectionTitle(Bot, "Agent Card")} agent={agent} onSubmit={handleSubmit} onDiscard={onDiscard}>
            <AgentEditSection>
                <div>The agent card describes its expertise and supported data formats, helping other agents find it.</div>

                <div className="my-8">
                    <div className="mb-2 flex items-center justify-between">
                        <div>
                            <div className="mb-1 text-base font-semibold">Abilities</div>
                            <div className="mb-4">In your own words, give your agent up to ten abilities that clearly describe what it can do.</div>
                        </div>
                        {!isAddingSkill && skills.length < MAX_SKILLS && (
                            <Button variant="ghost" title="Add a new ability" onClick={() => setIsAddingSkill(true)} size="sm">
                                <Plus className="size-4" />
                                Add Ability
                            </Button>
                        )}
                    </div>

                    {isAddingSkill && (
                        <div className="pb-6">
                            <form onSubmit={handleFormSubmit(handleSaveSkill, trackAgentFormErrors)}>
                                <div>
                                    <PageLabel className="text-(--primary-text-wMain)" required>
                                        Name
                                    </PageLabel>
                                    <Input id="skill-name" data-testid="agent-skillName" type="text" {...register("name")} aria-invalid={!!errors.name} />
                                    {errors.name && <ErrorLabel message={errors.name.message} />}
                                </div>
                                <div className="mt-3">
                                    <PageLabel className="text-(--primary-text-wMain)" required>
                                        Description
                                    </PageLabel>
                                    <Textarea id="skill-description" data-testid="agent-skillDescription" {...register("description")} className="resize-none" aria-invalid={!!errors.description} rows={2} />
                                    {errors.description && <ErrorLabel message={errors.description.message} />}
                                </div>
                                <div className="mt-3 flex justify-end gap-2">
                                    <Button variant="ghost" onClick={handleCancelAddSkill} type="button">
                                        Cancel
                                    </Button>
                                    <Button variant="default" type="submit">
                                        Add
                                    </Button>
                                </div>
                            </form>
                        </div>
                    )}

                    {skills.length > 0 && (
                        <div className="mb-4 space-y-2">
                            {skills
                                .sort((a, b) => a.name.localeCompare(b.name))
                                .map((skill, index) => (
                                    <div key={skill.name + index} className="group relative">
                                        <AgentAbilityCard
                                            ability={skill}
                                            className="pr-10"
                                            actionButton={
                                                isAddingSkill ? null : (
                                                    <Button variant="ghost" onClick={() => handleRemoveSkill(index)} tooltip="Remove ability">
                                                        <X className="size-4" />
                                                    </Button>
                                                )
                                            }
                                        />
                                    </div>
                                ))}
                        </div>
                    )}
                    {skills.length === 0 && !isAddingSkill && <AgentEmptySection message="No abilities" />}
                </div>

                <div>
                    <div className="mb-1 text-base font-semibold">Communication Modes</div>
                    <div className="mb-4">Select the types of information your agent can work with.</div>
                    <div className="space-y-4">
                        <div>
                            <div className="mb-2">Input Modes</div>
                            <MultiSelect options={INPUT_MODE_OPTIONS} value={inputModes} onChange={setInputModes} placeholder="Select input modes..." />
                        </div>
                        <div>
                            <div className="mb-2">Output Modes</div>
                            <MultiSelect options={OUTPUT_MODE_OPTIONS} value={outputModes} onChange={setOutputModes} placeholder="Select output modes..." />
                        </div>
                    </div>
                </div>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
