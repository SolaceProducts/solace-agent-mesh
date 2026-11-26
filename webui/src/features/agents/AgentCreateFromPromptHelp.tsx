// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, Sparkles } from "lucide-react";

import { messageColourVariants } from "@/common";

const BOT = <Bot className="flex-shrink-0 text-(--brand-wMain)" />;
const SPARKLES = <Sparkles className="flex-shrink-0 text-(--brand-wMain)" />;
const ITEM_CLASS = "mb-3 flex flex-row gap-2";
const ITEM_TEXT_CLASS = "flex flex-col gap-1";

export const AgentCreateFromPromptHelp = () => {
    return (
        <>
            <div className={`relative w-full p-6 sm:w-80 ${messageColourVariants.info} text-(--secondary-text-wMain)`}>
                <div>
                    <h3 className="mb-4 text-sm font-semibold">Best Practices</h3>

                    <div>
                        <div className={ITEM_CLASS}>
                            {BOT}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">What problem does your agent solve?</div>
                                <div>Who will use it and what outcome do they expect?</div>
                            </div>
                        </div>
                        <div className={ITEM_CLASS}>
                            {SPARKLES}
                            <div className={ITEM_TEXT_CLASS}>
                                <div className="font-semibold">Describe specific use cases someone would ask this agent for help with.</div>
                                <div>Examples: analyze sales trends, troubleshoot network issues, draft quarterly reports.</div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
            <div className="h-6 w-full bg-(--background-w10)"></div>
        </>
    );
};
