function normalizeFailureClass(value?: string | null): string {
  return value?.trim().toLowerCase() ?? ""
}

function failureRunAdjective(value?: string | null): string {
  switch (normalizeFailureClass(value)) {
    case "stall_timeout":
      return "stalled"
    case "turn_timeout":
      return "timed out"
    case "read_timeout":
      return "read-timed-out"
    default:
      return "interrupted"
  }
}

export function describeFailureRuns(count: number | undefined, value?: string | null): string {
  const total = count ?? 0
  return `${total} ${failureRunAdjective(value)} ${total === 1 ? "run" : "runs"}`
}

export function failureStatusLabel(value?: string | null): string | null {
  switch (normalizeFailureClass(value)) {
    case "stall_timeout":
      return "Stalled"
    case "turn_timeout":
      return "Timed out"
    case "read_timeout":
      return "Read timed out"
    case "run_interrupted":
      return "Interrupted"
    default:
      return null
  }
}

export function failureHeadline(value?: string | null): string | null {
  switch (normalizeFailureClass(value)) {
    case "stall_timeout":
      return "Last run stalled"
    case "turn_timeout":
      return "Last run timed out"
    case "read_timeout":
      return "Last run hit a read timeout"
    case "run_interrupted":
      return "Last run interrupted"
    default:
      return null
  }
}

export function failureMessage(value?: string | null): string | null {
  switch (normalizeFailureClass(value)) {
    case "stall_timeout":
      return "The last known execution stopped producing progress before completion."
    case "turn_timeout":
      return "The last known execution exceeded the turn time budget before completion."
    case "read_timeout":
      return "The last known execution stopped returning readable output before completion."
    case "run_interrupted":
      return "The last known execution ended without a live completion signal."
    default:
      return null
  }
}
