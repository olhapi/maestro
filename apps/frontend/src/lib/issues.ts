import type { Issue } from "@/lib/types";

export function canEditIssueType(issue?: Pick<Issue, "provider_kind">) {
  return !issue?.provider_kind || issue.provider_kind === "kanban";
}
