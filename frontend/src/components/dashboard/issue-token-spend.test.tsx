import { screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { IssueCard } from "@/components/dashboard/issue-card";
import { IssuePreviewSheet } from "@/components/dashboard/issue-preview-sheet";
import {
  makeBootstrapResponse,
  makeIssueDetail,
  makeIssueSummary,
} from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";

const navigate = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
  },
}));

vi.mock("@/lib/api", () => ({
  api: {
    getIssue: vi.fn(),
    retryIssue: vi.fn(),
    setIssueBlockers: vi.fn(),
    updateIssue: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

describe("issue token spend surfaces", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows lifetime token spend on issue cards", () => {
    const issue = makeIssueSummary({ total_tokens_spent: 55 });

    renderWithQueryClient(
      <IssueCard
        issue={issue}
        bootstrap={makeBootstrapResponse()}
        onOpen={vi.fn()}
      />,
    );

    expect(screen.getByText("55 tokens")).toBeInTheDocument();
  });

  it("shows lifetime token spend in the preview sheet execution summary", async () => {
    const bootstrap = makeBootstrapResponse();
    const summary = makeIssueSummary({ total_tokens_spent: 55 });
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({ total_tokens_spent: 55 }),
    );

    renderWithQueryClient(
      <IssuePreviewSheet
        issue={summary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={vi.fn().mockResolvedValue(undefined)}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("55 lifetime tokens")).toBeInTheDocument();
    });
  });
});
