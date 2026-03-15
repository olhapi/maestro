import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import react from "@astrojs/react";
import sitemap from "@astrojs/sitemap";
import tailwindcss from "@tailwindcss/vite";

import { siteOrigin } from "./src/site.config";

type AstroVitePlugins = NonNullable<NonNullable<Parameters<typeof defineConfig>[0]["vite"]>["plugins"]>;

const tailwindPlugins = tailwindcss() as unknown as AstroVitePlugins;

export default defineConfig({
  site: siteOrigin,
  output: "static",
  integrations: [
    mdx(),
    react(),
    sitemap(),
  ],
  vite: {
    plugins: tailwindPlugins,
  },
});
