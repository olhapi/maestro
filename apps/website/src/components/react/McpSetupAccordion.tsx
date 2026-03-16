import * as Accordion from "@radix-ui/react-accordion";
import CopyCodeField from "./CopyCodeField";

const otherAgentConfig = `{
  "mcpServers": {
    "maestro": {
      "command": "maestro",
      "args": ["mcp"]
    }
  }
}`;

const sections = [
  {
    value: "codex",
    title: "Codex",
    blocks: ["codex mcp add maestro -- maestro mcp"],
  },
  {
    value: "claude",
    title: "Claude Code",
    blocks: ["claude mcp add maestro -- maestro mcp"],
  },
  {
    value: "other",
    title: "Other coding agents",
    blocks: [otherAgentConfig],
  },
] as const;

function TriggerChevron() {
  return (
    <svg
      aria-hidden="true"
      className="size-4 shrink-0 text-[var(--muted)] transition-transform duration-200 group-data-[state=open]:rotate-180 group-data-[state=open]:text-[var(--accent-strong)]"
      fill="none"
      viewBox="0 0 16 16"
    >
      <path
        d="m4 6 4 4 4-4"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
      />
    </svg>
  );
}

export default function McpSetupAccordion() {
  return (
    <div className="not-prose">
      <Accordion.Root
        className="panel overflow-hidden"
        collapsible
        defaultValue="codex"
        type="single"
      >
        {sections.map((section) => (
          <Accordion.Item
            key={section.value}
            className="border-b border-white/8 last:border-b-0"
            value={section.value}
          >
            <Accordion.Header className="m-0!">
              <Accordion.Trigger className="group flex w-full cursor-pointer items-center justify-between gap-3 px-4 py-2 text-left transition hover:bg-white/3 md:px-5">
                <span className="block font-display text-[1.02rem] leading-tight font-semibold text-white md:text-[1.15rem]">
                  {section.title}
                </span>
                <TriggerChevron />
              </Accordion.Trigger>
            </Accordion.Header>
            <Accordion.Content className="overflow-hidden">
              <div className="border-t border-white/8 px-3 py-2 md:px-4">
                {section.blocks.map((block, index) => (
                  <div key={`${section.value}-${index}`}>
                    <CopyCodeField command={block} dense />
                  </div>
                ))}
              </div>
            </Accordion.Content>
          </Accordion.Item>
        ))}
      </Accordion.Root>
      <p className="mt-2.5 text-sm leading-6 text-[var(--muted)]">
        If you built Maestro from source and did not add it to your{" "}
        <code>PATH</code>, replace <code>maestro</code> with the absolute path
        to the binary. Start <code>maestro run</code> first, then let your
        coding agent invoke <code>maestro mcp</code> against the same database.
      </p>
    </div>
  );
}
