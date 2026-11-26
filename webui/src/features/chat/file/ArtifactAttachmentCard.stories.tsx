// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor, within } from "storybook/test";

import type { ArtifactWithSession } from "@/api/artifacts";

import { ArtifactAttachmentCard } from "@/features/chat/file/ArtifactAttachmentCard";

import { withDefaults } from "@/testing/helpers";

const oneByte = new Uint8Array([0x50]);

const baseArtifact: ArtifactWithSession = {
    filename: "slides.pptx",
    size: 1024,
    mimeType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
    lastModified: "2026-01-01T00:00:00Z",
    uri: "artifact://sess-1/slides.pptx",
    sessionId: "sess-1",
    sessionName: "Session One",
};

const meta = {
    title: "Chat/ArtifactAttachmentCard",
    component: ArtifactAttachmentCard,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component:
                    "Card-style chip for attached existing artifacts. Critical contract: PPTX/DOCX mime types contain the substring 'xml' and would otherwise pass `supportsTextPreview` — `isDoc` MUST be evaluated first so binary office files don't render as garbled text. Storybook play tests guard that precedence in real Chromium where the document renderers run.",
            },
        },
    },
    argTypes: {
        onClick: { action: "clicked" },
        onRemove: { action: "removed" },
    },
} satisfies Meta<typeof ArtifactAttachmentCard>;

export default meta;
type Story = StoryObj<typeof meta>;

const fakeContentHandler = (filename: string, mime: string) =>
    http.get(`*/api/v1/artifacts/:sessionId/${filename}/versions/latest`, () => {
        return new HttpResponse(oneByte, {
            headers: { "Content-Type": mime },
        });
    });

export const PptxRendersDocumentThumbnail: Story = {
    args: { artifact: baseArtifact },
    parameters: {
        msw: { handlers: withDefaults(fakeContentHandler("slides.pptx", baseArtifact.mimeType)) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTitle(/slides\.pptx/i);

        await waitFor(() => {
            expect(canvas.queryByText(/^P{2,}/)).not.toBeInTheDocument();
        });
    },
};

export const DocxRendersDocumentThumbnail: Story = {
    args: {
        artifact: {
            ...baseArtifact,
            filename: "letter.docx",
            mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            uri: "artifact://sess-1/letter.docx",
        },
    },
    parameters: {
        msw: { handlers: withDefaults(fakeContentHandler("letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTitle(/letter\.docx/i);
        await waitFor(() => {
            expect(canvas.queryByText(/^P{2,}/)).not.toBeInTheDocument();
        });
    },
};

export const TextRendersInlineSnippet: Story = {
    args: {
        artifact: {
            ...baseArtifact,
            filename: "notes.txt",
            mimeType: "text/plain",
            uri: "artifact://sess-1/notes.txt",
        },
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get(`*/api/v1/artifacts/:sessionId/notes.txt/versions/latest`, () => {
                    return new HttpResponse("hello from notes\nsecond line\n", {
                        headers: { "Content-Type": "text/plain" },
                    });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTitle(/notes\.txt/i);
        await waitFor(() => {
            expect(canvas.getByText(/hello from notes/i)).toBeInTheDocument();
        });
    },
};
