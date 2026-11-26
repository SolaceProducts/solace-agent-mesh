// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type FormEvent, useMemo, useRef } from "react";
import { BookOpen, Code2, Lightbulb, Pencil, Sparkles } from "lucide-react";

import { useChatContext, useConfigContext, useUIModeContext } from "@/providers/contexts";

import { MarkdownHTMLConverter } from "@/common/markdown";
import { report } from "@/common/product-analytics/report";
import type { ProductAnalyticsSuggestionLabel } from "@/common/product-analytics/types";

import { resolveLogo } from "@/utils/resolveLogo";

import { Button } from "@/ui/button";
import { CHAT_STYLES } from "@/ui/chat/chatStyles";

import { LogoImage, SolaceIcon } from "@/common";
import type { AgentCardInfo, AgentWelcomeConfig, WelcomeSuggestion } from "@/types";

import { ChatInputArea } from "./ChatInputArea";
import { resolveWelcomeMessage } from "./welcomeMessage";

interface SuggestionChip {
    label: string;
    icon: React.ReactNode;
    prompt: string;
    autoSend?: boolean;
    analyticsLabel: ProductAnalyticsSuggestionLabel;
}

const DEFAULT_SUGGESTIONS: SuggestionChip[] = [
    {
        label: "Write",
        icon: <Pencil className="h-4 w-4" />,
        prompt: "Help me write\u00A0",
        analyticsLabel: "write",
    },
    {
        label: "Learn",
        icon: <BookOpen className="h-4 w-4" />,
        prompt: "Explain to me\u00A0",
        analyticsLabel: "learn",
    },
    {
        label: "Code",
        icon: <Code2 className="h-4 w-4" />,
        prompt: "Help me build\u00A0",
        analyticsLabel: "code",
    },
    {
        label: "Brainstorm",
        icon: <Lightbulb className="h-4 w-4" />,
        prompt: "Help me brainstorm ideas for\u00A0",
        analyticsLabel: "brainstorm",
    },
    {
        label: "Surprise me",
        icon: <Sparkles className="h-4 w-4" />,
        prompt: "Surprise me with something interesting and useful",
        autoSend: true,
        analyticsLabel: "surprise_me",
    },
];

const AGENT_SUGGESTION_ICON = <Sparkles className="h-4 w-4" />;

const toChips = (suggestions: WelcomeSuggestion[]): SuggestionChip[] =>
    suggestions.map(s => ({
        label: s.label,
        icon: AGENT_SUGGESTION_ICON,
        prompt: s.prompt,
        autoSend: s.auto_send,
        analyticsLabel: "agent_custom",
    }));

const DefaultWelcomeLogo: React.FC = () => {
    const { configLogoUrl, configSmallLogoUrl } = useConfigContext();

    const logo = resolveLogo(configLogoUrl, configSmallLogoUrl);

    const fallback = <SolaceIcon variant="short" className="h-12 w-12 opacity-80" />;
    if (logo.kind === "default") return fallback;
    return <LogoImage key={logo.url} url={logo.url} className="h-12 w-auto object-contain opacity-90" fallback={fallback} />;
};

interface ChatWelcomeScreenProps {
    agents: AgentCardInfo[];
    selectedAgentName?: string;
    welcomeOverride?: AgentWelcomeConfig;
    compact?: boolean;
    autoFocusComposer?: boolean;
}

export const ChatWelcomeScreen: React.FC<ChatWelcomeScreenProps> = ({ agents, selectedAgentName, welcomeOverride, autoFocusComposer = false }) => {
    const { handleSubmit } = useChatContext();
    const { configDisclaimerText, configWelcomeMessage } = useConfigContext();
    const { mode } = useUIModeContext();

    const welcomeAgent = useMemo(() => agents.find(a => a.name === selectedAgentName || a.displayName === selectedAgentName), [agents, selectedAgentName]);

    const effectiveWelcome = welcomeAgent?.welcome ?? welcomeOverride;
    const welcomeMessage = resolveWelcomeMessage(effectiveWelcome?.welcome_message, configWelcomeMessage, mode);

    const suggestionsSignature = useMemo(() => (effectiveWelcome?.suggestions ? JSON.stringify(effectiveWelcome.suggestions) : null), [effectiveWelcome?.suggestions]);

    const stickyRef = useRef<{ agent?: string; signature: string | null }>({ agent: selectedAgentName, signature: null });
    if (stickyRef.current.agent !== selectedAgentName) {
        stickyRef.current = { agent: selectedAgentName, signature: null };
    }
    if (suggestionsSignature !== null) {
        stickyRef.current.signature = suggestionsSignature;
    }
    const resolvedSignature = suggestionsSignature ?? stickyRef.current.signature;

    const suggestions = useMemo(() => (resolvedSignature ? toChips(JSON.parse(resolvedSignature) as WelcomeSuggestion[]) : DEFAULT_SUGGESTIONS), [resolvedSignature]);

    const handleChipClick = async (chip: SuggestionChip) => {
        report("chat:suggestion_used", { suggestionLabel: chip.analyticsLabel });
        if (chip.autoSend) {
            const fakeEvent = new Event("submit") as unknown as FormEvent;
            await handleSubmit(fakeEvent, [], chip.prompt);
        } else {
            window.dispatchEvent(
                new CustomEvent("set-chat-input", {
                    detail: { text: chip.prompt },
                })
            );
        }
    };

    return (
        <div className="flex h-full w-full flex-col items-center overflow-y-auto px-4 pb-32">
            <div className="my-auto flex w-full flex-col items-center gap-6" style={{ maxWidth: "900px" }}>
                {effectiveWelcome?.welcome_icon || welcomeOverride?.welcome_icon ? (
                    <img
                        src={effectiveWelcome?.welcome_icon || welcomeOverride?.welcome_icon}
                        alt=""
                        className={`w-auto opacity-90 ${{ small: "h-12", medium: "h-24", large: "h-40" }[effectiveWelcome?.welcome_icon_size || welcomeOverride?.welcome_icon_size || "small"]}`}
                    />
                ) : (
                    <DefaultWelcomeLogo />
                )}
                <h1 className="text-center text-3xl font-semibold tracking-tight">{welcomeMessage}</h1>
                <div className="mt-2 flex flex-wrap items-center justify-center gap-2">
                    {suggestions.map(chip => (
                        <Button variant="outline" key={chip.label} testid="welcomeSuggestion" onClick={() => handleChipClick(chip)} className="rounded-full">
                            {chip.icon}
                            {chip.label}
                        </Button>
                    ))}
                </div>
                <div className="mt-4 w-full" style={CHAT_STYLES}>
                    <ChatInputArea agents={agents} autoFocus={autoFocusComposer} />
                </div>
                {configDisclaimerText && (
                    <div className="w-full px-16 text-center">
                        <MarkdownHTMLConverter className="inline-block text-left text-xs">{configDisclaimerText}</MarkdownHTMLConverter>
                    </div>
                )}
            </div>
        </div>
    );
};
