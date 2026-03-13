import * as ToggleGroupPrimitive from '@radix-ui/react-toggle-group'

import { cn } from '@/lib/utils'

export const ToggleGroup = ToggleGroupPrimitive.Root

export function ToggleGroupItem({ className, ...props }: ToggleGroupPrimitive.ToggleGroupItemProps) {
  return (
    <ToggleGroupPrimitive.Item
      className={cn(
        'cursor-pointer rounded-[calc(var(--panel-radius)-0.375rem)] px-3.5 py-1.5 text-sm text-[var(--muted-foreground)] transition disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-40 data-[state=on]:bg-white data-[state=on]:text-black',
        className,
      )}
      {...props}
    />
  )
}
