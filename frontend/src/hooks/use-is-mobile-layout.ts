import { useSyncExternalStore } from "react";

export const MOBILE_LAYOUT_MAX_WIDTH = 1023;

function subscribe(onStoreChange: () => void) {
  if (typeof window === "undefined") {
    return () => {};
  }

  window.addEventListener("resize", onStoreChange);
  window.addEventListener("orientationchange", onStoreChange);

  return () => {
    window.removeEventListener("resize", onStoreChange);
    window.removeEventListener("orientationchange", onStoreChange);
  };
}

function getSnapshot() {
  if (typeof window === "undefined") {
    return false;
  }

  return window.innerWidth <= MOBILE_LAYOUT_MAX_WIDTH;
}

export function useIsMobileLayout() {
  return useSyncExternalStore(subscribe, getSnapshot, () => false);
}
