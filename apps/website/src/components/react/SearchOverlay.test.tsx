import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import SearchOverlay from "./SearchOverlay";

describe("SearchOverlay", () => {
  it("opens from the keyboard and filters results", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Install Maestro",
            href: "/docs/install",
            description: "Install from npm or build from source.",
            section: "Getting Started",
          },
          {
            title: "Operations and observability",
            href: "/docs/operations",
            description: "Use runtime endpoints and logs.",
            section: "Reference",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    const input = await screen.findByRole("searchbox", { name: "Search docs" });
    fireEvent.change(input, { target: { value: "oper" } });

    await waitFor(() => expect(screen.getByText("Operations and observability")).toBeInTheDocument());
    expect(screen.queryByText("Install Maestro")).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("searchbox", { name: "Search docs" })).not.toBeInTheDocument());
  });

  it("opens when a trigger button is clicked", async () => {
    render(
      <>
        <button data-search-trigger type="button">
          Open search
        </button>
        <SearchOverlay
          entries={[
            {
              title: "Quickstart",
              href: "/docs/quickstart",
              description: "Start the daemon and open the control center.",
              section: "Getting Started",
            },
          ]}
        />
      </>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Open search" }));

    await screen.findByRole("searchbox", { name: "Search docs" });
  });

  it("keeps the search badge hidden on mobile widths", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Quickstart",
            href: "/docs/quickstart",
            description: "Start the daemon and open the control center.",
            section: "Getting Started",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    await screen.findByRole("searchbox", { name: "Search docs" });
    expect(screen.getByText("Search")).toHaveClass("hidden", "sm:inline-flex");
  });

  it("resets the highlighted result when the query changes", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Install Maestro",
            href: "/docs/install",
            description: "Install from npm or build from source.",
            section: "Getting Started",
          },
          {
            title: "Operations and observability",
            href: "/docs/operations",
            description: "Use runtime endpoints and logs.",
            section: "Reference",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    const input = await screen.findByRole("searchbox", { name: "Search docs" });
    fireEvent.keyDown(window, { key: "ArrowDown" });
    fireEvent.change(input, { target: { value: "oper" } });

    await waitFor(() => expect(screen.getByText("Operations and observability")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /Operations and observability/i })).toHaveClass(
      "bg-[rgba(196,255,87,0.1)]",
    );
  });

  it("prefers exact title matches over incidental description matches", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Quickstart",
            href: "/docs/quickstart",
            description: "Start the daemon and open the control center.",
            section: "Getting Started",
          },
          {
            title: "Control center",
            href: "/docs/control-center",
            description: "Read the embedded dashboard as the live supervision surface.",
            section: "Core Concepts",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    const input = await screen.findByRole("searchbox", { name: "Search docs" });
    fireEvent.change(input, { target: { value: "control center" } });

    await waitFor(() => expect(screen.getByText("Control center")).toBeInTheDocument());

    const resultButtons = screen.getAllByRole("button");
    expect(resultButtons[0]).toHaveTextContent("Control center");
    expect(resultButtons[1]).toHaveTextContent("Quickstart");
  });

  it("matches hidden search text from doc headings", async () => {
    render(
      <SearchOverlay
        entries={[
          {
            title: "Control center",
            href: "/docs/control-center",
            description: "Read the embedded dashboard as the live supervision surface.",
            section: "Core Concepts",
            searchText: "Control center Work board Issue detail Sessions",
          },
        ]}
      />,
    );

    fireEvent.keyDown(window, { ctrlKey: true, key: "k" });

    const input = await screen.findByRole("searchbox", { name: "Search docs" });
    fireEvent.change(input, { target: { value: "work board" } });

    await waitFor(() => expect(screen.getByText("Control center")).toBeInTheDocument());
  });
});
