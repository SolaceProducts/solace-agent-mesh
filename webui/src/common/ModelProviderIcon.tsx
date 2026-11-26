// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { SlidersHorizontal } from "lucide-react";

import azureOpenaiIcon from "@/assets/images/azure_openai.svg";
import bedrockIcon from "@/assets/images/bedrock.svg";
import claudeIcon from "@/assets/images/claude.svg";
import googleAiStudioIcon from "@/assets/images/google_ai_studio.svg";
import ollamaIcon from "@/assets/images/ollama.svg";
import openaiIcon from "@/assets/images/openai.svg";
import vertexAiIcon from "@/assets/images/vertexai.svg";
import solaceIcon from "@/assets/solace-logo-s.svg";

import { cn } from "@/utils";

interface ModelProviderIconProps {
    provider: string | null | undefined;
    size?: "xs" | "sm" | "md";
}

const sizeConfig = {
    xs: {
        container: "h-5 w-5",
        image: "h-4 w-4",
        text: "text-xs",
    },
    sm: {
        container: "h-8 w-8",
        image: "h-7 w-7",
        text: "text-xs",
    },
    md: {
        container: "h-12 w-12",
        image: "h-10 w-10",
        text: "text-sm",
    },
};

const providerIconMap: Record<string, string> = {
    anthropic: claudeIcon,
    openai: openaiIcon,
    google_ai_studio: googleAiStudioIcon,
    vertex_ai: vertexAiIcon,
    azure_openai: azureOpenaiIcon,
    bedrock: bedrockIcon,
    ollama: ollamaIcon,
    solace: solaceIcon,
};

export const ModelProviderIcon = ({ provider, size = "md" }: ModelProviderIconProps) => {
    const [imageError, setImageError] = useState(false);
    const config = sizeConfig[size];
    const providerKey = provider ?? "";
    const normalizedProvider = providerKey.toLowerCase();
    const iconPath = providerIconMap[normalizedProvider];

    useEffect(() => {
        setImageError(false);
    }, [provider]);

    if (normalizedProvider === "custom") {
        return (
            <div className={cn("flex items-center justify-center", config.container)}>
                <SlidersHorizontal className={cn("text-(--primary-text-wMain)", config.image)} />
            </div>
        );
    }

    if (!iconPath || imageError) {
        return (
            <div className={cn("flex items-center justify-center rounded-full bg-(--secondary-w10)", config.container)}>
                <span className={cn("font-semibold text-(--primary-text-wMain)", config.text)}>{providerKey.charAt(0).toUpperCase()}</span>
            </div>
        );
    }

    return (
        <div className={cn("flex items-center justify-center rounded-sm bg-white/90", config.container)}>
            <img src={iconPath} alt={providerKey} className={cn("object-contain", config.image)} onError={() => setImageError(true)} />
        </div>
    );
};
