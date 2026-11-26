// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OBSERVABILITY_ROUTES } from "@/features/observability/routes";
import { BUDGETS_ROUTE } from "@/features/systemSettings/routes";

const FIRST_MATCH_WINS_PREFIX_TO_ID: ReadonlyArray<[string, string]> = [
    ["/agents", "agents"],
    ["/workflows", "workflows"],
    ["/agent-mesh", "agentMesh"],
    ["/connectors", "connectors-mgmt"],
    ["/entrypoints", "gateways-mgmt"],
    ["/toolsets", "toolsets"],
    ["/skills", "skills"],
    ["/models", "models"],
    [BUDGETS_ROUTE, "budgets"],
    ["/builder", "build"],
    ["/artifacts", "artifacts"],
    ["/scheduled-tasks", "schedules"],
    ["/activities", "activities"],
    [OBSERVABILITY_ROUTES.feedback, "feedback"],
    [OBSERVABILITY_ROUTES.usage, "usage"],
    ["/user-management", "user-management"],
    ["/evaluations/lab", "eval-lab"],
    ["/evaluations", "eval-reports"],
    ["/projects", "projects"],
    ["/prompts", "prompts"],
];

export function activeNavItemId(path: string): string {
    if (path === "/" || path.startsWith("/chat") || path.startsWith("/agent-mode/chat") || path.startsWith("/recent-chats")) {
        return "chat";
    }
    return FIRST_MATCH_WINS_PREFIX_TO_ID.find(([prefix]) => path.startsWith(prefix))?.[1] ?? "chat";
}
