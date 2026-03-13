import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { PropsWithChildren } from 'react'

import { cn } from '@/lib/utils'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogContent({
  children,
  className,
}: PropsWithChildren<{
  className?: string
}>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm" />
      <DialogPrimitive.Content
        className={cn(
          'fixed left-1/2 top-1/2 z-50 w-[min(94vw,720px)] -translate-x-1/2 -translate-y-1/2 rounded-[1.75rem] border border-white/10 bg-[var(--panel)] p-6 shadow-2xl',
          className,
        )}
      >
        <DialogPrimitive.Close className="absolute right-4 top-4 rounded-full p-2 text-[var(--muted-foreground)] hover:bg-white/8 hover:text-white">
          <X className="size-4" />
        </DialogPrimitive.Close>
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export const DialogTitle = DialogPrimitive.Title
export const DialogDescription = DialogPrimitive.Description
