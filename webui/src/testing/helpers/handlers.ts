// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { http, HttpResponse, type RequestHandler } from "msw";

export const getPaginatedMeta = (count: number, pageSize = 100) => {
    const totalPages = Math.max(1, Math.ceil(count / pageSize));
    return {
        pagination: {
            pageNumber: 1,
            count,
            pageSize,
            nextPage: totalPages > 1 ? 2 : null,
            totalPages,
        },
    };
};

export const paginatedResponse = <T>(items: T[], pageSize = Math.max(items.length, 1)) => ({
    data: items,
    meta: getPaginatedMeta(items.length, pageSize),
});

export const pageFromRequest = <T>(request: Request, items: T[], defaultPageSize = 20) => {
    const url = new URL(request.url);
    const pageNumber = Math.max(1, Number(url.searchParams.get("pageNumber") ?? 1));
    const pageSize = Math.max(1, Number(url.searchParams.get("pageSize") ?? defaultPageSize));
    const count = items.length;
    const totalPages = Math.max(1, Math.ceil(count / pageSize));
    const start = (pageNumber - 1) * pageSize;
    return {
        data: items.slice(start, start + pageSize),
        meta: { pagination: { pageNumber, count, pageSize, nextPage: pageNumber < totalPages ? pageNumber + 1 : null, totalPages } },
    };
};

export const getEmptyResponse = (url: string) => {
    return http.get(url, () => HttpResponse.json(paginatedResponse([])));
};

export const getEmptyResponses = (urls: string[]) => {
    return urls.map(url => getEmptyResponse(url));
};

export const withDefaults = (...handlers: RequestHandler[]): RequestHandler[] => [...handlers, ...defaultHandlers];

export const defaultHandlers: RequestHandler[] = [
    http.get("*/api/v1/config", () => {
        return HttpResponse.json({});
    }),
    http.get("*/api/v1/config/capabilities", () => {
        return HttpResponse.json(paginatedResponse([]));
    }),
    http.get("*/api/v1/speech/config", () => {
        return HttpResponse.json({});
    }),
    http.get("*/api/v1/budget/status", () => HttpResponse.json({ ownLevel: "none", meshLimitReached: false, periodEnd: "2099-01-01T00:00:00.000Z", capKey: null })),
    ...getEmptyResponses([
        "*/api/v1/agentCards",
        "*/api/v1/sessions",
        "*/api/v1/share/sharedWithMe",
        "*/api/v1/projects",
        "*/api/v1/artifacts/all",
        "*/api/v2/platform/toolsets",
        "*/api/v2/platform/agents",
        "*/api/v2/platform/workflows",
        "*/api/v2/platform/connectors",
        "*/api/v2/platform/entrypoints",
        "*/api/v2/platform/skills",
        "*/api/v2/platform/remoteAgents",
        "*/api/v2/platform/models",
    ]),
    http.get("*/api/v1/artifacts/:sessionId", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v2/platform/modelStatus", () => HttpResponse.json({ data: { configured: true } })),
    http.get("*/networkingDetails", () => {
        return HttpResponse.json({ data: { dnsName: null, port: null, url: null, webhookUrl: null, publicUrl: null, apiPaths: [] } });
    }),
    http.get("*/api/v2/platform/models/:modelId/dependents", () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/toolsetBuildTarget", () => {
        return HttpResponse.json({ data: { os: "linux", arch: "arm64", source: "str_init", pythonVersion: "3.11" } });
    }),
    http.get("*/api/v2/platform/toolsets/:id/config", () => HttpResponse.json({ data: { values: {}, updatedBy: null, updatedTime: null } })),
    http.get("*/api/v2/platform/skills/:id/files", () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/rbac/serviceAccounts", () =>
        HttpResponse.json(
            paginatedResponse([
                { id: "6dbe83db-3549-4d99-b4fb-c3aea7b75219", sub: "system:default", name: "default", displayName: "Default", roleIds: ["system_agent_invoke"] },
                { id: "5f1c2d3e-4b5a-6c7d-8e9f-0a1b2c3d4e5f", sub: "system:helpdesk", name: "helpdesk", displayName: "Helpdesk", roleIds: [] },
            ])
        )
    ),
];
