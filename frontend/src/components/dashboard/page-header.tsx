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

      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0 flex-1">
          {eyebrow ? <Badge className="w-fit">{eyebrow}</Badge> : null}
          <h1
            className={cn(
              "font-display text-[length:var(--page-title-size)] font-semibold leading-[var(--page-title-line-height)] tracking-tight text-white",
              eyebrow ? "mt-3" : "mt-0",
            )}
          >
            {title}
          </h1>
          {description ? (
            <p
              className={cn(
                "mt-2.5 max-w-2xl text-sm leading-6 text-[var(--muted-foreground)]",
                descriptionClassName,
              )}
            >
              {description}
            </p>
          ) : null}
        </div>
        {actions ? (
          <div className="flex shrink-0 flex-wrap items-center gap-2.5 lg:justify-end">
            {actions}
          </div>
        ) : null}
      </div>

      {stats ? <div className={cn("grid gap-3 sm:grid-cols-2 lg:grid-cols-4", statsClassName)}>{stats}</div> : null}
    </div>
  );
}
