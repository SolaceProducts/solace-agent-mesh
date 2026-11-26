// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";

import type { A2UIComponent, A2UISurface } from "@/types";

import { A2UISurfaceRenderer } from "./A2UIRenderer";

interface ArgSpec {
    key: string;
    value: string;
    contentType: string;
}

function approvalSurface(args: ArgSpec[]): A2UISurface {
    const components: A2UIComponent[] = [
        { id: "warn-icon", component: "Icon", icon: "warning" },
        { id: "title", component: "Text", text: { literalString: "Approval Required" }, variant: "h3" },
        { id: "title-row", component: "Row", children: { explicitList: ["warn-icon", "title"] } },
        { id: "info", component: "Text", text: { literalString: 'Agent "jira-confluence" wants to run tool: create_confluence_page' }, variant: "body" },
    ];
    const columnChildren = ["title-row", "info"];

    const argFieldIds: string[] = [];
    for (const a of args) {
        const fieldId = `arg-${a.key}`;
        components.push(
            { id: `${fieldId}-label`, component: "Text", text: { literalString: a.key }, variant: "caption" },
            { id: `${fieldId}-value`, component: "Text", text: { literalString: a.value }, variant: "body", multiline: true, contentType: a.contentType },
            { id: fieldId, component: "Column", children: { explicitList: [`${fieldId}-label`, `${fieldId}-value`] } }
        );
        argFieldIds.push(fieldId);
    }
    components.push({ id: "args-fields", component: "Column", children: { explicitList: argFieldIds } }, { id: "args-card", component: "Card", child: "args-fields" });
    columnChildren.push("args-card");

    components.push(
        { id: "approve-text", component: "Text", text: { literalString: "Approve" } },
        { id: "approve-btn", component: "Button", child: "approve-text", variant: "primary", action: { event: { name: "submit", context: { approved: true }, completionText: "Approved" } } },
        { id: "deny-text", component: "Text", text: { literalString: "Deny" } },
        { id: "deny-btn", component: "Button", child: "deny-text", action: { event: { name: "submit", context: { approved: false }, completionText: "Denied" } } },
        { id: "actions", component: "Row", children: { explicitList: ["approve-btn", "deny-btn"] }, justify: "end" }
    );
    columnChildren.push("actions");

    components.push({ id: "main-col", component: "Column", children: { explicitList: columnChildren } }, { id: "root", component: "Card", child: "main-col" });

    return { surfaceId: "story-approval", catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json", components, dataModel: {} };
}

const HTML_BODY = `<h1>All About Cats</h1>
<p>Cats are small, carnivorous mammals valued by humans for companionship.</p>
<ul>
  <li>Independent</li>
  <li>Playful</li>
  <li>Excellent nappers</li>
</ul>`;

const JSON_METADATA = JSON.stringify({ labels: ["release", "cats"], parentId: 12345, public: true }, null, 2);

const MARKDOWN_NOTES = `## Release notes

- **New:** cat facts page
- Fixed: litter-box pagination bug

Ship target: \`v1.2.0\``;

const meta = {
    title: "Chat/A2UIApprovalCard",
    component: A2UISurfaceRenderer,
    parameters: {
        layout: "centered",
        docs: { description: { component: "Per-tool human approval card. Tool-argument values render by content type — HTML as a sandboxed preview, JSON as a structured view, markdown rendered, plain text in a monospace block." } },
    },
    decorators: [
        Story => (
            <div style={{ width: 600 }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof A2UISurfaceRenderer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const RichArguments: Story = {
    args: {
        onAction: () => {},
        surface: approvalSurface([
            { key: "pageBody", value: HTML_BODY, contentType: "html" },
            { key: "metadata", value: JSON_METADATA, contentType: "json" },
            { key: "releaseNotes", value: MARKDOWN_NOTES, contentType: "markdown" },
            { key: "spaceKey", value: "ENG", contentType: "text" },
        ]),
    },
};

export const HtmlOnly: Story = {
    args: {
        onAction: () => {},
        surface: approvalSurface([{ key: "pageBody", value: HTML_BODY, contentType: "html" }]),
    },
};
