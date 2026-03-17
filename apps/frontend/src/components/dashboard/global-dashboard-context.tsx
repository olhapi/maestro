import { useState, type ReactNode } from "react";

import {
  defaultWorkOverviewFilters,
  GlobalDashboardContext,
  type WorkOverviewFilters,
} from "@/lib/global-dashboard-context";

export function GlobalDashboardProvider({ children }: { children: ReactNode }) {
  const [workOverviewFilters, setWorkOverviewFilters] = useState<WorkOverviewFilters>(defaultWorkOverviewFilters);

  return (
    <GlobalDashboardContext.Provider value={{ workOverviewFilters, setWorkOverviewFilters }}>
      {children}
    </GlobalDashboardContext.Provider>
  );
}
