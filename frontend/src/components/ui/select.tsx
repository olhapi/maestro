import type { SelectHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'h-11 w-full rounded-xl border border-white/10 bg-black/20 px-4 text-sm text-white outline-none transition focus:border-[var(--accent)]',
        className,
      )}
      {...props}
    />
  )
}
