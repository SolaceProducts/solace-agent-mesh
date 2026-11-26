// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { A2UISurface } from "@/types/a2ui";

import { summarizeHilSurface } from "./hilSummary";

const lit = (s: string) => ({ literalString: s });

const approval: A2UISurface = {
    surfaceId: "req-1",
    catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
    dataModel: {},
    components: [
        { id: "warn-icon", component: "Icon", icon: "warning" },
        { id: "title", component: "Text", text: lit("Approval Required"), variant: "h3" },
        { id: "title-row", component: "Row", children: { explicitList: ["warn-icon", "title"] } },
        { id: "info", component: "Text", text: lit('Agent "OpsAgent" wants to run tool: restart_service'), variant: "body" },
        { id: "approve-text", component: "Text", text: lit("Approve") },
        { id: "approve-btn", component: "Button", child: "approve-text", variant: "primary", action: { event: { name: "submit" } } },
        { id: "deny-text", component: "Text", text: lit("Deny") },
        { id: "deny-btn", component: "Button", child: "deny-text", action: { event: { name: "submit" } } },
        { id: "actions", component: "Row", children: { explicitList: ["approve-btn", "deny-btn"] }, justify: "end" },
        { id: "countdown", component: "Text", text: lit("Expires in 30 minutes"), variant: "caption" },
        { id: "main-col", component: "Column", children: { explicitList: ["title-row", "info", "actions", "countdown"] } },
        { id: "root", component: "Card", child: "main-col" },
    ],
};

