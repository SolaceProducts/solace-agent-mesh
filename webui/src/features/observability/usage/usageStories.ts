// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { http, type RequestHandler } from "msw";
import { screen, within } from "storybook/test";

import { USAGE_BUDGETS, usageUserRow } from "@/testing/data/usage";
import { withDefaults } from "@/testing/helpers";
import { defaultUsageDataset, type UsageDataset, usageHandlers } from "@/testing/msw/usageHandlers";

import { OBSERVABILITY_ROUTES } from "../routes";

export const usageAt = (query = "") => ({ routerValues: { initialPath: `${OBSERVABILITY_ROUTES.usage}${query}`, routePath: OBSERVABILITY_ROUTES.usage } });

export const usageMsw = (dataset: UsageDataset = defaultUsageDataset, ...first: RequestHandler[]) => ({ msw: { handlers: withDefaults(...first, ...usageHandlers(dataset)) } });

export function recordRequests(path: string) {
    const urls: URL[] = [];
    const handler = http.get(path, ({ request }) => {
        urls.push(new URL(request.url));
    });
    return { urls, handler, clear: () => (urls.length = 0) };
}

export const regionOf = (container: HTMLElement, name: string) => within(within(container).getByRole("region", { name }));
export const tableOf = (canvasElement: HTMLElement) => regionOf(canvasElement, "Cost Breakdown");
export const detailPanel = async () => within(await screen.findByTestId("usageDetailPanel"));

export const chartRows = (container: HTMLElement, name: string) => within(within(container).getByRole("region", { name })).getAllByRole("row").slice(1);

export const lastAxisDay = (container: HTMLElement, name: string) => [...within(container).getByRole("region", { name }).querySelectorAll(".recharts-xAxis-tick-labels text")].at(-1);

export const barOpacities = (chart: Element, selector = ".recharts-bar-rectangle path") => [...chart.querySelectorAll(selector)].map(bar => bar.getAttribute("fill-opacity"));

export const engineeringDataset: UsageDataset = {
    ...defaultUsageDataset,
    rows: {
        ...defaultUsageDataset.rows,
        users: [
            ...defaultUsageDataset.rows.users,
            usageUserRow({ name: "Avery Platform", cost: "162.40", activities: 702, budget: USAGE_BUDGETS.engineering }),
            usageUserRow({ name: "Quinn Data", cost: "128.00", activities: 540, budget: USAGE_BUDGETS.engineering }),
            usageUserRow({ name: "Taylor Mobile", cost: "141.20", activities: 610, budget: { ...USAGE_BUDGETS.engineering, topUp: "50" } }),
            usageUserRow({ name: "Lee Newhire", cost: "0", activities: 0, budget: USAGE_BUDGETS.engineering }),
        ],
    },
};
