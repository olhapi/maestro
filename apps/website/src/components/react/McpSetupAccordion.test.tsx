import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import McpSetupAccordion from "./McpSetupAccordion";

describe("McpSetupAccordion", () => {
  it("shows Codex setup and a manual MCP fallback", () => {
    render(<McpSetupAccordion />);

    expect(screen.getByText("codex mcp add maestro -- maestro mcp")).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: /^(Codex|Manual MCP client)$/i }),
    ).toHaveLength(2);

    fireEvent.click(screen.getByRole("button", { name: /Manual MCP client/i }));
    expect(screen.getByText(/"mcpServers"/)).toBeInTheDocument();
    expect(screen.getByText(/"command": "maestro"/)).toBeInTheDocument();
  });
});
