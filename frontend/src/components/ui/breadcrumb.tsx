import * as React from 'react'
import { ChevronRight, MoreHorizontal } from 'lucide-react'
import { Slot } from '@radix-ui/react-slot'

import { cn } from '@/lib/utils'

export function Breadcrumb(props: React.ComponentProps<'nav'>) {
  return <nav aria-label="Breadcrumb" {...props} />
}

export function BreadcrumbList({ className, ...props }: React.ComponentProps<'ol'>) {
  return <ol className={cn('flex flex-wrap items-center gap-2 text-sm text-[var(--muted-foreground)]', className)} {...props} />
}

export function BreadcrumbItem({ className, ...props }: React.ComponentProps<'li'>) {
  return <li className={cn('inline-flex items-center gap-2', className)} {...props} />
}

export function BreadcrumbLink({
  asChild,
  className,
  ...props
}: React.ComponentProps<'a'> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : 'a'
  return <Comp className={cn('transition-colors hover:text-white', className)} {...props} />
}

export function BreadcrumbPage({ className, ...props }: React.ComponentProps<'span'>) {
  return <span className={cn('font-medium text-white', className)} aria-current="page" {...props} />
}

export function BreadcrumbSeparator({ children, className, ...props }: React.ComponentProps<'li'>) {
  return (
    <li aria-hidden="true" className={cn('text-white/30 [&>svg]:size-3.5', className)} {...props}>
      {children ?? <ChevronRight />}
    </li>
  )
}

export function BreadcrumbEllipsis({ className, ...props }: React.ComponentProps<'span'>) {
  return (
    <span className={cn('flex size-8 items-center justify-center text-white/50', className)} {...props}>
      <MoreHorizontal className="size-4" />
      <span className="sr-only">More</span>
    </span>
  )
}
