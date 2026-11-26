// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { addAllCard, addCard, ALL_ID, type ComponentOption, setCardGrant } from "./bundles";

export const DEMO_AGENTS: ComponentOption[] = [
    { id: "agent_019eb6a2-b940-781a-8def-91234b461f80", name: "HR Assistant", description: "Answers HR policy questions and routes requests." },
    { id: "agent_019ec111-2222-7333-8444-555566667777", name: "Sales Copilot", description: "Drafts proposals and summarizes CRM activity." },
    { id: "agent_019ed888-9999-7aaa-8bbb-ccccddddeeee", name: "Support Triage", description: "Classifies and prioritizes inbound support tickets." },
];

export const DEMO_WORKFLOWS: ComponentOption[] = [
    { id: "workflow_019ef101-1111-7222-8333-444455556666", name: "Onboarding", description: "New-hire setup across systems." },
    { id: "workflow_019ef202-aaaa-7bbb-8ccc-ddddeeeeffff", name: "Incident Review", description: "Post-incident analysis and report generation." },
];

export const makeAgents = (n: number): ComponentOption[] =>
    Array.from({ length: n }, (_, i) => {
        const id = `agent_${String(i).padStart(4, "0")}`;
        return { id, name: `Agent ${i + 1}`, description: `Synthetic demo agent number ${i + 1}.` };
    });

const LONG_TEXT = "Customer Lifecycle Orchestration and Retention Intelligence Assistant for Enterprise Accounts";
const LONG_DESCRIPTION =
    "Handles an unusually long, wrapping description so we can verify that names and descriptions flow onto multiple lines without overflowing their container, truncating awkwardly, or pushing the remove control off-screen — across both the picker dropdown and the selected rows.";

export const LONG_AGENTS: ComponentOption[] = [
    { id: "agent_long_0001", name: LONG_TEXT, description: LONG_DESCRIPTION },
    { id: "agent_long_0002", name: `${LONG_TEXT} (Secondary)`, description: LONG_DESCRIPTION },
];

export const LONG_WORKFLOWS: ComponentOption[] = [{ id: "workflow_long_0001", name: LONG_TEXT, description: LONG_DESCRIPTION }];

const allInvoke = addAllCard(addAllCard([], "agent"), "workflow");

export const EXAMPLE_ROLE_SCOPES = {
    feedbackViewer: setCardGrant(setCardGrant(allInvoke, "agent", ALL_ID, "feedbackRead", true), "workflow", ALL_ID, "feedbackRead", true),
    traceViewer: setCardGrant(allInvoke, "agent", ALL_ID, "traceRead", true),
    generalReviewer: (() => {
        let s = setCardGrant(allInvoke, "agent", ALL_ID, "feedbackRead", true);
        s = setCardGrant(s, "agent", ALL_ID, "traceRead", true);
        s = setCardGrant(s, "workflow", ALL_ID, "feedbackRead", true);
        return s;
    })(),
    operationsReviewer: (() => {
        let s = setCardGrant(addCard([], "agent", DEMO_AGENTS[2].id), "agent", DEMO_AGENTS[2].id, "traceRead", true);
        s = setCardGrant(addCard(s, "workflow", DEMO_WORKFLOWS[1].id), "workflow", DEMO_WORKFLOWS[1].id, "feedbackRead", true);
        return s;
    })(),
} as const;
