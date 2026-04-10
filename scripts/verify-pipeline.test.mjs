import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const ROOT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const PACKAGE_JSON_PATH = path.join(ROOT_DIR, 'package.json')
const PACKAGE_JSON = JSON.parse(readFileSync(PACKAGE_JSON_PATH, 'utf8'))
const SCRIPTS = PACKAGE_JSON.scripts ?? {}

function assertScriptOrder(scriptName, patterns) {
  const script = SCRIPTS[scriptName]
  assert.ok(script, `missing script: ${scriptName}`)

  let previousIndex = -1
  for (const pattern of patterns) {
    const index = script.indexOf(pattern)
    assert.notEqual(index, -1, `${scriptName} is missing ${pattern}`)
    assert.ok(index > previousIndex, `${scriptName} should list ${pattern} after the previous step`)
    previousIndex = index
  }
}

test('verify runs the go suite before the web and package gates', () => {
  assertScriptOrder('verify', ['pnpm verify:go', 'pnpm verify:web', 'pnpm verify:coverage', 'pnpm verify:package'])
})

test('verify:ci excludes the Go suite', () => {
  assertScriptOrder('verify:ci', ['pnpm verify:web', 'pnpm verify:package'])

  const ci = SCRIPTS['verify:ci']
  assert.ok(ci, 'missing script: verify:ci')
  assert.ok(!ci.includes('pnpm verify:go'), 'verify:ci should not run the Go suite')
  assert.ok(!ci.includes('go test ./...'), 'verify:ci should not run go test ./...')
})

test('verify:pre-push keeps the full pre-push gate', () => {
  const prePush = SCRIPTS['verify:pre-push']
  assert.ok(prePush, 'missing script: verify:pre-push')
  assert.ok(prePush.includes('pnpm verify'), 'verify:pre-push should run the full verify suite')
  assert.ok(prePush.includes('host-package-smoke.sh'), 'verify:pre-push should keep the host smoke')
  assert.ok(prePush.includes('retry-safety-e2e.sh'), 'verify:pre-push should keep the retry harness')
})
