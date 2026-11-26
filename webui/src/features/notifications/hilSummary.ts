// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2UIComponent, A2UISurface } from "@/types/a2ui";

const INPUT_COMPONENTS = new Set(["Button", "ChoicePicker", "TextField"]);
const CONTAINERS = new Set(["Row", "Column"]);
const ROOT_ID = "root";
const TITLE_ID = "title";

const SELECTION_HEADING_ID = "summary-selection-heading";
const SELECTION_HEADING: A2UIComponent[] = [
    { id: "summary-selection-icon", component: "Icon", icon: "selection" },
    { id: "summary-selection-title", component: "Text", text: { literalString: "Selection Required" }, variant: "h3" },
    { id: SELECTION_HEADING_ID, component: "Row", children: { explicitList: ["summary-selection-icon", "summary-selection-title"] } },
];

export interface HilRequestSummary {
    surface: A2UISurface;
    captions: string[];
}

function literal(val: unknown): string {
    if (typeof val === "string") return val;
    if (val && typeof val === "object" && "literalString" in val) {
        return String((val as { literalString: unknown }).literalString);
    }
    return "";
}

function childIds(c: A2UIComponent): string[] {
    const list = (c.children as { explicitList?: unknown } | undefined)?.explicitList;
    const ids = Array.isArray(list) ? list.filter((id): id is string => typeof id === "string") : [];
    return typeof c.child === "string" ? [...ids, c.child] : ids;
}

function withoutChildren(c: A2UIComponent, removed: Set<string>): A2UIComponent {
    const next: A2UIComponent = { ...c };
    const list = (c.children as { explicitList?: unknown } | undefined)?.explicitList;
    if (Array.isArray(list)) {
        next.children = { explicitList: list.filter(id => !removed.has(id as string)) };
    }
    if (typeof c.child === "string" && removed.has(c.child)) {
        delete next.child;
    }
    return next;
}

export function summarizeHilSurface(surface: A2UISurface): HilRequestSummary {
    const byId = new Map(surface.components.map(c => [c.id, c]));
    const removed = new Set<string>();
    const captions: string[] = [];
    const root = byId.get(ROOT_ID);
    const main = root?.component === "Card" && typeof root.child === "string" ? byId.get(root.child) : root;
    const topLevel = new Set(main ? childIds(main) : []);

    const removeTree = (id: string) => {
        if (removed.has(id)) return;
        removed.add(id);
        const c = byId.get(id);
        if (c) childIds(c).forEach(removeTree);
    };
    for (const c of surface.components) {
        if (INPUT_COMPONENTS.has(c.component)) {
            removeTree(c.id);
        } else if (c.component === "Text" && c.variant === "caption" && topLevel.has(c.id) && !removed.has(c.id)) {
            const text = literal(c.text).trim();
            if (text) captions.push(text);
            removed.add(c.id);
        }
    }

    let components = surface.components.filter(c => !removed.has(c.id)).map(c => withoutChildren(c, removed));
    for (;;) {
        const empty = new Set(components.filter(c => c.id !== ROOT_ID && CONTAINERS.has(c.component) && childIds(c).length === 0).map(c => c.id));
        if (empty.size === 0) break;
        components = components.filter(c => !empty.has(c.id)).map(c => withoutChildren(c, empty));
    }
    components = components.map(c => (c.id === ROOT_ID && c.component === "Card" ? asColumn(c, []) : c));
    if (needsSelectionHeading(surface.components, components)) {
        components = [...SELECTION_HEADING, ...components.map(c => (c.id === ROOT_ID ? asColumn(c, [SELECTION_HEADING_ID]) : c))];
    }

    return { surface: { ...surface, components }, captions };
}

function needsSelectionHeading(original: readonly A2UIComponent[], summarized: readonly A2UIComponent[]): boolean {
    const asksForSelection = original.some(c => c.component === "ChoicePicker");
    const titled = original.some(c => c.id === TITLE_ID);
    return asksForSelection && !titled && summarized.find(c => c.id === ROOT_ID)?.component === "Column";
}

function asColumn(c: A2UIComponent, leading: string[]): A2UIComponent {
    return { id: c.id, component: "Column", children: { explicitList: [...leading, ...childIds(c)] } };
}
