// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { Button, Textarea } from "@/ui";

interface FeedbackModalProps {
    isOpen: boolean;
    onClose: () => void;
    feedbackType: "up" | "down";
    onSubmit: (feedbackText: string) => Promise<void>;
}

export const FeedbackModal = React.memo<FeedbackModalProps>(({ isOpen, onClose, feedbackType, onSubmit }) => {
    const [feedbackText, setFeedbackText] = useState("");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const textareaRef = useRef<HTMLTextAreaElement>(null);

    useEffect(() => {
        if (isOpen) {
            setFeedbackText("");
            setIsSubmitting(false);
            setTimeout(() => {
                textareaRef.current?.focus();
            }, 100);
        }
    }, [isOpen]);

    const handleSubmit = async () => {
        setIsSubmitting(true);
        try {
            await onSubmit(feedbackText);
            onClose();
        } catch {
            setIsSubmitting(false);
        }
    };

    const handleClose = () => {
        if (!isSubmitting) {
            onClose();
        }
    };

    const feedbackPrompt =
        feedbackType === "up" ? (
            <>
                What did you like about the response? <span className="text-(--secondary-text-wMain)">(Optional)</span>
            </>
        ) : (
            <>
                What did you dislike about the response? <span className="text-(--secondary-text-wMain)">(Optional)</span>
            </>
        );

    return (
        <Dialog open={isOpen} onOpenChange={handleClose}>
            <DialogContent className="sm:max-w-[750px]" showCloseButton={false}>
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        Provide Feedback
                        {feedbackType === "up" ? <ThumbsUp className="h-4 w-4 text-(--secondary-text-wMain)" aria-hidden /> : <ThumbsDown className="h-4 w-4 text-(--secondary-text-wMain)" aria-hidden />}
                    </DialogTitle>
                    <DialogDescription className="flex flex-col gap-2 pb-2">
                        <span>{feedbackPrompt}</span>
                    </DialogDescription>
                </DialogHeader>
                <div className="flex flex-col gap-2">
                    <Textarea ref={textareaRef} value={feedbackText} onChange={e => setFeedbackText(e.target.value)} className="min-h-[72px] text-sm" disabled={isSubmitting} />
                    <p className="mt-4 text-xs text-(--secondary-text-wMain)">This conversation may be viewed to help improve responses</p>
                </div>
                <DialogFooter>
                    <Button variant="ghost" onClick={handleClose} disabled={isSubmitting}>
                        Cancel
                    </Button>
                    <Button variant="default" onClick={handleSubmit} disabled={isSubmitting}>
                        Submit Feedback
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
});
