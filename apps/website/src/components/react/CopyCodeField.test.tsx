import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import CopyCodeField from "./CopyCodeField";

const originalClipboard = navigator.clipboard;

describe("CopyCodeField", () => {
  afterEach(() => {
    Object.assign(navigator, {
      clipboard: originalClipboard,
    });
  });

  it("copies the command and shows the copied label", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, {
      clipboard: {
        writeText,
      },
    });

    render(<CopyCodeField command="maestro workflow init ." />);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("maestro workflow init ."));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("disables copying when clipboard access is unavailable", async () => {
    Object.assign(navigator, {
      clipboard: undefined,
    });

    render(<CopyCodeField command="maestro workflow init ." />);

    await waitFor(() => expect(screen.getByRole("button", { name: "Manual copy" })).toBeDisabled());
  });

  it("renders the dense variant with compact sizing classes", async () => {
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });

    render(<CopyCodeField command="maestro workflow init ." dense className="mt-4" />);

    await waitFor(() => expect(screen.getByRole("button", { name: "Copy" })).toHaveClass("size-7"));
    expect(screen.getByText("maestro workflow init .").closest("pre")).toHaveClass("font-mono");
    expect(screen.getByRole("button", { name: "Copy" }).parentElement).toHaveClass("relative", "mt-4");
  });
});
