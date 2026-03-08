import * as ContextMenuPrimitive from '@radix-ui/react-context-menu'
import { Check, ChevronRight, Circle } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

export const ContextMenu = ContextMenuPrimitive.Root
export const ContextMenuTrigger = ContextMenuPrimitive.Trigger
export const ContextMenuGroup = ContextMenuPrimitive.Group
export const ContextMenuPortal = ContextMenuPrimitive.Portal
export const ContextMenuSub = ContextMenuPrimitive.Sub
export const ContextMenuRadioGroup = ContextMenuPrimitive.RadioGroup

export function ContextMenuSubTrigger({
  className,
  inset,
  children,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.SubTrigger> & { inset?: boolean }) {
  return (
    <ContextMenuPrimitive.SubTrigger
      className={cn(
        'flex cursor-default items-center rounded-xl px-3 py-2 text-sm text-white outline-none transition data-[state=open]:bg-white/10 focus:bg-white/10',
        inset && 'pl-8',
        className,
      )}
      {...props}
    >
      {children}
      <ChevronRight className="ml-auto size-4 text-[var(--muted-foreground)]" />
    </ContextMenuPrimitive.SubTrigger>
  )
}

export function ContextMenuSubContent({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.SubContent>) {
  return (
    <ContextMenuPrimitive.SubContent
      className={cn('z-50 min-w-[12rem] rounded-2xl border border-white/10 bg-[rgba(13,17,24,.98)] p-2 shadow-2xl', className)}
      {...props}
    />
  )
}

export function ContextMenuContent({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Content>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        className={cn('z-50 min-w-[12rem] rounded-2xl border border-white/10 bg-[rgba(13,17,24,.98)] p-2 shadow-2xl', className)}
        {...props}
      />
    </ContextMenuPrimitive.Portal>
  )
}

export function ContextMenuItem({
  className,
  inset,
  variant = 'default',
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Item> & { inset?: boolean; variant?: 'default' | 'destructive' }) {
  return (
    <ContextMenuPrimitive.Item
      className={cn(
        'flex cursor-default items-center gap-2 rounded-xl px-3 py-2 text-sm outline-none transition focus:bg-white/10',
        variant === 'destructive' ? 'text-red-200 focus:bg-red-500/10' : 'text-white',
        inset && 'pl-8',
        className,
      )}
      {...props}
    />
  )
}

export function ContextMenuCheckboxItem({
  className,
  children,
  checked,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.CheckboxItem>) {
  return (
    <ContextMenuPrimitive.CheckboxItem className={cn('relative rounded-xl py-2 pl-8 pr-3 text-sm text-white outline-none focus:bg-white/10', className)} checked={checked} {...props}>
      <span className="absolute left-3 top-1/2 -translate-y-1/2">
        <ContextMenuPrimitive.ItemIndicator>
          <Check className="size-4" />
        </ContextMenuPrimitive.ItemIndicator>
      </span>
      {children}
    </ContextMenuPrimitive.CheckboxItem>
  )
}

export function ContextMenuRadioItem({ className, children, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.RadioItem>) {
  return (
    <ContextMenuPrimitive.RadioItem className={cn('relative rounded-xl py-2 pl-8 pr-3 text-sm text-white outline-none focus:bg-white/10', className)} {...props}>
      <span className="absolute left-3 top-1/2 -translate-y-1/2">
        <ContextMenuPrimitive.ItemIndicator>
          <Circle className="size-2 fill-current" />
        </ContextMenuPrimitive.ItemIndicator>
      </span>
      {children}
    </ContextMenuPrimitive.RadioItem>
  )
}

export function ContextMenuLabel({ className, inset, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Label> & { inset?: boolean }) {
  return <ContextMenuPrimitive.Label className={cn('px-3 py-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]', inset && 'pl-8', className)} {...props} />
}

export function ContextMenuSeparator({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Separator>) {
  return <ContextMenuPrimitive.Separator className={cn('my-2 h-px bg-white/8', className)} {...props} />
}

export function ContextMenuShortcut({ className, ...props }: React.ComponentProps<'span'>) {
  return <span className={cn('ml-auto text-xs tracking-[0.18em] text-[var(--muted-foreground)]', className)} {...props} />
}
