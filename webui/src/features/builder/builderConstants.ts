// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type AgentWelcomeConfig } from "@/types/agent";
import builderWelcomeIcon from "@/assets/images/sam-ai-builder.png";

export const BUILDER_WELCOME: AgentWelcomeConfig = {
    welcome_message: "What would you like to build today?",
    welcome_icon: builderWelcomeIcon,
    welcome_icon_size: "large",
    suggestions: [
        { label: "Build an event-driven agent", prompt: "I want to create an agent that\u00A0", auto_send: false },
        { label: "Create a multi-step workflow", prompt: "I want to create a workflow that\u00A0", auto_send: false },
        { label: "Connect to an external system", prompt: "I need to connect to\u00A0", auto_send: false },
        { label: "Automate a business process", prompt: "I want to automate a process where\u00A0", auto_send: false },
    ],
};
