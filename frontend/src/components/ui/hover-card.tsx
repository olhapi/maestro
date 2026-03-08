import * as HoverCardPrimitive from '@radix-ui/react-hover-card'
import type * as React from 'react'

import { cn } from '@/lib/utils'

export const HoverCard = HoverCardPrimitive.Root
export const HoverCardTrigger = HoverCardPrimitive.Trigger

export function HoverCardContent({
  className,
  align = 'center',
  sideOffset = 8,
  ...props
}: React.ComponentProps<typeof HoverCardPrimitive.Content>) {
  return (
    <HoverCardPrimitive.Portal>
      <HoverCardPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn('z-50 w-72 rounded-2xl border border-white/10 bg-[rgba(12,16,22,.98)] p-4 text-white shadow-2xl', className)}
        {...props}
      />
    </HoverCardPrimitive.Portal>
  )
}
