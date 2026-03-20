import path from 'node:path'

import { describe, expect, it } from 'vitest'

import { scanSourceText, scanWorkspace } from '../../scripts/leak-hunt.mjs'

describe('leak-hunt scanner', () => {
  it('flags missing cleanup in a component effect', () => {
    const findings = scanSourceText(
      `
      function LeakyComponent() {
        useEffect(() => {
          window.addEventListener('resize', onResize)
          const timeoutID = setTimeout(() => {}, 500)
          const controller = new AbortController()
          return () => {
            controller.abort()
          }
        }, [])
      }
    `,
      '/tmp/leaky-component.tsx',
    )

    expect(findings.some((finding) => finding.kind === 'listener')).toBe(true)
    expect(findings.some((finding) => finding.kind === 'timeout')).toBe(true)
    expect(findings.some((finding) => finding.kind === 'abort-controller')).toBe(false)
  })

  it('treats once listeners and matching cleanup as safe', () => {
    const findings = scanSourceText(
      `
      function SafeComponent() {
        useEffect(() => {
          const handler = () => {}
          const rafID = requestAnimationFrame(() => {})
          window.addEventListener('resize', handler, { once: true })
          return () => {
            window.removeEventListener('resize', handler)
            cancelAnimationFrame(rafID)
          }
        }, [])
      }
    `,
      '/tmp/safe-component.tsx',
    )

    expect(findings).toEqual([])
  })

  it('flags leaked object URLs even when another function revokes its own URL', () => {
    const findings = scanSourceText(
      `
      function makeSafePreview(file: File) {
        const url = URL.createObjectURL(file)
        URL.revokeObjectURL(url)
      }

      function makePreview(file: File) {
        return URL.createObjectURL(file)
      }
    `,
      '/tmp/object-url.ts',
    )

    expect(findings).toHaveLength(1)
    expect(findings[0]?.kind).toBe('object-url')
    expect(findings[0]?.functionName).toBe('makePreview')
  })

  it('keeps the current frontend source tree clean', async () => {
    const report = await scanWorkspace(path.resolve(process.cwd(), 'src'))

    expect(report.findings).toEqual([])
  })
})
