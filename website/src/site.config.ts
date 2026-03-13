export const siteOrigin = process.env.SITE_URL ?? "https://maestro.olhapi.dev";
export const repoUrl = "https://github.com/olhapi/maestro";
export const patreonUrl = "https://patreon.com/olhapi";
export const xUrl = "https://x.com/ollhapi";

export const durableSurfaces = [
  {
    title: "Local tracker",
    description: "Keep projects, epics, and issues in a durable SQLite-backed Kanban surface without needing Linear to get started.",
  },
  {
    title: "MCP bridge",
    description: "Expose that tracker to Codex or ChatGPT through a single `maestro mcp` attach point that mirrors the live daemon.",
  },
  {
    title: "Orchestrator",
    description: "Route ready issues into workspaces, launch the agent command you configure, and keep retries, logs, and runtime state visible.",
  },
] as const;

export const quickstartSteps = [
  {
    title: "Install",
    command: "npm install -g @olhapi/maestro",
    detail: "Preferred global install for macOS arm64/x64, Linux glibc arm64/x64, and Windows x64.",
  },
  {
    title: "Bootstrap workflow",
    command: "maestro workflow init .",
    detail: "Write a repo-local WORKFLOW.md with the default Kanban + Codex settings.",
  },
  {
    title: "Start orchestration",
    command: "maestro run /path/to/repo --port 8787",
    detail: "Run the daemon, observability API, and embedded control center.",
  },
] as const;

export const docsPreview = [
  {
    title: "Install and quickstart",
    href: "/docs/quickstart",
    description: "Move from npm install to a running daemon and local dashboard in a few commands.",
  },
  {
    title: "Workflow configuration",
    href: "/docs/workflow-config",
    description: "Tune agent mode, sandboxing, retries, dispatch, and phase prompts from WORKFLOW.md.",
  },
  {
    title: "Operations and observability",
    href: "/docs/operations",
    description: "Use the HTTP runtime endpoints, validation commands, extensions file, and logs without guesswork.",
  },
] as const;

export const architectureSteps = [
  "Codex or ChatGPT attaches through `maestro mcp`.",
  "The live `maestro run` daemon owns the SQLite store, observability API, and orchestrator loop.",
  "Ready issues move into workspaces and the configured agent command executes against them.",
] as const;

export const tourChapters = [
  {
    id: "overview",
    title: "Read system pressure before you dive into a single issue.",
    description:
      "The overview surface keeps running agents, retry pressure, throughput, and board load visible so you can triage before the queue gets noisy.",
    bullets: [
      "24h throughput and retry trends stay in view.",
      "State totals make backlog pressure obvious.",
      "Live runs and pending retries are one click away.",
    ],
    image: "/images/screens/overview.svg",
  },
  {
    id: "work",
    title: "Route work without losing the shape of the queue.",
    description:
      "The board keeps priority, blockers, live sessions, and token burn visible directly on the cards so planning and active execution share one surface.",
    bullets: [
      "Drag issues between lanes or inspect them in place.",
      "Filter by project, state, and search without flattening the board.",
      "Spot blocked or live issues before they become blind spots.",
    ],
    image: "/images/screens/work.svg",
  },
  {
    id: "issue-detail",
    title: "See the execution transcript where the work actually happens.",
    description:
      "When you need context, the issue page keeps blockers, branch state, token spend, and the activity feed in one dense but readable control panel.",
    bullets: [
      "Execution state, blockers, and commands stay adjacent.",
      "The transcript shows command progress and agent messages together.",
      "Live controls stay available without leaving the issue.",
    ],
    image: "/images/screens/issue-detail.png",
  },
  {
    id: "sessions",
    title: "Follow live workspaces and retries without opening every issue.",
    description:
      "The sessions surface is built for active supervision when multiple repos or issues are moving at once.",
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
    description: "Product overview and launch quickstart.",
    section: "site",
  },
  {
    title: "Docs",
    href: "/docs",
    description: "Documentation landing page.",
    section: "site",
  },
] as const;
