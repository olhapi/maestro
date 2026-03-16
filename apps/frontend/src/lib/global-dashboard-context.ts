import { createContext, useContext, type Dispatch, type SetStateAction } from "react";

export type WorkOverviewFilters = {
  issueType: string;
  projectID: string;
  sort: string;
  state: string;
};

export type GlobalDashboardContextValue = {
  setWorkOverviewFilters: Dispatch<SetStateAction<WorkOverviewFilters>>;
  workOverviewFilters: WorkOverviewFilters;
};

export const defaultWorkOverviewFilters: WorkOverviewFilters = {
  projectID: "",
  state: "",
  issueType: "",
  sort: "priority_asc",
};

export const GlobalDashboardContext = createContext<GlobalDashboardContextValue | null>(null);

export function useGlobalDashboardContext() {
  const value = useContext(GlobalDashboardContext);

  if (!value) {
    throw new Error("useGlobalDashboardContext must be used within GlobalDashboardProvider");
  }

  return value;
}
