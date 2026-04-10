import { z } from "zod";

export const workSortOptions = [
  { value: "updated_desc", label: "Recently updated" },
  { value: "updated_asc", label: "Oldest updated" },
  { value: "priority_asc", label: "Highest priority" },
  { value: "priority_desc", label: "Lowest priority" },
  { value: "identifier_asc", label: "Identifier A-Z" },
  { value: "identifier_desc", label: "Identifier Z-A" },
  { value: "state_asc", label: "State grouping" },
  { value: "state_desc", label: "State reverse grouping" },
  { value: "project_asc", label: "Project A-Z" },
  { value: "project_desc", label: "Project Z-A" },
  { value: "epic_asc", label: "Epic A-Z" },
  { value: "epic_desc", label: "Epic Z-A" },
  { value: "none", label: "Default order" },
] as const;

export type WorkSort = (typeof workSortOptions)[number]["value"];

export const workViewValues = ["board", "list"] as const;
export type WorkView = (typeof workViewValues)[number];

export type WorkRequestSort = Exclude<WorkSort, "none">;

const workSortValues = workSortOptions.map((option) => option.value) as [
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

export function normalizeWorkSort(sort: WorkSort): WorkRequestSort {
  return sort === "none" ? workSearchDefaults.sort : sort;
}

export type WorkSearch = z.infer<typeof workSearchSchema>;
