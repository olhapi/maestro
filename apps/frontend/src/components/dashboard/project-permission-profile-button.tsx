import { useMutation } from "@tanstack/react-query";
import { Route, Shield, ShieldCheck } from "lucide-react";
import { type ComponentType } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import {
  projectPermissionProfileButtonCopy,
} from "@/lib/project-permission-profiles";
import type { PermissionProfile } from "@/lib/types";
import { cn } from "@/lib/utils";

const projectPermissionProfileMeta: Record<
  PermissionProfile,
  {
    icon: ComponentType<{ className?: string }>;
    next: PermissionProfile;
    className: string;
  }
> = {
  default: {
    icon: Shield,
    next: "full-access",
    className:
      "border-white/10 bg-white/6 text-white hover:border-white/15 hover:bg-white/10 hover:text-white",
  },
  "full-access": {
    icon: ShieldCheck,
    next: "plan-then-full-access",
    className:
      "border-emerald-400/30 bg-emerald-400/10 text-emerald-50 hover:border-emerald-300/40 hover:bg-emerald-400/15 hover:text-white",
  },
  "plan-then-full-access": {
    icon: Route,
    next: "default",
    className:
      "border-cyan-400/30 bg-cyan-400/10 text-cyan-50 hover:border-cyan-300/40 hover:bg-cyan-400/15 hover:text-white",
  },
};

export function ProjectPermissionProfileButton({
  className,
  onSuccess,
  permissionProfile,
  projectId,
  scopeLabel,
}: {
  className?: string;
  onSuccess?: (nextProfile: PermissionProfile) => Promise<void> | void;
  permissionProfile?: PermissionProfile;
  projectId: string;
  scopeLabel: string;
}) {
  const copy = projectPermissionProfileButtonCopy(
    scopeLabel,
    permissionProfile,
  );
  const meta = projectPermissionProfileMeta[copy.currentProfile];
  const Icon = meta.icon;

  const permissionMutation = useMutation({
    mutationFn: (nextProfile: PermissionProfile) =>
      api.setProjectPermissionProfile(projectId, nextProfile),
    onSuccess: async (_project, nextProfile) => {
      await onSuccess?.(nextProfile);
      toast.success("Project permissions updated");
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? `Unable to update project permissions: ${error.message}`
          : "Unable to update project permissions",
      );
    },
  });

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          aria-label={copy.ariaLabel}
          className={cn(
            "w-fit shrink-0 justify-start gap-2.5 rounded-full border px-3.5 py-2 text-left shadow-none transition",
            meta.className,
            className,
          )}
          disabled={permissionMutation.isPending}
          size="default"
          type="button"
          variant="ghost"
          onClick={() => {
            permissionMutation.mutate(copy.nextProfile);
          }}
        >
          <Icon className="size-4 shrink-0" aria-hidden="true" />
          <span className="whitespace-nowrap text-sm font-medium">
            {copy.currentLabel}
          </span>
        </Button>
      </TooltipTrigger>
      <TooltipContent className="space-y-2">
        <div className="flex items-center gap-2">
          <Icon className="size-4 shrink-0 text-[var(--accent)]" aria-hidden="true" />
          <p className="text-sm font-semibold text-white">{copy.currentLabel}</p>
        </div>
        <p className="text-xs leading-5 text-[var(--muted-foreground)]">
          {copy.currentDescription}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}
