// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Clock, ListChecks } from "lucide-react";

import { Card, CardContent } from "@/ui/card";
import { Checkbox } from "@/ui/checkbox";
import { Input } from "@/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";
import { Textarea } from "@/ui/textarea";

import type { A2UIComponent, A2UISurface } from "@/types";
import { Button } from "@/ui";
import { cn } from "@/utils";

import { ArgValue } from "./ArgValue";
import type { SurfaceAction } from "./types";

function lit(val: unknown): string {
    if (typeof val === "string") return val;
    if (val && typeof val === "object" && "literalString" in val) {
        return String((val as { literalString: string }).literalString);
    }
    return "";
}

function buildIndex(components: A2UIComponent[]): Map<string, A2UIComponent> {
    const map = new Map<string, A2UIComponent>();
    for (const c of components) {
        if (c.id) map.set(c.id, c);
    }
    return map;
}

function getChildrenIds(comp: A2UIComponent): string[] {
    const children = comp.children as { explicitList?: string[] } | undefined;
    if (children?.explicitList) return children.explicitList;
    if (typeof comp.child === "string") return [comp.child];
    return [];
}

export function getByPath(model: Record<string, unknown>, path: string): unknown {
    const parts = path.replace(/^\//, "").split("/");
    let current: unknown = model;
    for (const part of parts) {
        if (current && typeof current === "object" && part in (current as Record<string, unknown>)) {
            current = (current as Record<string, unknown>)[part];
        } else {
            return undefined;
        }
    }
    return current;
}

export function setByPath(model: Record<string, unknown>, path: string, value: unknown): Record<string, unknown> {
    const parts = path.replace(/^\//, "").split("/");
    const clone = structuredClone(model);
    let current: Record<string, unknown> = clone;
    for (let i = 0; i < parts.length - 1; i++) {
        if (!(parts[i] in current) || typeof current[parts[i]] !== "object") {
            current[parts[i]] = {};
        }
        current = current[parts[i]] as Record<string, unknown>;
    }
    current[parts[parts.length - 1]] = value;
    return clone;
}

export function resolveContext(context: Record<string, unknown>, model: Record<string, unknown>): Record<string, unknown> {
    const resolved: Record<string, unknown> = {};
    for (const [key, val] of Object.entries(context)) {
        if (val && typeof val === "object" && "path" in val) {
            resolved[key] = getByPath(model, (val as { path: string }).path);
        } else {
            resolved[key] = val;
        }
    }
    return resolved;
}

interface RenderCtx {
    index: Map<string, A2UIComponent>;
    model: Record<string, unknown>;
    onModelChange: (model: Record<string, unknown>) => void;
    onAction: (action: SurfaceAction) => void;
    disabled: boolean;
    isLastTabActive?: boolean;
    onLastTabActiveChange?: (isLast: boolean) => void;
    isInsideTabContent?: boolean;
    flush?: boolean;
}

function RenderComponent({ id, ctx }: { id: string; ctx: RenderCtx }) {
    const comp = ctx.index.get(id);
    if (!comp) return null;

    switch (comp.component) {
        case "Card":
            return <RenderCard comp={comp} ctx={ctx} />;
        case "Column":
            return <RenderColumn comp={comp} ctx={ctx} />;
        case "Row":
            return <RenderRow comp={comp} ctx={ctx} />;
        case "Text":
            return <RenderText comp={comp} />;
        case "Icon":
            return <RenderIcon comp={comp} />;
        case "ChoicePicker":
            return <RenderChoicePicker comp={comp} ctx={ctx} />;
        case "TextField":
            return <RenderTextField comp={comp} ctx={ctx} />;
        case "Button":
            return <RenderButton comp={comp} ctx={ctx} />;
        case "Tabs":
            return <RenderTabs comp={comp} ctx={ctx} />;
        default:
            return null;
    }
}

function RenderChildren({ ids, ctx }: { ids: string[]; ctx: RenderCtx }) {
    return (
        <>
            {ids.map(id => (
                <RenderComponent key={id} id={id} ctx={ctx} />
            ))}
        </>
    );
}

function RenderCard({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const childIds = getChildrenIds(comp);
    return (
        <Card className="w-full gap-0 py-0" noPadding>
            <CardContent noPadding>
                <RenderChildren ids={childIds} ctx={ctx.flush ? { ...ctx, flush: false } : ctx} />
            </CardContent>
        </Card>
    );
}

function RenderColumn({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const childIds = getChildrenIds(comp);
    const visibleIds = childIds.filter(id => !id.endsWith("-other"));
    return (
        <div className={ctx.flush ? "flex flex-col gap-3" : "flex flex-col gap-3 px-5 py-3"}>
            <RenderChildren ids={visibleIds} ctx={ctx} />
        </div>
    );
}

function hasActionEvent(comp: A2UIComponent, eventName: string): boolean {
    const action = comp.action as Record<string, unknown> | undefined;
    const event = action?.event as Record<string, unknown> | undefined;
    return event?.name === eventName;
}

function RenderRow({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const childIds = getChildrenIds(comp);
    const justify = comp.justify === "end" ? "justify-end" : "justify-start";

    const sortedChildIds = [...childIds].sort((aId, bId) => {
        const aComp = ctx.index.get(aId);
        const bComp = ctx.index.get(bId);
        if (!aComp || !bComp) return 0;
        if (aComp.component !== "Button" || bComp.component !== "Button") return 0;
        const aIsPrimary = aComp.variant === "primary" || hasActionEvent(aComp, "submit");
        const bIsPrimary = bComp.variant === "primary" || hasActionEvent(bComp, "submit");
        if (aIsPrimary && !bIsPrimary) return 1;
        if (!aIsPrimary && bIsPrimary) return -1;
        return 0;
    });

    return (
        <div className={cn("flex items-center gap-2", justify)}>
            <RenderChildren ids={sortedChildIds} ctx={ctx} />
        </div>
    );
}

function RenderText({ comp }: { comp: A2UIComponent }) {
    const text = lit(comp.text);
    const variant = comp.variant as string | undefined;
    const tooltip = typeof comp.tooltip === "string" ? comp.tooltip : undefined;
    const multiline = comp.multiline === true;

    if (multiline) {
        return <ArgValue value={text} contentType={comp.contentType} tooltip={tooltip} />;
    }

    switch (variant) {
        case "h3":
            return (
                <span className="text-sm font-semibold break-words" title={tooltip}>
                    {text}
                </span>
            );
        case "caption":
            return (
                <span className="text-xs break-words whitespace-pre-wrap" title={tooltip}>
                    {text}
                </span>
            );
        case "body":
        default:
            return (
                <span className="text-sm break-words whitespace-pre-wrap" title={tooltip}>
                    {text}
                </span>
            );
    }
}

function RenderIcon({ comp }: { comp: A2UIComponent }) {
    const icon = comp.icon as string;
    switch (icon) {
        case "warning":
            return <AlertTriangle className="size-5 text-(--warning-wMain)" />;
        case "selection":
            return <ListChecks className="size-5 text-(--primary-wMain)" />;
        default:
            return <span className="size-5" />;
    }
}

function RenderChoicePicker({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const variant = comp.variant as string;
    const isMulti = variant === "multipleSelection";
    const rawOptions = Array.isArray(comp.options) ? (comp.options as Record<string, unknown>[]) : [];
    const valuePath = (comp.value as { path?: string })?.path ?? "";
    const currentValue = valuePath ? getByPath(ctx.model, valuePath) : undefined;

    const options = rawOptions.map(o => ({
        label: lit(o.label),
        value: String(o.value ?? ""),
        description: o.description ? lit(o.description) : undefined,
    }));

    const handleSingleSelect = (val: string) => {
        if (valuePath) ctx.onModelChange(setByPath(ctx.model, valuePath, val));
    };

    const handleMultiToggle = (val: string, checked: boolean) => {
        const current = Array.isArray(currentValue) ? (currentValue as string[]) : [];
        const updated = checked ? [...current, val] : current.filter(v => v !== val);
        if (valuePath) ctx.onModelChange(setByPath(ctx.model, valuePath, updated));
    };

    return (
        <div className="space-y-1">
            {options.map(opt =>
                isMulti ? (
                    <label key={opt.value} className="flex cursor-pointer items-start gap-2.5 rounded-md px-2 py-1.5 text-sm transition-colors">
                        <Checkbox checked={Array.isArray(currentValue) && currentValue.includes(opt.value)} onCheckedChange={(checked: boolean) => handleMultiToggle(opt.value, checked)} disabled={ctx.disabled} className="mt-0.5" />
                        <div className="min-w-0 flex-1">
                            <span className="break-words">{opt.label}</span>
                            {opt.description && <p className="text-xs break-words">{opt.description}</p>}
                        </div>
                    </label>
                ) : (
                    (() => {
                        const isOtherSelected = opt.value === "__other__" && currentValue === "__other__";
                        const isSelected = currentValue === opt.value;
                        const inputId = `${comp.id}-${opt.value}`;
                        return (
                            <label
                                key={opt.value}
                                htmlFor={inputId}
                                className={cn(
                                    "flex cursor-pointer gap-3 rounded-md px-4 py-2 text-sm transition-colors",
                                    isOtherSelected ? "items-center" : "items-start",
                                    isSelected ? "bg-(--brand-wMain)/10" : "",
                                    ctx.disabled && "cursor-not-allowed opacity-60"
                                )}
                            >
                                <span className={cn("relative inline-flex shrink-0 items-center justify-center", !isOtherSelected && "mt-0.5")}>
                                    <input
                                        id={inputId}
                                        type="radio"
                                        name={comp.id}
                                        value={opt.value}
                                        checked={isSelected}
                                        onChange={() => handleSingleSelect(opt.value)}
                                        disabled={ctx.disabled}
                                        className="peer h-5 w-5 cursor-pointer appearance-none rounded-full border-2 border-(--secondary-w40) bg-(--background-w10) transition-colors checked:border-(--primary-wMain) checked:bg-(--primary-wMain) focus-visible:ring-2 focus-visible:ring-(--primary-wMain) focus-visible:ring-offset-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60"
                                    />
                                    {isSelected && <span aria-hidden="true" className="pointer-events-none absolute h-2 w-2 rounded-full bg-(--background-w10)" />}
                                </span>
                                <div className="flex min-w-0 flex-1 items-center gap-3">
                                    <div className={cn("min-w-0", !isOtherSelected && "flex-1")}>
                                        <span className="break-words">{opt.label}</span>
                                        {opt.description && <p className="text-xs break-words">{opt.description}</p>}
                                    </div>
                                    {opt.value === "__other__" && currentValue === "__other__" && (
                                        <div className="min-w-0 flex-1">
                                            <RenderComponent id={`${comp.id.replace(/-picker$/, "")}-other`} ctx={ctx} />
                                        </div>
                                    )}
                                </div>
                            </label>
                        );
                    })()
                )
            )}
        </div>
    );
}

function RenderTextField({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const placeholder = lit(comp.placeholder);
    const valuePath = (comp.value as { path?: string })?.path ?? "";
    const currentValue = valuePath ? String(getByPath(ctx.model, valuePath) ?? "") : "";
    const isMultiline = comp.multiline === true;

    const isOtherField = typeof comp.id === "string" && comp.id.endsWith("-other");
    const indentClass = isOtherField ? "ml-7" : "";

    const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
        if (valuePath) ctx.onModelChange(setByPath(ctx.model, valuePath, e.target.value));
    };

    if (isMultiline) {
        return <Textarea placeholder={placeholder} value={currentValue} onChange={handleChange} disabled={ctx.disabled} rows={3} className={cn("resize-y", indentClass)} />;
    }

    return <Input placeholder={placeholder} value={currentValue} onChange={handleChange} disabled={ctx.disabled} className={cn(indentClass)} />;
}

function RenderButton({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const childIds = getChildrenIds(comp);
    const variant = comp.variant as string | undefined;
    const action = comp.action as
        | {
              event?: { name?: string; context?: Record<string, unknown>; completionText?: string };
          }
        | undefined;
    const eventName = action?.event?.name ?? "";
    const eventContext = action?.event?.context ?? {};
    const completionText = action?.event?.completionText ?? "";

    const isCancel = eventName === "cancel";
    const isSubmit = eventName === "submit";
    const isPrimary = variant === "primary";

    if (isSubmit && ctx.isLastTabActive === false) {
        return null;
    }
    if (isCancel && ctx.isLastTabActive === false && !ctx.isInsideTabContent) {
        return null;
    }

    const handleClick = () => {
        const resolved = resolveContext(eventContext, ctx.model);
        ctx.onAction({ eventName, context: resolved, completionText });
    };

    return (
        <Button size="sm" variant={isPrimary ? "default" : isCancel ? "ghost" : "outline"} onClick={handleClick} disabled={ctx.disabled}>
            <RenderChildren ids={childIds} ctx={ctx} />
        </Button>
    );
}

interface TabItem {
    title: unknown;
    child: string;
}

function RenderTabs({ comp, ctx }: { comp: A2UIComponent; ctx: RenderCtx }) {
    const tabItems = (comp.tabItems as TabItem[]) ?? [];
    const autoAdvance = comp["x-auto-advance"] === true;
    const [activeTab, setActiveTab] = useState(tabItems[0]?.child ?? "");

    useEffect(() => {
        if (autoAdvance && ctx.onLastTabActiveChange) {
            const isLast = tabItems.length <= 1 || activeTab === tabItems[tabItems.length - 1]?.child;
            ctx.onLastTabActiveChange(isLast);
        }
    }, [activeTab, autoAdvance, tabItems, ctx]);

    const advanceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

    const handleModelChange = useCallback(
        (newModel: Record<string, unknown>) => {
            ctx.onModelChange(newModel);

            if (!autoAdvance) return;

            const currentIndex = tabItems.findIndex(t => t.child === activeTab);
            if (currentIndex < 0 || currentIndex >= tabItems.length - 1) return;

            const panel = ctx.index.get(activeTab);
            if (!panel) return;
            const childIds = getChildrenIds(panel);
            const picker = childIds.map(id => ctx.index.get(id)).find(c => c?.component === "ChoicePicker");

            if (!picker || picker.variant !== "mutuallyExclusive") return;

            const pickerPath = (picker.value as { path?: string })?.path ?? "";
            const newValue = pickerPath ? getByPath(newModel, pickerPath) : undefined;

            if (typeof newValue === "string" && newValue !== "" && newValue !== "__other__") {
                if (advanceTimer.current) clearTimeout(advanceTimer.current);
                advanceTimer.current = setTimeout(() => {
                    setActiveTab(tabItems[currentIndex + 1].child);
                    advanceTimer.current = null;
                }, 300);
            }
        },
        [ctx, autoAdvance, activeTab, tabItems]
    );

    const tabCtx: RenderCtx = useMemo(
        () => ({
            ...ctx,
            onModelChange: autoAdvance ? handleModelChange : ctx.onModelChange,
        }),
        [ctx, autoAdvance, handleModelChange]
    );

    const handleTabChange = useCallback((value: string) => {
        if (advanceTimer.current) {
            clearTimeout(advanceTimer.current);
            advanceTimer.current = null;
        }
        setActiveTab(value);
    }, []);

    return (
        <Tabs value={activeTab} onValueChange={handleTabChange}>
            <TabsList className="flex h-auto max-w-full [scrollbar-width:thin] divide-x overflow-x-auto rounded-md border bg-transparent p-0 [&::-webkit-scrollbar]:h-1 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-(--secondary-w20)">
                {tabItems.map(item => (
                    <TabsTrigger
                        key={item.child}
                        value={item.child}
                        className="shrink-0 rounded-none border-0 border-l border-(--secondary-w20) px-4 py-1.5 text-xs whitespace-nowrap text-(--secondary-text-wMain) first:border-l-0 hover:bg-(--secondary-w10) data-[state=active]:bg-(--primary-w10) data-[state=active]:font-semibold data-[state=active]:text-(--primary-text-wMain) data-[state=active]:shadow-none"
                    >
                        {lit(item.title)}
                    </TabsTrigger>
                ))}
            </TabsList>
            {tabItems.map((item, idx) => (
                <TabsContent key={item.child} value={item.child}>
                    <RenderComponent id={item.child} ctx={tabCtx} />
                    {!ctx.flush && idx < tabItems.length - 1 && (
                        <div className="flex items-center justify-end gap-2 px-4 pt-2">
                            <RenderComponent id="cancel-btn" ctx={{ ...tabCtx, isInsideTabContent: true }} />
                            <Button size="sm" variant="default" onClick={() => setActiveTab(tabItems[idx + 1].child)} disabled={ctx.disabled}>
                                Next
                            </Button>
                        </div>
                    )}
                </TabsContent>
            ))}
        </Tabs>
    );
}

interface A2UISurfaceRendererProps {
    surface: A2UISurface;
    onAction: (action: SurfaceAction) => void;
    disabled?: boolean;
    expiresAt?: string;
    flush?: boolean;
}

export function A2UISurfaceRenderer({ surface, onAction, disabled = false, expiresAt, flush = false }: Readonly<A2UISurfaceRendererProps>) {
    const [model, setModel] = useState<Record<string, unknown>>(() => (surface.dataModel as Record<string, unknown>) ?? {});
    const [isLastTabActive, setIsLastTabActive] = useState<boolean | undefined>(undefined);

    const isExpired = useIsExpired(expiresAt);

    const actionsDisabled = disabled || isExpired;

    const index = useMemo(() => buildIndex(surface.components), [surface.components]);

    const handleAction = useCallback(
        (action: SurfaceAction) => {
            if (isExpired) return;
            onAction(action);
        },
        [onAction, isExpired]
    );

    const handleLastTabActiveChange = useCallback((isLast: boolean) => {
        setIsLastTabActive(isLast);
    }, []);

    const ctx: RenderCtx = useMemo(
        () => ({
            index,
            model,
            onModelChange: setModel,
            onAction: handleAction,
            disabled: actionsDisabled,
            isLastTabActive,
            onLastTabActiveChange: handleLastTabActiveChange,
            flush,
        }),
        [index, model, handleAction, actionsDisabled, isLastTabActive, handleLastTabActiveChange, flush]
    );

    const rootId = surface.components.find(c => c.id === "root")?.id ?? surface.components[0]?.id;
    if (!rootId) return null;

    return (
        <div className="w-full">
            <RenderComponent id={rootId} ctx={ctx} />
            {expiresAt && !isExpired && <CountdownTimer expiresAt={expiresAt} />}
            {isExpired && <p className="py-2 text-center text-xs text-(--secondary-w40)">This request expired — ask again to continue.</p>}
        </div>
    );
}

const MAX_TIMEOUT_DELAY_MS = 2_147_483_647;

export function isPastExpiry(expiresAt?: string): boolean {
    if (!expiresAt) return false;
    const t = new Date(expiresAt).getTime();
    return !Number.isNaN(t) && Date.now() >= t;
}

export function useIsExpired(expiresAt?: string): boolean {
    const [isExpired, setIsExpired] = useState(() => isPastExpiry(expiresAt));
    useEffect(() => {
        if (!expiresAt) {
            setIsExpired(false);
            return;
        }
        const remaining = new Date(expiresAt).getTime() - Date.now();
        if (Number.isNaN(remaining)) return;
        if (remaining <= 0) {
            setIsExpired(true);
            return;
        }
        setIsExpired(false);
        if (remaining > MAX_TIMEOUT_DELAY_MS) return;
        const id = setTimeout(() => setIsExpired(true), remaining);
        return () => clearTimeout(id);
    }, [expiresAt]);
    return isExpired;
}

const COUNTDOWN_VISIBLE_THRESHOLD_MS = 60_000;

function CountdownTimer({ expiresAt }: { expiresAt: string }) {
    const [text, setText] = useState("");

    useEffect(() => {
        setText("");
        const expires = new Date(expiresAt).getTime();
        if (Number.isNaN(expires)) return;

        let interval: ReturnType<typeof setInterval> | undefined;

        const tick = () => {
            const remaining = expires - Date.now();
            if (remaining <= 0) {
                setText("Expired");
                return false;
            }
            const mins = Math.floor(remaining / 60000);
            const secs = Math.floor((remaining % 60000) / 1000);
            setText(mins > 0 ? `${mins}m ${secs}s` : `${secs}s`);
            return true;
        };

        const start = () => {
            if (!tick()) return;
            interval = setInterval(() => {
                if (!tick()) clearInterval(interval);
            }, 1000);
        };

        let startTimer: ReturnType<typeof setTimeout> | undefined;
        const untilVisible = expires - Date.now() - COUNTDOWN_VISIBLE_THRESHOLD_MS;
        if (untilVisible > 0) {
            startTimer = setTimeout(start, untilVisible);
        } else {
            start();
        }

        return () => {
            if (startTimer) clearTimeout(startTimer);
            if (interval) clearInterval(interval);
        };
    }, [expiresAt]);

    if (!text) return null;

    return (
        <div className="flex items-center justify-center gap-1.5 py-2 text-xs">
            <Clock className="size-3" />
            <span>{text}</span>
        </div>
    );
}
