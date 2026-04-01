import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatRelativeTime(value?: string | null) {
  if (!value) return 'n/a'
  const date = new Date(value)
  const seconds = Math.round((date.getTime() - Date.now()) / 1000)
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['day', 86_400],
    ['hour', 3_600],
    ['minute', 60],
    ['second', 1],
  ]
  for (const [unit, amount] of units) {
    if (Math.abs(seconds) >= amount || unit === 'second') {
      return rtf.format(Math.round(seconds / amount), unit)
    }
  }
  return 'just now'
}

export function formatRelativeTimeCompact(value?: string | null, referenceTimeMs = Date.now()) {
  if (!value) return 'n/a'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'n/a'

  const seconds = Math.round((referenceTimeMs - date.getTime()) / 1000)
  const absolute = Math.abs(seconds)
  const units: Array<[suffix: string, size: number]> = [
    ['d', 86_400],
    ['h', 3_600],
    ['m', 60],
    ['s', 1],
  ]

  for (const [suffix, size] of units) {
    if (absolute >= size || suffix === 's') {
      const amount = Math.max(0, Math.round(absolute / size))
      const label = `${amount}${suffix}`
      return seconds >= 0 ? `${label} ago` : `in ${label}`
    }
  }
  return '0s ago'
}

export function formatNumber(value: number | undefined) {
  return new Intl.NumberFormat('en-US').format(value ?? 0)
}

export function formatCompactNumber(value: number | undefined) {
  return new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value ?? 0)
}

export function toTitleCase(value: string) {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (char) => char.toUpperCase())
}

export function formatDateTime(value?: string | null) {
  if (!value) return 'n/a'
  return new Intl.DateTimeFormat('en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}
