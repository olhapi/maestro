import React from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import TourScroller from "./TourScroller";

let reducedMotion = false;

vi.mock("motion/react", async () => {
  const actual = await vi.importActual<typeof import("motion/react")>("motion/react");

  return {
    ...actual,
    useReducedMotion: () => reducedMotion,
  };
});

type ObserverRecord = {
  callback: IntersectionObserverCallback;
  disconnect: ReturnType<typeof vi.fn>;
  observe: ReturnType<typeof vi.fn>;
};

const observers: ObserverRecord[] = [];

class MockIntersectionObserver {
  callback: IntersectionObserverCallback;
  disconnect = vi.fn();
  observe = vi.fn();

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback;
    observers.push(this);
  }
}

const chapters = [
  {
    id: "overview",
    eyebrow: "Chapter 1",
    title: "Overview",
    description: "See the queue health.",
    bullets: ["Watch retries", "Check live state"],
    image: "/images/screens/overview.svg",
  },
  {
    id: "sessions",
    eyebrow: "Chapter 2",
    title: "Sessions",
    description: "Review the active runs.",
    bullets: ["Track parallel work", "Inspect stalls"],
    image: "/images/screens/sessions.svg",
  },
] as const;

const chaptersWithoutEyebrows = [
  {
    id: "overview",
    title: "Overview",
    description: "See the queue health.",
    bullets: ["Watch retries", "Check live state"],
    image: "/images/screens/overview.svg",
  },
  {
    id: "sessions",
    title: "Sessions",
    description: "Review the active runs.",
    bullets: ["Track parallel work", "Inspect stalls"],
    image: "/images/screens/sessions.svg",
  },
] as const;

describe("TourScroller", () => {
  beforeEach(() => {
    reducedMotion = false;
    observers.length = 0;
    vi.stubGlobal("IntersectionObserver", MockIntersectionObserver as unknown as typeof IntersectionObserver);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the home layout with reduced motion enabled", () => {
    reducedMotion = true;

    render(<TourScroller chapters={chapters} mode="home" />);

    expect(document.querySelector('[class*="min-h-[28rem]"]')).not.toBeNull();
    expect(screen.getAllByAltText("Overview")).toHaveLength(2);
    expect(screen.getByText("1 / 2")).toBeInTheDocument();
  });

  it("advances the desktop chapter when a section becomes visible", async () => {
    render(<TourScroller chapters={chapters} mode="tour" />);

    await waitFor(() => expect(observers).toHaveLength(chapters.length));

    act(() => {
      observers[1].callback([{ isIntersecting: true } as IntersectionObserverEntry], observers[1] as never);
    });

    await waitFor(() => expect(screen.getByText("2 / 2")).toBeInTheDocument());
    expect(screen.getAllByAltText("Sessions")).toHaveLength(2);
    expect(document.querySelector('[class*="min-h-[34rem]"]')).not.toBeNull();
  });

  it("ignores non-intersecting sections and disconnects observers on unmount", async () => {
    const { unmount } = render(<TourScroller chapters={chaptersWithoutEyebrows} mode="tour" />);

    await waitFor(() => expect(observers).toHaveLength(chaptersWithoutEyebrows.length));

    act(() => {
      observers[0].callback([{ isIntersecting: false } as IntersectionObserverEntry], observers[0] as never);
    });

    expect(screen.getByText("1 / 2")).toBeInTheDocument();
    expect(screen.queryByText("Chapter 1")).not.toBeInTheDocument();

    unmount();

    expect(observers[0].disconnect).toHaveBeenCalled();
    expect(observers[1].disconnect).toHaveBeenCalled();
  });
});
