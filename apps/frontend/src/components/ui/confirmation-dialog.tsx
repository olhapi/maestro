import { useState, type ReactNode } from "react";

import { Button, type ButtonProps } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export function ConfirmationDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  cancelLabel = "Cancel",
  confirmVariant = "destructive",
  isPending = false,
  detail,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  pendingLabel?: string;
  cancelLabel?: string;
  confirmVariant?: ButtonProps["variant"];
  isPending?: boolean;
  detail?: ReactNode;
  onConfirm: () => Promise<void> | void;
}) {
  const [localPending, setLocalPending] = useState(false);
  const pending = isPending || localPending;

  const handleConfirm = async () => {
    setLocalPending(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } catch {
      // Mutations surface their own errors; keep the dialog open so the operator can retry or cancel.
    } finally {
      setLocalPending(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (pending && !nextOpen) {
          return;
        }
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent showCloseButton={false} className="w-[min(92vw,30rem)] p-6">
        <div className="space-y-6">
          <DialogHeader>
            <DialogTitle className="text-xl font-semibold text-white">{title}</DialogTitle>
            <DialogDescription className="text-sm leading-6 text-[var(--muted-foreground)]">
              {description}
            </DialogDescription>
          </DialogHeader>

          {detail ? (
            <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-3 text-sm text-white">
              {detail}
            </div>
          ) : null}

          <DialogFooter>
            <Button variant="secondary" onClick={() => onOpenChange(false)} disabled={pending}>
              {cancelLabel}
            </Button>
            <Button variant={confirmVariant} onClick={() => void handleConfirm()} disabled={pending}>
              {pending ? pendingLabel ?? `${confirmLabel}...` : confirmLabel}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  );
}
