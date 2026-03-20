import type { PermissionProfile } from "@/lib/types";

export const projectPermissionProfileDetails: Record<
  PermissionProfile,
  {
    label: string;
    description: string;
  }
> = {
  default: {
    label: "Default access",
    description:
      "Uses the workspace baseline agent settings until a more specific access mode is chosen.",
  },
  "full-access": {
    label: "Full access",
    description: "Grants full workspace access immediately.",
  },
  "plan-then-full-access": {
    label: "Plan, then full access",
    description:
      "Starts in planning mode with workspace-scoped access, then upgrades after the plan is approved.",
  },
};

export function nextProjectPermissionProfile(
  permissionProfile: PermissionProfile,
): PermissionProfile {
  switch (permissionProfile) {
    case "default":
      return "full-access";
    case "full-access":
      return "plan-then-full-access";
    case "plan-then-full-access":
      return "default";
  }
}

export function projectPermissionProfileButtonCopy(
  scopeLabel: string,
  permissionProfile?: PermissionProfile,
) {
  const currentProfile = permissionProfile ?? "default";
  const nextProfile = nextProjectPermissionProfile(currentProfile);
  const current = projectPermissionProfileDetails[currentProfile];
  const next = projectPermissionProfileDetails[nextProfile];
  const nextActionText = `Click to switch to ${next.label.toLowerCase()}.`;

  return {
    currentProfile,
    currentLabel: current.label,
    currentDescription: current.description,
    nextProfile,
    nextLabel: next.label,
    nextActionText,
    ariaLabel: `${scopeLabel}: ${current.label}. ${current.description} ${nextActionText}`,
  };
}
