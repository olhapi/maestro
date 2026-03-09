import { Fragment, type ReactNode } from 'react'
import { Link } from '@tanstack/react-router'

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

interface Crumb {
  label: string
  to?: string
  params?: Record<string, string>
}

export function PageHeader({
  eyebrow,
  title,
  description,
  crumbs = [],
  actions,
  stats,
  statsClassName,
}: {
  eyebrow?: string
  title: string
  description?: string
  crumbs?: Crumb[]
  actions?: ReactNode
  stats?: ReactNode
  statsClassName?: string
}) {
  return (
    <div className="grid gap-5">
      {crumbs.length > 0 ? (
        <Breadcrumb>
          <BreadcrumbList>
            {crumbs.map((crumb, index) => (
              <Fragment key={`${crumb.label}-${index}`}>
                <BreadcrumbItem>
                  {crumb.to ? (
                    <BreadcrumbLink asChild>
                      <Link to={crumb.to} params={crumb.params}>
                        {crumb.label}
                      </Link>
                    </BreadcrumbLink>
                  ) : (
                    <BreadcrumbPage>{crumb.label}</BreadcrumbPage>
                  )}
                </BreadcrumbItem>
                {index < crumbs.length - 1 ? <BreadcrumbSeparator /> : null}
              </Fragment>
            ))}
          </BreadcrumbList>
        </Breadcrumb>
      ) : null}

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="max-w-3xl">
          {eyebrow ? <Badge>{eyebrow}</Badge> : null}
          <h1 className={cn('mt-4 font-display text-4xl font-semibold tracking-tight text-white', eyebrow ? '' : 'mt-0')}>{title}</h1>
          {description ? <p className="mt-3 max-w-2xl text-sm leading-7 text-[var(--muted-foreground)]">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-3">{actions}</div> : null}
      </div>

      {stats ? <div className={cn('grid gap-3 md:grid-cols-2 xl:grid-cols-4', statsClassName)}>{stats}</div> : null}
    </div>
  )
}
