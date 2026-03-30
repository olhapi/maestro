import React from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

let reducedMotion = false;

vi.mock("motion/react", async () => {
  const actual = await vi.importActual<typeof import("motion/react")>("motion/react");

  return {
    ...actual,
    useReducedMotion: () => reducedMotion,
  };
});

import HeroShowcase from "./HeroShowcase";

describe("HeroShowcase", () => {
  it("renders the showcase image with the default motion settings", () => {
    reducedMotion = false;

    render(<HeroShowcase />);

    expect(
      screen.getByRole("img", {
        name: /maestro work board control center view with the shared issue composer open/i,
      }),
    ).toHaveAttribute("src", "/images/screens/work-control-center.webp");
    expect(screen.getByText("Live control center")).toBeInTheDocument();
  });

  it("renders the showcase image when reduced motion is requested", () => {
    reducedMotion = true;

    render(<HeroShowcase />);

    expect(
      screen.getByRole("img", {
        name: /maestro work board control center view with the shared issue composer open/i,
      }),
    ).toBeInTheDocument();
  });
});
