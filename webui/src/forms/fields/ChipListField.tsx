// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ClipboardEvent, type KeyboardEvent, type MouseEvent, useRef, useState } from "react";
import { CircleX, X } from "lucide-react";
import { useFormContext } from "react-hook-form";

import { cn } from "@/utils";

import { NO_BROWSER_FILL } from "../browserFill";
import { type FieldProps, FieldShell } from "../FieldShell";

import { addEntries, joinPaste, type ParseEntry } from "./chipList";

export interface ChipListFieldProps extends FieldProps {
    parseEntry: ParseEntry;
    splitPattern?: RegExp;
}

const DEFAULT_SPLIT = /[,\s]+/;
const COMMIT_KEYS = new Set(["Enter", ",", "Tab"]);

export function ChipListField({ placeholder, disabled, parseEntry, splitPattern = DEFAULT_SPLIT, ...shell }: Readonly<ChipListFieldProps>) {
    const [draft, setDraft] = useState("");
    const inputRef = useRef<HTMLInputElement | null>(null);
    const { trigger } = useFormContext();

    return (
        <FieldShell {...shell}>
            {({ field, fieldState, controlProps }) => {
                const chips: string[] = Array.isArray(field.value) ? field.value : [];

                const setChips = (next: string[]) => {
                    field.onChange(next);
                    trigger(shell.name).catch(() => undefined);
                };

                const commit = (raw: string) => {
                    setChips(addEntries(chips, raw, parseEntry, splitPattern));
                    setDraft("");
                };

                const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
                    if (COMMIT_KEYS.has(event.key) && !event.shiftKey && draft.trim() !== "") {
                        event.preventDefault();
                        commit(draft);
                    } else if (event.key === "Enter") {
                        event.preventDefault();
                    } else if (event.key === "Backspace" && draft === "" && chips.length > 0) {
                        setChips(chips.slice(0, -1));
                    }
                };

                const onPaste = (event: ClipboardEvent<HTMLInputElement>) => {
                    event.preventDefault();
                    commit(joinPaste(draft, event.clipboardData.getData("text")));
                };

                const focusInput = (event: MouseEvent<HTMLElement>) => {
                    if (event.target instanceof Element && event.target.closest("button, input")) return;
                    event.preventDefault();
                    inputRef.current?.focus();
                };

                const remove = (chip: string) => {
                    setChips(chips.filter(c => c !== chip));
                    inputRef.current?.focus();
                };

                return (
                    <div
                        onMouseDown={focusInput}
                        className={cn(
                            "flex min-h-9 w-full min-w-0 cursor-text flex-wrap items-center gap-1 rounded-xs border bg-transparent px-2 py-1 shadow-xs transition-[color,box-shadow] focus-within:border-(--brand-wMain)",
                            fieldState.invalid && "border-(--error-w100)",
                            disabled && "cursor-not-allowed opacity-50"
                        )}
                    >
                        {chips.length > 0 && (
                            <ul className="flex flex-wrap gap-1">
                                {chips.map(chip => {
                                    const problem = parseEntry(chip)?.problem;
                                    return (
                                        <li
                                            key={chip}
                                            title={problem}
                                            className={cn("inline-flex max-w-full items-center gap-1 overflow-hidden rounded px-2 py-0.5 text-sm", problem ? "bg-(--error-w10) text-(--error-wMain) ring-1 ring-(--error-wMain)" : "bg-(--primary-w10)")}
                                        >
                                            {problem && <CircleX className="h-3.5 w-3.5 flex-shrink-0" aria-hidden="true" />}
                                            <span className="truncate">{chip}</span>
                                            {problem && <span className="sr-only">{problem}</span>}
                                            <button type="button" aria-label={`Remove ${chip}`} disabled={disabled} className="flex-shrink-0 cursor-pointer text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)" onClick={() => remove(chip)}>
                                                <X className="h-3.5 w-3.5" />
                                            </button>
                                        </li>
                                    );
                                })}
                            </ul>
                        )}
                        <input
                            {...controlProps}
                            ref={element => {
                                inputRef.current = element;
                                field.ref(element);
                            }}
                            type="text"
                            name={field.name}
                            autoComplete={NO_BROWSER_FILL}
                            value={draft}
                            placeholder={chips.length === 0 ? placeholder : undefined}
                            disabled={disabled}
                            onChange={event => setDraft(event.target.value)}
                            onKeyDown={onKeyDown}
                            onPaste={onPaste}
                            onBlur={() => {
                                if (draft.trim() !== "") commit(draft);
                                field.onBlur();
                            }}
                            className="min-w-[120px] flex-1 bg-transparent py-0.5 text-base outline-none placeholder:text-(--secondary-wMain) disabled:cursor-not-allowed md:text-sm"
                        />
                    </div>
                );
            }}
        </FieldShell>
    );
}
