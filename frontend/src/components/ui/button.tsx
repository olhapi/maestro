import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex cursor-pointer items-center justify-center gap-2 rounded-xl border font-medium transition duration-200 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-40',
  {
    variants: {
      variant: {
        default: 'border-white/10 bg-[var(--accent)] px-4 py-2 text-black shadow-[0_0_40px_rgba(196,255,87,.18)] hover:bg-[var(--accent-strong)]',
        secondary: 'border-white/10 bg-white/5 px-4 py-2 text-white hover:bg-white/10',
        ghost: 'border-transparent bg-transparent px-3 py-2 text-[var(--muted-foreground)] hover:bg-white/6 hover:text-white',
        destructive: 'border-red-500/30 bg-red-500/15 px-4 py-2 text-red-100 hover:bg-red-500/20',
      },
      size: {
        default: 'h-10',
        sm: 'h-9 px-3 text-sm',
        lg: 'h-11 px-5',
        icon: 'h-10 w-10',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  ),
)

Button.displayName = 'Button'