describe("summarizeHilSurface", () => {
    it("keeps the prompt's text, drops its buttons and their labels, and lifts the expiry caption out", () => {
        const { surface, captions } = summarizeHilSurface(approval);
        const ids = surface.components.map(c => c.id);
        expect(ids).toEqual(expect.arrayContaining(["warn-icon", "title", "title-row", "info", "main-col", "root"]));
        for (const gone of ["approve-btn", "approve-text", "deny-btn", "deny-text", "actions", "countdown"]) {
            expect(ids).not.toContain(gone);
        }
        expect(surface.components.find(c => c.id === "main-col")?.children).toEqual({ explicitList: ["title-row", "info"] });
        expect(captions).toEqual(["Expires in 30 minutes"]);
    });

    it("replaces the outer card with a plain column so the details have no frame", () => {
        const root = summarizeHilSurface(approval).surface.components.find(c => c.id === "root");
        expect(root).toEqual({ id: "root", component: "Column", children: { explicitList: ["main-col"] } });
    });

    it("drops a question's choice picker and text field, keeps the question, and opens with Selection Required", () => {
        const question: A2UISurface = {
            ...approval,
            components: [
                { id: "q", component: "Text", text: lit("Which region should the report cover?"), variant: "h3" },
                { id: "picker", component: "ChoicePicker", options: [] },
                { id: "other", component: "TextField", label: lit("Please specify") },
                { id: "col", component: "Column", children: { explicitList: ["q", "picker", "other"] } },
                { id: "root", component: "Card", child: "col" },
            ],
        };
        const { components } = summarizeHilSurface(question).surface;
        expect(components.map(c => c.id)).toEqual(["summary-selection-icon", "summary-selection-title", "summary-selection-heading", "q", "col", "root"]);
        expect(components.find(c => c.id === "root")).toEqual({ id: "root", component: "Column", children: { explicitList: ["summary-selection-heading", "col"] } });
        expect(components.find(c => c.id === "summary-selection-title")?.text).toEqual(lit("Selection Required"));
    });

    it("gives a free-text question no Selection Required heading", () => {
        const question: A2UISurface = {
            ...approval,
            components: [
                { id: "q", component: "Text", text: lit("What should the summary be called?"), variant: "h3" },
                { id: "answer", component: "TextField", label: lit("Title") },
                { id: "col", component: "Column", children: { explicitList: ["q", "answer"] } },
                { id: "root", component: "Card", child: "col" },
            ],
        };
        expect(summarizeHilSurface(question).surface.components.map(c => c.id)).toEqual(["q", "col", "root"]);
    });

    it("puts Selection Required above the message and the tabs of a multi-question form", () => {
        const tabbed: A2UISurface = {
            ...approval,
            components: [
                { id: "msg", component: "Text", text: lit("A couple of questions first."), variant: "body" },
                { id: "q0-label", component: "Text", text: lit("Which region?"), variant: "h3" },
                { id: "q0-picker", component: "ChoicePicker", variant: "mutuallyExclusive", options: [{ label: lit("EMEA"), value: "emea" }], value: { path: "/answers/q0" } },
                { id: "q0-other", component: "TextField", label: lit("Other") },
                { id: "q0-panel", component: "Column", children: { explicitList: ["q0-label", "q0-picker", "q0-other"] } },
                { id: "q1-label", component: "Text", text: lit("What should it be called?"), variant: "h3" },
                { id: "q1-text", component: "TextField", label: lit("Title"), value: { path: "/answers/q1" } },
                { id: "q1-panel", component: "Column", children: { explicitList: ["q1-label", "q1-text"] } },
                {
                    id: "question-tabs",
                    component: "Tabs",
                    tabItems: [
                        { title: lit("Region"), child: "q0-panel" },
                        { title: lit("Title"), child: "q1-panel" },
                    ],
                },
                { id: "submit-text", component: "Text", text: lit("Submit") },
                { id: "submit-btn", component: "Button", child: "submit-text", action: { event: { name: "submit" } } },
                { id: "actions", component: "Row", children: { explicitList: ["submit-btn"] } },
                { id: "main-col", component: "Column", children: { explicitList: ["msg", "question-tabs", "actions"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
        };
        const { components } = summarizeHilSurface(tabbed).surface;
        expect(components.find(c => c.id === "root")?.children).toEqual({ explicitList: ["summary-selection-heading", "main-col"] });
        expect(components.find(c => c.id === "main-col")?.children).toEqual({ explicitList: ["msg", "question-tabs"] });
        expect(components.find(c => c.id === "summary-selection-icon")?.icon).toBe("selection");
    });

    it("gives a volume prompt that offers volumes no second heading over its own title", () => {
        const volume: A2UISurface = {
            ...approval,
            components: [
                { id: "title", component: "Text", text: lit("Volume Required"), variant: "h3" },
                { id: "prompt", component: "Text", text: lit("Pick a volume to mount"), variant: "body" },
                { id: "volume-picker", component: "ChoicePicker", variant: "mutuallyExclusive", options: [{ label: lit("data"), value: "v1" }], value: { path: "/selected_volume" } },
                { id: "manual-id", component: "TextField", label: lit("Volume ID") },
                { id: "main-col", component: "Column", children: { explicitList: ["title", "prompt", "volume-picker", "manual-id"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
        };
        const ids = summarizeHilSurface(volume).surface.components.map(c => c.id);
        expect(ids).toEqual(["title", "prompt", "main-col", "root"]);
    });

    it("keeps an approval's arguments card as a card", () => {
        const withCard: A2UISurface = {
            ...approval,
            components: [
                { id: "title", component: "Text", text: lit("Approval Required"), variant: "h3" },
                { id: "arg-env-label", component: "Text", text: lit("environment"), variant: "caption" },
                { id: "arg-env-value", component: "Text", text: lit("staging"), variant: "body", multiline: true },
                { id: "arg-env", component: "Column", children: { explicitList: ["arg-env-label", "arg-env-value"] } },
                { id: "args-fields", component: "Column", children: { explicitList: ["arg-env"] } },
                { id: "args-card", component: "Card", child: "args-fields" },
                { id: "main-col", component: "Column", children: { explicitList: ["title", "args-card"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
        };
        const { components } = summarizeHilSurface(withCard).surface;
        expect(components.find(c => c.id === "args-card")).toEqual({ id: "args-card", component: "Card", child: "args-fields" });
        expect(components.map(c => c.id)).not.toContain("summary-selection-heading");
    });

    it("leaves a surface with no root component as it was rather than leading it with the heading", () => {
        const rootless: A2UISurface = {
            ...approval,
            components: [
                { id: "col", component: "Column", children: { explicitList: ["q", "p"] } },
                { id: "q", component: "Text", text: lit("Pick one"), variant: "h3" },
                { id: "p", component: "ChoicePicker", options: [] },
            ],
        };
        const ids = summarizeHilSurface(rootless).surface.components.map(c => c.id);
        expect(ids).toEqual(["col", "q"]);
    });

    it("keeps an approval's argument names next to their values and lifts only the expiry", () => {
        const withArgs: A2UISurface = {
            ...approval,
            components: [
                { id: "title", component: "Text", text: lit("Approval Required"), variant: "h3" },
                { id: "arg-service-label", component: "Text", text: lit("service"), variant: "caption" },
                { id: "arg-service-value", component: "Text", text: lit("billing-api"), variant: "body" },
                { id: "arg-service", component: "Column", children: { explicitList: ["arg-service-label", "arg-service-value"] } },
                { id: "arg-env-label", component: "Text", text: lit("environment"), variant: "caption" },
                { id: "arg-env-value", component: "Text", text: lit("staging"), variant: "body" },
                { id: "arg-env", component: "Column", children: { explicitList: ["arg-env-label", "arg-env-value"] } },
                { id: "approve-text", component: "Text", text: lit("Approve") },
                { id: "approve-btn", component: "Button", child: "approve-text" },
                { id: "actions", component: "Row", children: { explicitList: ["approve-btn"] } },
                { id: "countdown", component: "Text", text: lit("Expires in 30 minutes"), variant: "caption" },
                { id: "main-col", component: "Column", children: { explicitList: ["title", "arg-service", "arg-env", "actions", "countdown"] } },
                { id: "root", component: "Card", child: "main-col" },
            ],
        };
        const { surface, captions } = summarizeHilSurface(withArgs);
        expect(captions).toEqual(["Expires in 30 minutes"]);
        const byId = new Map(surface.components.map(c => [c.id, c]));
        expect(byId.get("arg-service")?.children).toEqual({ explicitList: ["arg-service-label", "arg-service-value"] });
        expect(byId.get("arg-env")?.children).toEqual({ explicitList: ["arg-env-label", "arg-env-value"] });
        expect(byId.get("main-col")?.children).toEqual({ explicitList: ["title", "arg-service", "arg-env"] });
    });

    it("leaves the original surface untouched", () => {
        const before = JSON.stringify(approval);
        summarizeHilSurface(approval);
        expect(JSON.stringify(approval)).toBe(before);
    });
});
