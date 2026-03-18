import { api } from "@/lib/api";

export interface IssueAssetChangeSet {
  newAssets: File[];
  removeAssetIDs: string[];
}

export interface IssueAssetSyncResult {
  failures: string[];
  removed: number;
  uploaded: number;
}

export const issueAssetInputAccept = "*/*";

export function hasIssueAssetChanges(changes: IssueAssetChangeSet): boolean {
  return changes.newAssets.length > 0 || changes.removeAssetIDs.length > 0;
}

export async function applyIssueAssetChanges(
  identifier: string,
  changes: IssueAssetChangeSet,
): Promise<IssueAssetSyncResult> {
  const result: IssueAssetSyncResult = {
    failures: [],
    removed: 0,
    uploaded: 0,
  };

  for (const file of changes.newAssets) {
    try {
      await api.uploadIssueAsset(identifier, file);
      result.uploaded += 1;
    } catch (error) {
      result.failures.push(
        `upload ${file.name}: ${error instanceof Error ? error.message : "request failed"}`,
      );
    }
  }

  for (const assetID of changes.removeAssetIDs) {
    try {
      await api.deleteIssueAsset(identifier, assetID);
      result.removed += 1;
    } catch (error) {
      result.failures.push(
        `remove ${assetID}: ${error instanceof Error ? error.message : "request failed"}`,
      );
    }
  }

  return result;
}

export function formatIssueAssetSize(byteSize: number): string {
  if (byteSize >= 1024 * 1024) {
    return `${(byteSize / (1024 * 1024)).toFixed(1)} MiB`;
  }
  if (byteSize >= 1024) {
    return `${Math.round(byteSize / 1024)} KiB`;
  }
  return `${byteSize} B`;
}

export function summarizeIssueAssetFailures(
  result: IssueAssetSyncResult,
): string {
  if (result.failures.length === 0) {
    return "";
  }
  if (result.failures.length === 1) {
    return result.failures[0];
  }
  return `${result.failures.length} asset operations failed. First error: ${result.failures[0]}`;
}

export function issueAssetContentURL(
  identifier: string,
  assetID: string,
): string {
  return `/api/v1/app/issues/${encodeURIComponent(identifier)}/assets/${encodeURIComponent(assetID)}/content`;
}

export function isIssueAssetImage(contentType: string | undefined): boolean {
  return (contentType ?? "").startsWith("image/");
}
