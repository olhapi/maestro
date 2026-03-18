export function extractClipboardFiles(data: DataTransfer | null | undefined): File[] {
  if (!data) {
    return [];
  }
  const filesFromItems = Array.from(data.items ?? [])
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter((file): file is File => file instanceof File);
  if (filesFromItems.length > 0) {
    return filesFromItems;
  }
  return Array.from(data.files ?? []);
}
