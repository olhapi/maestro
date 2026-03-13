import type { SelectHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'h-11 w-full cursor-pointer appearance-none rounded-xl border border-white/10 bg-[url("data:image/svg+xml,%3Csvg%20xmlns=%27http://www.w3.org/2000/svg%27%20viewBox=%270%200%2016%2016%27%20fill=%27none%27%20stroke=%27white%27%20stroke-width=%272%27%20stroke-linecap=%27round%27%20stroke-linejoin=%27round%27%3E%3Cpath%20d=%27m4%206%204%204%204-4%27/%3E%3C/svg%3E")] bg-[length:1rem_1rem] bg-[right_1rem_center] bg-no-repeat px-4 pr-10 text-sm text-white outline-none transition disabled:cursor-not-allowed disabled:opacity-50 focus:border-[var(--accent)]',
        className,
      )}
      {...props}
    />
  )
}
