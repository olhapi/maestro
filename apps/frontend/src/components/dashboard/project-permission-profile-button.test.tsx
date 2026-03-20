import { useState } from "react";

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import {
  ProjectPermissionProfileButton,
} from "@/components/dashboard/project-permission-profile-button";
import { projectPermissionProfileButtonCopy } from "@/lib/project-permission-profiles";
import { renderWithQueryClient } from "@/test/test-utils";
import type { PermissionProfile } from "@/lib/types";

vi.mock("@/lib/api", () => ({
  api: {
    setProjectPermissionProfile: vi.fn(),
  },
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

function Harness() {
  const [permissionProfile, setPermissionProfile] =
    useState<PermissionProfile>("default");

  return (
    <ProjectPermissionProfileButton
      projectId="project-1"
      permissionProfile={permissionProfile}
      scopeLabel="Project access"
      onSuccess={(nextProfile) => {
        setPermissionProfile(nextProfile);
      }}
    />
  );
}

describe("ProjectPermissionProfileButton", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.setProjectPermissionProfile).mockResolvedValue({} as never);
  });

  it("cycles through the three permission profiles and renders a labeled access chip", async () => {
    renderWithQueryClient(<Harness />);

    const button = () =>
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "default",
        ).ariaLabel,
      });

    expect(button()).toHaveTextContent("Default access");
    expect(button()).not.toHaveTextContent("?");
    expect(button().querySelector("svg")).not.toBeNull();

    fireEvent.focus(button());
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "Default access",
    );
    expect(screen.getByRole("tooltip")).toHaveTextContent(
      "Uses the workspace baseline agent settings until a more specific access mode is chosen.",
    );

    fireEvent.click(button());
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenNthCalledWith(
        1,
        "project-1",
        "full-access",
      );
    });
    await waitFor(() => {
      expect(screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "full-access",
        ).ariaLabel,
      })).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy("Project access", "full-access").ariaLabel,
      }),
    ).toHaveTextContent("Full access");

    fireEvent.click(
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "full-access",
        ).ariaLabel,
      }),
    );
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenNthCalledWith(
        2,
        "project-1",
        "plan-then-full-access",
      );
    });
    await waitFor(() => {
      expect(screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "plan-then-full-access",
        ).ariaLabel,
      })).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy("Project access", "plan-then-full-access").ariaLabel,
      }),
    ).toHaveTextContent("Plan, then full access");

    fireEvent.click(
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "plan-then-full-access",
        ).ariaLabel,
      }),
    );
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenNthCalledWith(
        3,
        "project-1",
        "default",
      );
    });
    await waitFor(() => {
      expect(screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          "default",
        ).ariaLabel,
      })).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy("Project access", "default").ariaLabel,
      }),
    ).toHaveTextContent("Default access");
  });
});
