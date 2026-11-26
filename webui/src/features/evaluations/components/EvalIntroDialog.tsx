// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useThemeContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import experimentReportIllustrationDark from "../assets/experimentReportIllustrationDarkMode.svg";
import experimentReportIllustrationLight from "../assets/experimentReportIllustrationLightMode.svg";
import { EVAL_ROUTES } from "../constants";

interface EvalIntroDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly dismissed: boolean;
    readonly onDismissedChange: (dismissed: boolean) => void;
}

export function EvalIntroDialog({ open, onOpenChange, dismissed, onDismissedChange }: EvalIntroDialogProps) {
    const navigate = useNavigate();
    const { currentTheme } = useThemeContext();
    const illustration = currentTheme.includes("dark") ? experimentReportIllustrationDark : experimentReportIllustrationLight;

    const handleGetStarted = () => {
        onOpenChange(false);
        navigate(EVAL_ROUTES.LAB);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="gap-0 overflow-hidden p-0 sm:max-w-[750px]">
                <img src={illustration} alt="Evaluations overview" className="w-full" />

                <div className="flex flex-col gap-6 px-6 pt-6">
                    <DialogHeader>
                        <DialogTitle className="text-xl leading-snug font-bold">Set Up Experiments and Coach Your Agents</DialogTitle>
                        <DialogDescription className="text-sm font-medium">
                            Experiments validate that your agents respond consistently and safely, allowing you to coach your agent in the correct behaviour before you ship and catch regressions whenever a prompt, model, or tool changes.
                        </DialogDescription>
                    </DialogHeader>
                    <label className="flex cursor-pointer items-center gap-2 pt-1">
                        <Checkbox checked={dismissed} onCheckedChange={checked => onDismissedChange(!!checked)} />
                        <span className="text-sm font-medium select-none">Don't show this again</span>
                    </label>
                </div>

                <DialogFooter className="px-6 pb-6">
                    <Button variant="secondary" onClick={() => onOpenChange(false)}>
                        Close
                    </Button>
                    <Button onClick={handleGetStarted}>
                        Get Started
                        <ArrowRight className="ml-1 size-4" />
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
