import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import react from "@astrojs/react";
import sitemap from "@astrojs/sitemap";
import mermaid from "astro-mermaid";
import tailwindcss from "@tailwindcss/vite";

import { siteOrigin } from "./src/site.config";

type AstroVitePlugins = NonNullable<NonNullable<Parameters<typeof defineConfig>[0]["vite"]>["plugins"]>;

const tailwindPlugins = tailwindcss() as unknown as AstroVitePlugins;

export default defineConfig({
  site: siteOrigin,
  output: "static",
  integrations: [
    mermaid({
      theme: "base",
      autoTheme: false,
      mermaidConfig: {
        securityLevel: "loose",
        htmlLabels: true,
        flowchart: {
          curve: "linear",
        },
        themeVariables: {
          darkMode: true,
          background: "#08090c",
          fontFamily: "\"IBM Plex Sans\", ui-sans-serif, system-ui, sans-serif",
          primaryColor: "#101114",
          primaryTextColor: "#ffffff",
          primaryBorderColor: "#c4ff57",
          secondaryColor: "#101114",
          secondaryTextColor: "#ffffff",
          secondaryBorderColor: "#53d9ff",
          tertiaryColor: "#121418",
          tertiaryTextColor: "#ffffff",
          tertiaryBorderColor: "#404651",
          mainBkg: "#101114",
          secondBkg: "#121418",
          tertiaryBkg: "#08090c",
          lineColor: "#6b7485",
          defaultLinkColor: "#9aa2b3",
          textColor: "#ffffff",
          nodeTextColor: "#ffffff",
          titleColor: "#ffffff",
          clusterBkg: "#0e1014",
          clusterBorder: "#404651",
          edgeLabelBackground: "#08090c",
        },
      },
    }),
    mdx(),
    react(),
    sitemap(),
  ],
  vite: {
    plugins: tailwindPlugins,
  },
});
