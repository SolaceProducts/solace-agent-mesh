// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AlertCircle, CircleCheckBig, Dot } from "lucide-react";

import { messageColourVariants } from "@/common";

const CHECK = <CircleCheckBig className="flex-shrink-0 text-(--success-w100)" />;
const WARN = <AlertCircle className="flex-shrink-0 text-(--warning-w100)" />;
const DOT = <Dot className="flex-shrink-0 self-start" size={21} />;
const ITEM_CLASS = "mb-3 flex flex-row gap-2";
const ITEM_TEXT_CLASS = "flex flex-col gap-1";

export const AgentInstructionsHelp = () => {
    return (
        <>
            <div className={`relative mt-[90px] w-140 p-6 ${messageColourVariants.info} text-(--secondary-text-wMain)`}>
                <div className="pr-8">
                    <h3 className="mb-4 text-sm font-semibold">Best Practices</h3>
                    <div className="mb-4 text-xs text-(--secondary-text-wMain)">
                        Write clear, specific instructions that tell your agent exactly what to do. Providing good instructions helps your agent respond with better accuracy and consistency.
                    </div>

                    <div className="text-xs text-(--secondary-text-wMain)">
                        <div className={ITEM_CLASS}>
                            {CHECK}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Define its identity and area of expertise.</div>
                                <div>Clearly state the role your agent is playing and the specific knowledge it has so it understands how it should respond.</div>
                                <div>Example: "You are a customer support agent specializing in software troubleshooting for mobile applications."</div>
                            </div>
                        </div>
                        <div className={ITEM_CLASS}>
                            {CHECK}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Set its communication style and response format.</div>
                                <div>
                                    Tell the agent what tone to use (for example, friendly, formal, humorous, business-like, etc.) and how to structure its answers. If applicable, list the format (for example, text, markdown, or JSON) it should use
                                    in its responses.
                                </div>
                                <div>Example: "Use a friendly, professional tone. Start each response with a brief acknowledgment, provide a step-by-step solution, and end by offering further assistance."</div>
                            </div>
                        </div>
                        <div className={ITEM_CLASS}>
                            {CHECK}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Provide relevant examples of good responses, including the tools you expect it to use.</div>
                                <div>Give 1 to 3 concrete examples that show how the agent should respond to common requests and which tools to use. This helps the agent understand what requests to expect and how to handle them.</div>
                                <div>Example: "When a customer reports an issue with service latency:"</div>
                                <div className="flex items-center">{DOT}Respond with: "Let me check for any known issues and help you resolve this."</div>
                                <div className="flex items-center">{DOT}(Use web access tool) to look up the current service status and any known issues.</div>
                                <div className="flex items-center">{DOT}Provide a personalized solution based on findings.</div>
                                <div className="flex items-center">{DOT}Ask: "Does this resolve the issue, or would you like me to escalate this to our technical team?"</div>
                            </div>
                        </div>
                        <div className={ITEM_CLASS}>
                            {CHECK}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Identify any constraints or limitations.</div>
                                <div>Tell the agent what it can't do or access so users know what to expect.</div>
                                <div>Example: "You can only access customer data from the past 12 months. For older records, direct users to contact the billing department."</div>
                            </div>
                        </div>
                        <div className={ITEM_CLASS}>
                            {WARN}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Avoid:</div>
                                <div className="flex">{DOT}Unnecessary context: Don't include long backstories or detailed company history unless directly relevant. Avoid too many scenario examples.</div>
                                <div className="flex">{DOT}Unrealistic claims: Don't use statements like "You can solve any problem" or "You have access to all company information."</div>
                                <div className="flex">
                                    {DOT}Ambiguous language: Use specific, actionable terms instead of vague descriptions. For example, instead of "When users seem frustrated, be helpful", write "When customers use words like 'frustrated', 'angry',
                                    or 'disappointed', acknowledge their feelings and offer to escalate."
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
            <div className="h-6 w-full bg-(--background-w10)"></div>
        </>
    );
};
