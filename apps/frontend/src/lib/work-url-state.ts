import { z } from "zod";

import { issueSortOptions } from "@/lib/dashboard";

export type WorkSort = (typeof issueSortOptions)[number]["value"];

export const workViewValues = ["board", "list"] as const;
export type WorkView = (typeof workViewValues)[number];

const workSortValues = issueSortOptions.map((option) => option.value) as [
  WorkSort,
  ...WorkSort[],
];

export const workSearchDefaults = {
  query: "",
  projectId: "",
  state: "",
  sort: "priority_asc",
  view: "board",
} as const satisfies {
  query: string;
  projectId: string;
  state: string;
  sort: WorkSort;
  view: WorkView;
};

const workSearchInputSchema = z.object({
  query: z.string().optional().catch(undefined),
  projectId: z.string().optional().catch(undefined),
  state: z.string().optional().catch(undefined),
  sort: z.enum(workSortValues).optional().catch(undefined),
  view: z.enum(workViewValues).optional().catch(undefined),
});

export const workSearchSchema = workSearchInputSchema.transform((search) => ({
  query: search.query ?? workSearchDefaults.query,
  projectId: search.projectId ?? workSearchDefaults.projectId,
  state: search.state ?? workSearchDefaults.state,
  sort: search.sort ?? workSearchDefaults.sort,
  view: search.view ?? workSearchDefaults.view,
}));

export type WorkSearch = z.infer<typeof workSearchSchema>;
