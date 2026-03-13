import * as TabsPrimitive from '@radix-ui/react-tabs'

import { cn } from '@/lib/utils'

export const Tabs = TabsPrimitive.Root

export function TabsList({ className, ...props }: TabsPrimitive.TabsListProps) {
  return (
    <TabsPrimitive.List
      className={cn('inline-flex rounded-[var(--panel-radius)] border border-white/10 bg-black/20 p-0.75', className)}
      {...props}
    />
  )
}

export function TabsTrigger({ className, ...props }: TabsPrimitive.TabsTriggerProps) {
  return (
    <TabsPrimitive.Trigger
      className={cn(
        'cursor-pointer rounded-[calc(var(--panel-radius)-0.375rem)] px-3.5 py-1.5 text-sm text-[var(--muted-foreground)] transition disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-40 data-[state=active]:bg-white data-[state=active]:text-black',
        className,
      )}
      {...props}
    />
  )
}

export const TabsContent = TabsPrimitive.Content
