// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/ui/dialog";

export interface ConfirmationDialogProps {
    open: boolean;
    title: string;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void | Promise<void>;

    onCancel?: () => void;

    content?: React.ReactNode;
    description?: React.ReactNode;

    isLoading?: boolean;
    isEnabled?: boolean;

    actionLabels?: {
        cancel?: string;
        confirm?: string;
    };

    trigger?: React.ReactNode;
}

export const ConfirmationDialog: React.FC<ConfirmationDialogProps> = ({ open, title, content, description, actionLabels, trigger, isLoading, isEnabled = true, onOpenChange, onConfirm, onCancel }) => {
    const cancelTitle = actionLabels?.cancel ?? "Cancel";
    const confirmTitle = actionLabels?.confirm ?? "Confirm";

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle className="flex min-w-0 flex-row gap-1 break-words">{title}</DialogTitle>
                    <DialogDescription>{description}</DialogDescription>
                </DialogHeader>
                <div className="min-w-0 break-words">{content}</div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button
                            variant="ghost"
                            title={cancelTitle}
                            onClick={e => {
                                e.stopPropagation();
                                onCancel?.();
                            }}
                            disabled={isLoading}
                        >
                            {cancelTitle}
                        </Button>
                    </DialogClose>
                    <Button
                        data-testid="dialogConfirmButton"
                        variant="outline"
                        title={confirmTitle}
                        onClick={async e => {
                            e.stopPropagation();
                            try {
                                await onConfirm();
                                onOpenChange(false);
                            } catch (err) {
                                console.error("Confirmation action failed:", err);
                            }
                        }}
                        disabled={isLoading || !isEnabled}
                    >
                        {confirmTitle}
                    </Button>
                </DialogFooter>
                {isLoading && (
                    <>
                        <style>{`
                        @keyframes progressBarSlide {
                            0% { transform: translateX(-100%); }
                            100% { transform: translateX(400%); }
                        }
                        .progress-bar-animate {
                            animation: progressBarSlide 2s ease-in-out infinite;
                            width: 25%;
                            background: var(--brand-wMain);
                        }
                        `}</style>
                        <div className="absolute right-1 bottom-0 left-1 h-1 overflow-hidden rounded-full bg-(--secondary-w10)">
                            <div className="progress-bar-animate h-full rounded-full"></div>
                        </div>
                    </>
                )}
            </DialogContent>
        </Dialog>
    );
};
