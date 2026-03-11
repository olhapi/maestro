import { Fragment, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface Crumb {
  label: string;
  to?: string;
  params?: Record<string, string>;
}

export function PageHeader({
  eyebrow,
  title,
  description,
  descriptionClassName,
  crumbs = [],
  actions,
  stats,
  statsClassName,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  descriptionClassName?: string;
  crumbs?: Crumb[];
  actions?: ReactNode;
  stats?: ReactNode;
  statsClassName?: string;
}) {
  return (
    <div className="grid gap-[var(--section-gap)]">
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

      <div className="flex flex-wrap items-start justify-between gap-3">
        {eyebrow ? <Badge>{eyebrow}</Badge> : null}
        <h1
          className={cn(
            "mt-3 font-display text-[length:var(--page-title-size)] font-semibold tracking-tight text-white leading-[var(--page-title-line-height)]",
            eyebrow ? "" : "mt-0",
          )}
        >
          {title}
        </h1>
        {description ? (
          <p className={cn("mt-2.5 max-w-2xl text-sm leading-6 text-[var(--muted-foreground)]", descriptionClassName)}>
            {description}
          </p>
        ) : null}
        {actions ? <div className="flex flex-wrap items-center gap-2.5">{actions}</div> : null}
      </div>

      {stats ? <div className={cn("grid gap-3 sm:grid-cols-2 lg:grid-cols-4", statsClassName)}>{stats}</div> : null}
    </div>
  );
}
