// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { defaultVersions, weatherPromptGroup } from "@/testing/data/prompts";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { VersionHistoryPage } from "./VersionHistoryPage";

const handlers = withDefaults(
    http.get(`*/api/v1/prompts/groups/${weatherPromptGroup.id}/prompts`, () => {
        return HttpResponse.json({
            data: defaultVersions,
            meta: getPaginatedMeta(defaultVersions.length),
        });
    }),
    http.get("*/api/v1/people/search*", () => HttpResponse.json({ data: [] }))
);

const meta = {
    title: "Prompts/VersionHistoryPage",
    component: VersionHistoryPage,
    parameters: {
        docs: {
            description: {
                component: "The component for templating and building custom prompts",
            },
        },
    },
} satisfies Meta<typeof VersionHistoryPage>;

export default meta;
type Story = StoryObj<typeof VersionHistoryPage>;

export const Default: Story = {
    args: {
        group: weatherPromptGroup,
    },
    parameters: {
        msw: { handlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        for (const version of defaultVersions) {
            const versionElem = await canvas.findByTestId(version.id);
            expect(versionElem).toBeVisible();
        }
    },
};

export const AsEditor: Story = {
    args: {
        group: { ...weatherPromptGroup, userId: "owner@example.com", accessLevel: "resource_editor" },
    },
    parameters: {
        msw: { handlers },
        configContext: { identityServiceType: "okta" },
        featureContext: { prompt_sharing: true },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        for (const version of defaultVersions) {
            const versionElem = await canvas.findByTestId(version.id);
            expect(versionElem).toBeVisible();
        }
        expect(canvas.queryByText(/delete all versions/i)).not.toBeInTheDocument();
        expect(canvas.queryByText(/^delete version$/i)).not.toBeInTheDocument();
    },
};

export const AsViewer: Story = {
    args: {
        group: { ...weatherPromptGroup, userId: "owner@example.com", accessLevel: "resource_viewer" },
    },
    parameters: {
        msw: { handlers },
        configContext: { identityServiceType: "okta" },
        featureContext: { prompt_sharing: true },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        for (const version of defaultVersions) {
            const versionElem = await canvas.findByTestId(version.id);
            expect(versionElem).toBeVisible();
        }
        expect(canvas.queryByText(/delete all versions/i)).not.toBeInTheDocument();
        expect(canvas.queryByText(/^delete version$/i)).not.toBeInTheDocument();
    },
};
