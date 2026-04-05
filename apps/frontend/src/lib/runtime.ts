import { toTitleCase } from '@/lib/utils'

function humanizeRuntimeToken(value?: string) {
  const trimmed = value?.trim()
  if (!trimmed) {
    return ''
  }
  return toTitleCase(trimmed.replace(/[_.-]+/g, ' '))
}

export function formatRuntimeName(value?: string) {
  return value?.trim() ?? ''
}

export function formatRuntimeProvider(value?: string) {
  return humanizeRuntimeToken(value)
}

export function formatRuntimeTransport(value?: string) {
  const trimmed = value?.trim()
  if (!trimmed) {
    return ''
  }
  if (trimmed === 'stdio') {
    return 'stdio'
  }
  return humanizeRuntimeToken(trimmed)
}

export function formatRuntimeAuthSource(value?: string) {
  const trimmed = value?.trim()
  if (!trimmed) {
    return ''
  }
  if (trimmed === 'OAuth') {
    return 'OAuth'
  }
  return humanizeRuntimeToken(trimmed)
}

export function formatRuntimeState(value?: string) {
  return humanizeRuntimeToken(value)
}

export function formatRuntimeStopReason(value?: string) {
  return humanizeRuntimeToken(value)
}
