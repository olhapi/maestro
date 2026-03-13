import React from "react";
import { renderToString } from "react-dom/server";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import CommandBlock from "./CommandBlock";

describe("CommandBlock", () => {
  it("copies the command and shows copied feedback", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, {
      clipboard: {
        writeText,
      },
    });

    render(<CommandBlock command="maestro workflow init ." detail="Bootstrap a workflow file." title="Bootstrap" />);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("maestro workflow init ."));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("renders the copy action as a compact icon button with pointer cursor", async () => {
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });

    render(<CommandBlock command="maestro workflow init ." detail="Bootstrap a workflow file." title="Bootstrap" />);

    await waitFor(() => expect(screen.getByRole("button", { name: "Copy" })).toHaveClass("cursor-pointer"));
  });

  it("disables the copy button when clipboard access is unavailable", () => {
    Object.assign(navigator, {
      clipboard: undefined,
    });

    render(<CommandBlock command="maestro workflow init ." detail="Bootstrap a workflow file." title="Bootstrap" />);

    return waitFor(() => expect(screen.getByRole("button", { name: "Manual copy" })).toBeDisabled());
  });

  it("renders a stable copy label during server rendering", () => {
    const originalNavigator = globalThis.navigator;

    try {
      Object.defineProperty(globalThis, "navigator", {
        configurable: true,
        value: undefined,
      });

      const html = renderToString(
        <CommandBlock command="maestro workflow init ." detail="Bootstrap a workflow file." title="Bootstrap" />,
      );

      expect(html).toContain(">Copy<");
      expect(html).not.toContain("Manual copy");
    } finally {
      Object.defineProperty(globalThis, "navigator", {
        configurable: true,
        value: originalNavigator,
      });
    }
  });
});
