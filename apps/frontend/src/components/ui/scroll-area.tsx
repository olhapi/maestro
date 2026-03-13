import * as ScrollAreaPrimitive from '@radix-ui/react-scroll-area'
import type { PropsWithChildren } from 'react'

import { cn } from '@/lib/utils'

export function ScrollArea({ className, children }: PropsWithChildren<{ className?: string }>) {
  return (
    <ScrollAreaPrimitive.Root className={cn('relative overflow-hidden', className)}>
      <ScrollAreaPrimitive.Viewport className="h-full w-full rounded-[inherit]">{children}</ScrollAreaPrimitive.Viewport>
      <ScrollAreaPrimitive.Scrollbar orientation="vertical" className="w-2.5 p-0.5">
        <ScrollAreaPrimitive.Thumb className="rounded-full bg-white/15" />
      </ScrollAreaPrimitive.Scrollbar>
      <ScrollAreaPrimitive.Scrollbar orientation="horizontal" className="h-2.5 p-0.5">
        <ScrollAreaPrimitive.Thumb className="rounded-full bg-white/15" />
      </ScrollAreaPrimitive.Scrollbar>
      <ScrollAreaPrimitive.Corner />
    </ScrollAreaPrimitive.Root>
  )
}
