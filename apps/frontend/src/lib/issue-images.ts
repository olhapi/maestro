import { api } from "@/lib/api";

export interface IssueImageChangeSet {
  newImages: File[];
  removeImageIDs: string[];
}

export interface IssueImageSyncResult {
  failures: string[];
  removed: number;
  uploaded: number;
}

export const issueImageInputAccept =
  "image/png,image/jpeg,image/webp,image/gif";

export function hasIssueImageChanges(changes: IssueImageChangeSet): boolean {
  return changes.newImages.length > 0 || changes.removeImageIDs.length > 0;
}

export async function applyIssueImageChanges(
  identifier: string,
  changes: IssueImageChangeSet,
): Promise<IssueImageSyncResult> {
  const result: IssueImageSyncResult = {
    failures: [],
    removed: 0,
    uploaded: 0,
  };

  for (const file of changes.newImages) {
    try {
      await api.uploadIssueImage(identifier, file);
      result.uploaded += 1;
    } catch (error) {
      result.failures.push(
        `upload ${file.name}: ${error instanceof Error ? error.message : "request failed"}`,
      );
    }
  }

  for (const imageID of changes.removeImageIDs) {
    try {
      await api.deleteIssueImage(identifier, imageID);
      result.removed += 1;
    } catch (error) {
      result.failures.push(
        `remove ${imageID}: ${error instanceof Error ? error.message : "request failed"}`,
      );
    }
  }

  return result;
}

export function formatIssueImageSize(byteSize: number): string {
  if (byteSize >= 1024 * 1024) {
    return `${(byteSize / (1024 * 1024)).toFixed(1)} MiB`;
  }
  if (byteSize >= 1024) {
    return `${Math.round(byteSize / 1024)} KiB`;
  }
  return `${byteSize} B`;
}

export function summarizeIssueImageFailures(
  result: IssueImageSyncResult,
): string {
  if (result.failures.length === 0) {
    return "";
  }
  if (result.failures.length === 1) {
    return result.failures[0];
  }
  return `${result.failures.length} image operations failed. First error: ${result.failures[0]}`;
}

export function issueImageContentURL(
  identifier: string,
  imageID: string,
): string {
  return `/api/v1/app/issues/${encodeURIComponent(identifier)}/images/${encodeURIComponent(imageID)}/content`;
}
