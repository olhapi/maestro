import * as TabsPrimitive from '@radix-ui/react-tabs'

import { cn } from '@/lib/utils'

export const Tabs = TabsPrimitive.Root

export function TabsList({ className, ...props }: TabsPrimitive.TabsListProps) {
  return (
    <TabsPrimitive.List
      className={cn('inline-flex rounded-2xl border border-white/10 bg-black/20 p-1', className)}
      {...props}
    />
  )
}

export function TabsTrigger({ className, ...props }: TabsPrimitive.TabsTriggerProps) {
  return (
    <TabsPrimitive.Trigger
      className={cn(
        'rounded-xl px-4 py-2 text-sm text-[var(--muted-foreground)] transition data-[state=active]:bg-white data-[state=active]:text-black',
        className,
      )}
      {...props}
    />
  )
}

export const TabsContent = TabsPrimitive.Content
