// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import type { ArtifactWithSession } from "@/api/artifacts";

import { AttachArtifactDialog } from "@/features/chat/file/AttachArtifactDialog";

import { paginatedResponse, withDefaults } from "@/testing/helpers";

const HostedDialog = (props: { onAttach?: (a: ArtifactWithSession[]) => void }) => {
    const [open] = useState(true);
    return <AttachArtifactDialog isOpen={open} onClose={() => {}} onAttach={props.onAttach ?? (() => {})} />;
};

const meta = {
    title: "Chat/AttachArtifactDialog",
    component: HostedDialog,
    parameters: {
        docs: {
            description: {
                component: "Existing-artifact picker. Real IntersectionObserver in the browser project lets us drive the infinite-scroll sentinel end-to-end — jsdom can only stub the observer.",
            },
        },
    },
} satisfies Meta<typeof HostedDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

const makePage = (start: number, count: number, hasMore: boolean) => {
    const items = Array.from({ length: count }, (_, i) => ({
        filename: `file-${start + i}.txt`,
        size: 100,
        mimeType: "text/plain",
        lastModified: "2026-01-01T00:00:00Z",
        uri: `artifact://sess-1/file-${start + i}.txt`,
        sessionId: "sess-1",
        sessionName: "Session One",
        projectId: null,
        projectName: null,
        source: null,
        tags: null,
    }));
    const nextPage = hasMore ? Math.floor(start / count) + 2 : null;
    return {
        data: items,
        meta: {
            pagination: {
                pageNumber: Math.floor(start / count) + 1,
                pageSize: count,
                count: hasMore ? start + count + 50 : start + count,
                nextPage,
                totalPages: hasMore ? nextPage! + 5 : Math.floor(start / count) + 1,
            },
        },
    };
};

export const InfiniteScrollLoadsNextPage: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/artifacts/all", ({ request }) => {
                    const url = new URL(request.url);
                    const page = Number(url.searchParams.get("pageNumber") ?? "1");
                    if (page === 1) {
                        return HttpResponse.json(makePage(1, 30, true));
                    }
                    return HttpResponse.json(makePage(31, 10, false));
                })
            ),
        },
    },
    play: async () => {
        const root = within(document.body);

        await root.findByText("file-1.txt");
        await root.findByText("file-30.txt");

        expect(root.queryByText("file-31.txt")).not.toBeInTheDocument();

        const role = await root.findByRole("listbox");
        const scrollContainer = role.parentElement as HTMLElement;
        scrollContainer.scrollTop = scrollContainer.scrollHeight;

        await waitFor(
            () => {
                expect(root.getByText("file-31.txt")).toBeInTheDocument();
            },
            { timeout: 5000 }
        );
    },
};

export const VersionPickerEncodesSelectedVersion: Story = {
    args: {
        onAttach: fn(),
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v1/artifacts/all", () => {
                    return HttpResponse.json(
                        paginatedResponse([
                            {
                                filename: "report.pdf",
                                size: 200,
                                mimeType: "application/pdf",
                                lastModified: "2026-01-01T00:00:00Z",
                                uri: "artifact://my-app/user-1/sess-1/report.pdf",
                                version: 2,
                                sessionId: "sess-1",
                                sessionName: "Session One",
                                projectId: null,
                                projectName: null,
                                source: null,
                                tags: null,
                            },
                        ])
                    );
                }),
                http.get("*/api/v1/artifacts/:sessionId/report.pdf/versions", () => {
                    return HttpResponse.json(paginatedResponse([0, 1, 2]));
                })
            ),
        },
    },
    play: async ({ args }) => {
        const root = within(document.body);

        const row = await root.findByText("report.pdf");
        const trigger = await root.findByRole("combobox");

        expect(trigger).toHaveTextContent("Version 2");

        await userEvent.click(trigger);

        const v1 = await waitFor(() => root.getByText("Version 1"));
        await userEvent.click(v1);

        await userEvent.click(row);

        const attachBtn = await root.findByRole("button", { name: /attach 1/i });
        await userEvent.click(attachBtn);

        await waitFor(() => {
            expect(args.onAttach).toHaveBeenCalled();
        });
        const emitted = (args.onAttach as ReturnType<typeof fn>).mock.calls[0][0] as ArtifactWithSession[];
        expect(emitted[0].uri).toBe("artifact://my-app/user-1/sess-1/report.pdf?version=1");
    },
};
