import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { GlobalInterruptPanel } from "@/components/dashboard/global-interrupt-panel";

describe("GlobalInterruptPanel", () => {
  it("auto-submits approval interrupts without rendering a submit button", async () => {
    const onRespond = vi.fn();

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
          id: "interrupt-approval",
          kind: "approval",
          issue_identifier: "ISS-1",
          issue_title: "Review migrations",
          phase: "review",
          attempt: 1,
          requested_at: "2026-03-16T10:00:00Z",
          approval: {
            decisions: [
              {
                value: "approved_once",
                label: "Approve once",
                description: "Run the tool and continue.",
              },
            ],
          },
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    );

    expect(screen.queryByRole("button", { name: /submit response/i })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /approve once/i }));

    expect(onRespond).toHaveBeenCalledWith({ decision: "approved_once" });
  });

  it("auto-submits option-only user input without rendering a submit button", async () => {
    const onRespond = vi.fn();

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
          id: "interrupt-options",
          kind: "user_input",
          issue_identifier: "ISS-2",
          issue_title: "Choose environment",
          requested_at: "2026-03-16T10:00:00Z",
          user_input: {
            questions: [
              {
                id: "environment",
                question: "Which environment should I use?",
                options: [
                  { label: "Staging" },
                  { label: "Production" },
                ],
              },
            ],
          },
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    );

    expect(screen.queryByRole("button", { name: /submit response/i })).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Staging").closest("button")!);

    expect(onRespond).toHaveBeenCalledWith({
      answers: {
        environment: ["Staging"],
      },
    });
  });

  it("keeps the submit button when user input includes an other-answer text input", async () => {
    const onRespond = vi.fn();

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
          id: "interrupt-other",
          kind: "user_input",
          issue_identifier: "ISS-3",
          issue_title: "Choose action",
          requested_at: "2026-03-16T10:00:00Z",
          user_input: {
            questions: [
              {
                id: "action",
                question: "How should I proceed?",
                options: [
                  { label: "Use default" },
                  { label: "Skip" },
                ],
                is_other: true,
              },
            ],
          },
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    );

    const submitButton = screen.getByRole("button", { name: /submit response/i });
    expect(submitButton).toBeInTheDocument();
    expect(submitButton).toBeDisabled();

    fireEvent.click(screen.getByText("Use default").closest("button")!);

    expect(onRespond).not.toHaveBeenCalled();
    expect(submitButton).toBeEnabled();
  });
});
