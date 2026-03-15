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

const markdownHeadingPattern = /^#{1,6}\s+(.+)$/gmu;

function normalizeSearchText(value: string) {
  return value
    .replace(/[`*_#[\]()<>{}|]/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();
}

export function extractSearchTextFromBody(body: string) {
  return Array.from(body.matchAll(markdownHeadingPattern), ([, heading]) => normalizeSearchText(heading))
    .filter(Boolean)
    .join(" ");
}

export function getDocSlug(entry: DocsEntry) {
  return entry.id.replace(/\.(md|mdx)$/u, "");
}

export const sectionMeta: Record<DocsSectionKey, { title: string; description: string }> = {
  "getting-started": {
    title: "Getting Started",
    description: "Install Maestro, start a local loop, and make the first handoff.",
  },
  concepts: {
    title: "Core Concepts",
    description: "Understand how Maestro keeps work moving and how to step back in without losing context.",
  },
  reference: {
    title: "Reference",
    description: "Look up the commands, APIs, and operations details that help you supervise or debug the loop.",
  },
  advanced: {
    title: "Advanced",
    description: "Use deterministic end-to-end harnesses when you need to verify the full handoff and execution path.",
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
    ...staticSearchEntries.map((entry) => ({
      ...entry,
      searchText: [entry.title, entry.description, entry.section].join("\n"),
    })),
    ...sortDocs(entries).map((entry) => ({
      title: entry.data.title,
      href: getDocHref(entry),
      description: entry.data.description,
      section: sectionMeta[entry.data.section].title,
      searchText: [
        entry.data.title,
        entry.data.navLabel,
        entry.data.description,
        sectionMeta[entry.data.section].title,
        extractSearchTextFromBody(entry.body ?? ""),
      ]
        .filter(Boolean)
        .join("\n"),
    })),
  ];
}
