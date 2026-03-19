import * as matchers from "@testing-library/jest-dom/matchers";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, expect, vi } from "vitest";

expect.extend(matchers);

type ConsoleCall = {
  level: "error" | "warn";
  args: unknown[];
};

let unexpectedConsoleCalls: ConsoleCall[] = [];

function formatConsoleArg(arg: unknown) {
  if (typeof arg === "string") {
    return arg;
  }
  try {
    return JSON.stringify(arg);
  } catch {
    return String(arg);
  }
}

beforeEach(() => {
  unexpectedConsoleCalls = [];

  vi.spyOn(console, "error").mockImplementation((...args) => {
    unexpectedConsoleCalls.push({ level: "error", args });
  });
  vi.spyOn(console, "warn").mockImplementation((...args) => {
    unexpectedConsoleCalls.push({ level: "warn", args });
  });
});

afterEach(() => {
  cleanup();
  const calls = unexpectedConsoleCalls.slice();
  unexpectedConsoleCalls = [];
  vi.restoreAllMocks();

  if (calls.length === 0) {
    return;
  }

  throw new Error(
    `Unexpected console output:\n${calls
      .map(({ level, args }) => `[console.${level}] ${args.map(formatConsoleArg).join(" ")}`)
      .join("\n")}`,
  );
});

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});
