// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { ChevronDown, UserRound, X } from "lucide-react";

import { UserTypeahead } from "@/common/form/UserTypeahead";

import { Button } from "@/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

export interface FeedbackUserFilterProps {
    value: string | null;
    onChange: (userId: string | null) => void;
}

export function FeedbackUserFilter({ value, onChange }: Readonly<FeedbackUserFilterProps>) {
    const [open, setOpen] = useState(false);

    const choose = (email: string) => {
        if (!email) return;
        setOpen(false);
        onChange(email);
    };

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button variant="outline" aria-label="User" className="w-56 justify-between gap-2 font-normal">
                    <span className="flex min-w-0 items-center gap-2">
                        <UserRound className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
                        <span className="truncate">{value ?? "All Users"}</span>
                    </span>
                    <ChevronDown className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
                </Button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-3" align="start">
                <div className="flex flex-col gap-2">
                    <UserTypeahead id="feedback-user-filter" selectedEmail={value} onSelect={choose} onRemove={() => setOpen(false)} excludeEmails={[]} hideRoleBadge hideCloseButton />
                    {value && (
                        <Button
                            variant="ghost"
                            className="self-start"
                            onClick={() => {
                                setOpen(false);
                                onChange(null);
                            }}
                        >
                            <X className="size-4" /> All Users
                        </Button>
                    )}
                </div>
            </PopoverContent>
        </Popover>
    );
}
