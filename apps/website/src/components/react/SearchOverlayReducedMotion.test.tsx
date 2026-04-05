import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("motion/react", async () => {
  const actual = await vi.importActual<typeof import("motion/react")>("motion/react");

  return {
    ...actual,
    useReducedMotion: () => true,
  };
});

import SearchOverlay from "./SearchOverlay";

describe("SearchOverlay reduced motion", () => {
  it("opens the dialog with the reduced motion branch", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Install Maestro",
            href: "/docs/install",
            description: "Install from npm or build from source.",
            section: "Getting Started",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
});
