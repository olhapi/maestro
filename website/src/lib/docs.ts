import type { CollectionEntry } from "astro:content";

import { staticSearchEntries } from "../site.config";

export const sectionOrder = ["getting-started", "concepts", "reference", "advanced"] as const;

export type DocsSectionKey = (typeof sectionOrder)[number];
export type DocsEntry = CollectionEntry<"docs">;
export type DocsGroup = {
  key: DocsSectionKey;
  title: string;
  description: string;
  items: DocsEntry[];
};

export function getDocSlug(entry: DocsEntry) {
  return entry.id.replace(/\.(md|mdx)$/u, "");
}

export const sectionMeta: Record<DocsSectionKey, { title: string; description: string }> = {
  "getting-started": {
    title: "Getting Started",
    description: "Install Maestro, bootstrap a workflow, and get the control center running.",
  },
  concepts: {
    title: "Core Concepts",
    description: "Understand the runtime model, dashboard surface, and workflow file before you tune it.",
  },
  reference: {
    title: "Reference",
    description: "Reach for commands, observability endpoints, extensions, and operational behavior quickly.",
  },
  advanced: {
    title: "Advanced",
    description: "Use the real Codex end-to-end harness when you need a deterministic full-loop verification.",
  },
};

export function getDocHref(entry: DocsEntry) {
  return `/docs/${getDocSlug(entry)}`;
}

export function sortDocs(entries: DocsEntry[]) {
  return [...entries]
    .filter((entry) => !entry.data.draft)
    .sort((left, right) => {
      const leftSection = sectionOrder.indexOf(left.data.section);
      const rightSection = sectionOrder.indexOf(right.data.section);
      if (leftSection !== rightSection) {
        return leftSection - rightSection;
      }
      if (left.data.order !== right.data.order) {
        return left.data.order - right.data.order;
      }
      return left.data.title.localeCompare(right.data.title);
    });
}

export function groupDocs(entries: DocsEntry[]) {
  const sorted = sortDocs(entries);
  return sectionOrder.map((section) => ({
    key: section,
    ...sectionMeta[section],
    items: sorted.filter((entry) => entry.data.section === section),
  })) satisfies DocsGroup[];
}

export function getSearchEntries(entries: DocsEntry[]) {
  return [
    ...staticSearchEntries,
    ...sortDocs(entries).map((entry) => ({
      title: entry.data.title,
      href: getDocHref(entry),
      description: entry.data.description,
      section: sectionMeta[entry.data.section].title,
    })),
  ];
}
