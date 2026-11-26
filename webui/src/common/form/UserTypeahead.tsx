// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ChangeEvent, type FC, type KeyboardEvent, useCallback, useEffect, useRef, useState } from "react";
import { cva } from "class-variance-authority";
import { X } from "lucide-react";

import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "@/ui/popover";
import { Spinner } from "@/ui/spinner";

import type { Person } from "@/types";

import { classForEmptyMessage, classForIconButton } from "../projectShareVariants";

import { usePeopleSearchTypeahead } from "./usePeopleSearchTypeahead";

interface UserTypeaheadProps {
    id: string;
    onSelect: (email: string, id: string) => void;
    onRemove: (id: string) => void;
    excludeEmails: string[];
    selectedEmail?: string | null;
    error?: boolean;
    hideRoleBadge?: boolean;
    hideCloseButton?: boolean;
}

export const UserTypeahead: FC<UserTypeaheadProps> = ({ id, onSelect, onRemove, excludeEmails, selectedEmail, error, hideRoleBadge = false, hideCloseButton = false }) => {
    const [isKeyboardMode, setIsKeyboardMode] = useState(false);
    const [isOpen, setIsOpen] = useState(true);
    const inputRef = useRef<HTMLInputElement>(null);

    const { searchQuery, setSearchQuery, activeIndex, setActiveIndex, filteredPeople, isLoading } = usePeopleSearchTypeahead({ excludeEmails });

    useEffect(() => {
        inputRef.current?.focus();
    }, []);

    const handleSelect = useCallback(
        (person: Person) => {
            onSelect(person.workEmail, id);
            setSearchQuery("");
        },
        [onSelect, id, setSearchQuery]
    );

    const handleClose = useCallback(() => {
        onRemove(id);
    }, [id, onRemove]);

    const handleKeyDown = useCallback(
        (e: KeyboardEvent) => {
            if (e.key === "Escape") {
                e.preventDefault();
                handleClose();
            } else if (e.key === "ArrowDown") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.min(prev + 1, filteredPeople.length - 1));
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.max(prev - 1, 0));
            } else if (e.key === "Enter") {
                e.preventDefault();
                if (filteredPeople.length > 0 && filteredPeople[activeIndex]) {
                    handleSelect(filteredPeople[activeIndex]);
                }
            }
        },
        [filteredPeople, activeIndex, handleSelect, handleClose, setActiveIndex]
    );

    useEffect(() => {
        const activeElement = document.getElementById(`user-typeahead-${id}-item-${activeIndex}`);
        if (activeElement) {
            activeElement.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
    }, [activeIndex, id]);

    const showResults = searchQuery.length > 0 && !selectedEmail;

    const handleInputChange = useCallback(
        (e: ChangeEvent<HTMLInputElement>) => {
            const newValue = e.target.value;
            if (selectedEmail) {
                onSelect("", id);
                setSearchQuery(newValue);
            } else {
                setSearchQuery(newValue);
            }
        },
        [selectedEmail, onSelect, id, setSearchQuery]
    );

    return (
        <>
            <Popover open={isOpen && showResults} onOpenChange={setIsOpen}>
                <PopoverAnchor asChild>
                    <div className="relative">
                        <Input
                            ref={inputRef}
                            type="text"
                            placeholder="Search by email..."
                            value={selectedEmail || searchQuery}
                            onChange={handleInputChange}
                            onKeyDown={handleKeyDown}
                            onFocus={() => setIsOpen(true)}
                            className={classForTypeaheadInput({ error })}
                        />
                        {isLoading && <Spinner size="small" variant="muted" className="absolute top-1/2 right-3 -translate-y-1/2" />}
                    </div>
                </PopoverAnchor>

                <PopoverContent align="start" className="w-(--radix-popover-trigger-width) min-w-[350px] p-0" onOpenAutoFocus={e => e.preventDefault()}>
                    <div className="max-h-[250px] overflow-y-auto">
                        {filteredPeople.length === 0 ? (
                            <div className={classForEmptyMessage({ size: "compact" })}>No users found</div>
                        ) : (
                            <div className="flex flex-col">
                                {filteredPeople.map((person, index) => (
                                    <button
                                        key={person.id}
                                        id={`user-typeahead-${id}-item-${index}`}
                                        onMouseDown={e => e.preventDefault()}
                                        onClick={() => handleSelect(person)}
                                        onMouseEnter={() => {
                                            setIsKeyboardMode(false);
                                            setActiveIndex(index);
                                        }}
                                        className={classForTypeaheadItem({ active: index === activeIndex, hoverEnabled: !isKeyboardMode })}
                                    >
                                        <div className="flex items-start gap-3">
                                            <div className="min-w-0 flex-1">
                                                <div className="flex flex-wrap items-center gap-2">
                                                    <span className="text-sm font-medium">{person.workEmail}</span>
                                                </div>
                                            </div>
                                        </div>
                                    </button>
                                ))}
                            </div>
                        )}
                    </div>
                </PopoverContent>
            </Popover>
            {!hideRoleBadge && (
                <Badge variant="secondary" className="justify-self-center">
                    Viewer
                </Badge>
            )}
            {!hideCloseButton && (
                <Button variant="ghost" size="sm" onClick={handleClose} className={classForIconButton()}>
                    <X className="h-4 w-4" />
                </Button>
            )}
        </>
    );
};

const classForTypeaheadItem = cva(["w-full", "px-3", "py-2", "text-left", "transition-colors"], {
    variants: {
        active: {
            true: "bg-(--primary-w10)",
            false: "",
        },
        hoverEnabled: {
            true: "hover:bg-(--primary-w10)",
            false: "",
        },
    },
    defaultVariants: { active: false, hoverEnabled: true },
});

const classForTypeaheadInput = cva(["h-9", "pr-9"], {
    variants: {
        error: {
            true: "border-(--error-wMain)",
            false: "",
        },
    },
    defaultVariants: { error: false },
});
