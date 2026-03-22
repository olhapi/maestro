export const siteOrigin = process.env.SITE_URL ?? "https://maestro.olhapi.dev";
export const buyMeACoffeeUrl = "https://buymeacoffee.com/olhapi";
export const repoUrl = "https://github.com/olhapi/maestro";
export const patreonUrl = "https://patreon.com/olhapi";
export const xUrl = "https://x.com/ollhapi";

export const durableSurfaces = [
  {
    title: "Shared local state",
    description:
      "Keep projects, blockers, screenshots, and run history in one local tracker so you can leave the loop and return without rebuilding context.",
  },
  {
    title: "Live MCP handoff",
    description:
      "Expose that same queue through `maestro mcp` so your agent sees the work and state the daemon is actually supervising.",
  },
  {
    title: "Visible runtime",
    description:
      "Let Maestro keep routing ready issues while you watch retries, sessions, logs, and live state from the control center.",
  },
] as const;

export const quickstartSteps = [
  {
    title: "Install",
    command: "npm install -g @olhapi/maestro",
    detail:
      "Install the local runtime once, then use the same command in every repo you want to supervise.",
  },
  {
    title: "Bootstrap workflow",
    command: "maestro workflow init .",
    detail:
      "Setup repo contract once so Maestro can keep making the same handoff decisions while you are elsewhere.",
  },
  {
    title: "Start orchestration",
    command: "maestro run",
    detail:
      "Start the daemon and embedded control center so the queue keeps moving while status, retries, and sessions stay visible.",
  },
] as const;

export const docsPreview = [
  {
    title: "Install and quickstart",
    href: "/docs/quickstart",
    description:
      "Get from install to a live local loop, then learn what keeps running after the initial handoff.",
  },
  {
    title: "Workflow configuration",
    href: "/docs/workflow-config",
    description:
      "Tune how much autonomy Maestro gets, how it retries, and which guardrails stay in place.",
  },
  {
    title: "Operations and observability",
    href: "/docs/operations",
    description:
      "Check queue health, inspect live runs, and debug the daemon without guessing what happened.",
  },
] as const;

export const tourChapters = [
  {
    id: "overview",
    title: "See whether the loop is healthy before you interrupt it",
    description:
      "Overview keeps running agents, retries, live token load, execution health, and token burn visible so you can decide whether to step in or let the queue keep moving.",
    bullets: [
      "Retry trends tell you whether the loop is drifting.",
      "State totals make backlog pressure obvious.",
      "Execution health and token burn are split into separate 24h views.",
      "Live runs and pending retries are one click away.",
    ],
    image: "/images/screens/overview.svg",
  },
  {
    id: "work",
    title: "Adjust the queue without stopping the rest of the work",
    description:
      "The board keeps blockers, live sessions, token burn, and the shared composer together so you can reroute work and get back out quickly.",
    bullets: [
      "Drag issues between lanes or inspect them in place.",
      "Open the shared composer and dictate issue descriptions with browser speech input.",
      "Filter by project, state, and search without flattening the board.",
      "Spot blocked or live issues before they become blind spots.",
    ],
    image: "/images/screens/work.svg",
  },
  {
    id: "issue-detail",
    title: "Step back into one issue with the full trail intact",
    description:
      "When one run needs attention, the issue page keeps transcript, blockers, screenshots, branch state, and commands in one dense control panel.",
    bullets: [
      "Execution state, screenshots, blockers, and follow-up commands stay adjacent.",
      "The transcript shows command progress and agent messages together.",
      "Attach or remove local images without leaving the issue.",
    ],
    image: "/images/screens/issue-detail.webp",
  },
  {
    id: "sessions",
    title: "Watch parallel work without opening every issue",
    description:
      "Sessions is the fast path when multiple repos or issues are moving and you only want to intervene where the loop is actually stuck.",
    bullets: [
      "See session identifiers, runtime churn, and last activity.",
      "Track retries and investigate stalled runs quickly.",
      "Keep the control-plane view even when agents are mid-flight.",
    ],
    image: "/images/screens/sessions.svg",
  },
] as const;

export const staticSearchEntries = [
  {
    title: "Home",
    href: "/",
    description: "Product overview for handing work off and checking back in.",
    section: "site",
  },
  {
    title: "Docs",
    href: "/docs",
    description: "Docs for starting, supervising, and tuning the loop.",
    section: "site",
  },
] as const;
