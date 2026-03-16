import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import McpSetupAccordion from "./McpSetupAccordion";

describe("McpSetupAccordion", () => {
  it("shows explicit Codex and Claude Code commands plus a generic fallback", () => {
    render(<McpSetupAccordion />);

    expect(screen.getByText("codex mcp add maestro -- maestro mcp")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Claude Code/i }));
    expect(screen.getByText("claude mcp add maestro -- maestro mcp")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Other coding agents/i }));
    expect(screen.getByText(/"mcpServers"/)).toBeInTheDocument();
    expect(screen.getByText(/"command": "maestro"/)).toBeInTheDocument();
  });
});
