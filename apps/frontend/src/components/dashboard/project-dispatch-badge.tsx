import { AlertTriangle, FolderOpen, FolderX } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import {
  projectDispatchBadgeClass,
  projectDispatchGuidance,
  projectDispatchLabel,
} from "@/lib/projects";
import type { Project } from "@/lib/types";
import { cn } from "@/lib/utils";

type DispatchProject = Pick<
  Project,
  "dispatch_error" | "dispatch_ready" | "orchestration_ready" | "repo_path" | "workflow_path"
>;

function DispatchGuidanceDetails({
  Icon,
  guidance,
  showTip,
}: {
  Icon: typeof AlertTriangle;
  guidance: NonNullable<ReturnType<typeof projectDispatchGuidance>>;
  showTip?: boolean;
}) {
  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <p className="inline-flex items-center gap-2 text-sm font-semibold text-white">
          <Icon className="size-4 text-[var(--accent)]" />
          {guidance.title}
        </p>
        <p className="text-xs leading-5 text-[var(--muted-foreground)]">
          {guidance.summary}
        </p>
        {showTip ? (
          <p className="text-xs leading-5 text-[var(--muted-foreground)]">
            {guidance.mobileTip}
          </p>
        ) : null}
      </div>

      <div className="grid gap-1.5 text-xs leading-5 text-[var(--muted-foreground)]">
        {guidance.steps.map((step, index) => (
          <p key={step}>
            <span className="mr-2 font-medium text-white/60">{index + 1}.</span>
            {step}
          </p>
        ))}
      </div>

      {guidance.context.length > 0 ? (
        <div className="grid gap-1 rounded-xl border border-white/8 bg-black/20 p-3 text-[11px] leading-5 text-[var(--muted-foreground)]">
          {guidance.context.map((line) => (
            <p key={line} className="break-all">
              {line}
            </p>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function ProjectDispatchBadge({
  project,
  align = "end",
  className,
}: {
  project: DispatchProject;
  align?: "start" | "center" | "end";
  className?: string;
}) {
  const isMobileLayout = useIsMobileLayout();
  const guidance = projectDispatchGuidance(project);
  if (!guidance) {
    return null;
  }

  const Icon =
    guidance.kind === "needs_repo_setup"
      ? FolderOpen
      : guidance.kind === "out_of_scope"
        ? FolderX
        : AlertTriangle;
  const badge = (
    <Badge
      className={cn(projectDispatchBadgeClass(project), "cursor-help", className)}
      tabIndex={0}
    >
      {projectDispatchLabel(project)}
    </Badge>
  );

  if (isMobileLayout) {
    return (
      <div className="flex w-full max-w-md flex-col items-start gap-2">
        {badge}
        <div className="w-full rounded-2xl border border-white/10 bg-black/20 p-4">
          <DispatchGuidanceDetails
            Icon={Icon}
            guidance={guidance}
            showTip
          />
        </div>
      </div>
    );
  }

  return (
    <HoverCard openDelay={120} closeDelay={120}>
      <HoverCardTrigger asChild>
        {badge}
      </HoverCardTrigger>
      <HoverCardContent align={align}>
        <DispatchGuidanceDetails Icon={Icon} guidance={guidance} />
      </HoverCardContent>
    </HoverCard>
  );
}
