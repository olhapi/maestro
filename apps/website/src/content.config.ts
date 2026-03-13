import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { z } from "astro/zod";

const docs = defineCollection({
  loader: glob({ pattern: "**/*.{md,mdx}", base: "./src/content/docs" }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    section: z.enum(["getting-started", "concepts", "reference", "advanced"]),
    order: z.number().int().nonnegative(),
    navLabel: z.string().optional(),
    draft: z.boolean().optional(),
  }),
});

export const collections = { docs };
