import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

const mediaVariants = cva('mb-2 flex shrink-0 items-center justify-center', {
  variants: {
    variant: {
      default: '',
      icon: 'size-12 rounded-2xl border border-white/10 bg-white/5 text-[var(--accent)] [&_svg]:size-6',
    },
  },
  defaultVariants: {
    variant: 'default',
  },
})

export function Empty({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex min-h-[260px] flex-col items-center justify-center rounded-[1.75rem] border border-dashed border-white/12 bg-white/[0.03] p-8 text-center', className)} {...props} />
}

export function EmptyHeader({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex max-w-md flex-col items-center gap-3', className)} {...props} />
}

export function EmptyMedia({ className, variant, ...props }: ComponentProps<'div'> & VariantProps<typeof mediaVariants>) {
  return <div className={cn(mediaVariants({ variant }), className)} {...props} />
}

export function EmptyTitle({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('text-xl font-semibold text-white', className)} {...props} />
}

export function EmptyDescription({ className, ...props }: ComponentProps<'p'>) {
  return <p className={cn('text-sm leading-6 text-[var(--muted-foreground)]', className)} {...props} />
}

export function EmptyContent({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('mt-4 flex flex-wrap items-center justify-center gap-3', className)} {...props} />
}
