#!/usr/bin/env node

import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { pathToFileURL } from 'node:url'

import * as ts from 'typescript'

const sourceFileExtensions = new Set(['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'])
const ignoredPathSegments = new Set(['node_modules', 'dist', '.turbo', '.astro', '.git'])

if (isMainModule()) {
  const args = parseArgs(process.argv.slice(2))
  const report = await scanWorkspace(args.rootPath)

  if (args.json) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`)
  } else {
    process.stdout.write(`${formatReport(report)}\n`)
  }

  if (report.findings.length > 0) {
    process.exitCode = 1
  }
}

export async function scanWorkspace(rootPath) {
  const resolvedRoot = path.resolve(rootPath)
  const stats = await fs.stat(resolvedRoot)
  const files = stats.isDirectory() ? await collectSourceFiles(resolvedRoot) : [resolvedRoot]
  const findings = []

  for (const filePath of files) {
    const sourceText = await fs.readFile(filePath, 'utf8')
    findings.push(...scanSourceText(sourceText, filePath))
  }

  findings.sort((left, right) => {
    if (left.filePath !== right.filePath) {
      return left.filePath.localeCompare(right.filePath)
    }
    if (left.line !== right.line) {
      return left.line - right.line
    }
    if (left.column !== right.column) {
      return left.column - right.column
    }
    return left.kind.localeCompare(right.kind)
  })

  return {
    findings,
    fileCount: files.length,
    rootPath: resolvedRoot,
  }
}

export function scanSourceText(sourceText, filePath = '<unknown>') {
  if (isIgnoredPath(filePath)) {
    return []
  }

  const scriptKind = getScriptKind(filePath)
  const sourceFile = ts.createSourceFile(filePath, sourceText, ts.ScriptTarget.Latest, true, scriptKind)
  const findings = []

  for (const topLevelFunction of collectTopLevelFunctions(sourceFile)) {
    findings.push(...scanFunctionNode(topLevelFunction.node, sourceFile, topLevelFunction.name))
  }

  findings.push(...scanObjectUrlUsage(sourceFile))

  return findings
}

export function formatReport(report) {
  const summary =
    report.findings.length === 0
      ? `frontend leak scan completed: no potential leaks found across ${report.fileCount} files`
      : `frontend leak scan found ${report.findings.length} potential leak${report.findings.length === 1 ? '' : 's'} across ${report.fileCount} files`

  if (report.findings.length === 0) {
    return summary
  }

  const details = report.findings.map((finding) => {
    const relativePath = path.relative(report.rootPath, finding.filePath) || path.basename(finding.filePath)
    return [
      `- ${relativePath}:${finding.line}:${finding.column} [${finding.severity}] ${finding.message}`,
      `  function: ${finding.functionName}`,
    ].join('\n')
  })

  return [summary, ...details].join('\n')
}

function scanFunctionNode(functionNode, sourceFile, functionName) {
  const findings = []
  const functionCallNames = collectCallAndConstructionNames(functionNode)

  for (const hit of collectResourceHits(functionNode)) {
    if (hit.kind === 'listener' && hasOnceListenerOption(hit.node)) {
      continue
    }

    if (hit.cleanupNames.some((cleanupName) => functionCallNames.has(cleanupName))) {
      continue
    }

    const { line, character } = sourceFile.getLineAndCharacterOfPosition(hit.node.getStart(sourceFile))
    findings.push({
      column: character + 1,
      filePath: sourceFile.fileName,
      functionName,
      kind: hit.kind,
      line: line + 1,
      message: `${functionName}: ${hit.message}`,
      severity: hit.severity,
    })
  }

  return findings
}

function collectResourceHits(rootNode) {
  const hits = []

  visit(rootNode, (current) => {
    if (ts.isCallExpression(current)) {
      const callName = getExpressionName(current.expression)
      if (!callName) {
        return
      }

      if (callName === 'addEventListener') {
        hits.push({
          cleanupNames: ['removeEventListener'],
          kind: 'listener',
          message: 'adds an event listener without cleanup',
          node: current,
          severity: 'high',
        })
        return
      }

      if (callName === 'setTimeout') {
        hits.push({
          cleanupNames: ['clearTimeout'],
          kind: 'timeout',
          message: 'starts a timeout without cleanup',
          node: current,
          severity: 'high',
        })
        return
      }

      if (callName === 'setInterval') {
        hits.push({
          cleanupNames: ['clearInterval'],
          kind: 'interval',
          message: 'starts an interval without cleanup',
          node: current,
          severity: 'high',
        })
        return
      }

      if (callName === 'requestAnimationFrame') {
        hits.push({
          cleanupNames: ['cancelAnimationFrame'],
          kind: 'raf',
          message: 'requests an animation frame without cleanup',
          node: current,
          severity: 'medium',
        })
      }

      return
    }

    if (ts.isNewExpression(current)) {
      const constructorName = getExpressionName(current.expression)
      if (!constructorName) {
        return
      }

      if (constructorName === 'WebSocket') {
        hits.push({
          cleanupNames: ['close'],
          kind: 'websocket',
          message: 'creates a WebSocket without cleanup',
          node: current,
          severity: 'high',
        })
        return
      }

      if (constructorName === 'EventSource') {
        hits.push({
          cleanupNames: ['close'],
          kind: 'eventsource',
          message: 'creates an EventSource without cleanup',
          node: current,
          severity: 'high',
        })
        return
      }

      if (constructorName === 'BroadcastChannel') {
        hits.push({
          cleanupNames: ['close'],
          kind: 'broadcast-channel',
          message: 'creates a BroadcastChannel without cleanup',
          node: current,
          severity: 'medium',
        })
        return
      }

      if (constructorName === 'ResizeObserver' || constructorName === 'MutationObserver' || constructorName === 'IntersectionObserver') {
        hits.push({
          cleanupNames: ['disconnect'],
          kind: 'observer',
          message: `creates a ${constructorName} without cleanup`,
          node: current,
          severity: 'high',
        })
        return
      }

      if (constructorName === 'AudioContext' || constructorName === 'webkitAudioContext') {
        hits.push({
          cleanupNames: ['close'],
          kind: 'audio-context',
          message: 'creates an AudioContext without cleanup',
          node: current,
          severity: 'medium',
        })
        return
      }

      if (constructorName === 'AbortController') {
        hits.push({
          cleanupNames: ['abort'],
          kind: 'abort-controller',
          message: 'creates an AbortController without cleanup',
          node: current,
          severity: 'medium',
        })
        return
      }

      if (constructorName.includes('SpeechRecognition')) {
        hits.push({
          cleanupNames: ['abort'],
          kind: 'speech-recognition',
          message: 'starts speech recognition without cleanup',
          node: current,
          severity: 'high',
        })
      }
    }
  })

  return hits
}

function scanObjectUrlUsage(sourceFile) {
  const findings = []
  const calls = []
  const callNames = collectCallAndConstructionNames(sourceFile)

  visit(sourceFile, (current) => {
    if (ts.isCallExpression(current)) {
      const callName = getExpressionName(current.expression)
      if (callName === 'createObjectURL') {
        calls.push(current)
      }
    }
  })

  if (calls.length === 0 || callNames.has('revokeObjectURL')) {
    return findings
  }

  for (const call of calls) {
    const { line, character } = sourceFile.getLineAndCharacterOfPosition(call.getStart(sourceFile))
    findings.push({
      column: character + 1,
      filePath: sourceFile.fileName,
      functionName: path.basename(sourceFile.fileName),
      kind: 'object-url',
      line: line + 1,
      message: 'creates an object URL without a matching revokeObjectURL cleanup',
      severity: 'high',
    })
  }

  return findings
}

function collectTopLevelFunctions(sourceFile) {
  const functions = []

  for (const statement of sourceFile.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.body && statement.name) {
      functions.push({ name: statement.name.text, node: statement })
      continue
    }

    if (!ts.isVariableStatement(statement)) {
      continue
    }

    for (const declaration of statement.declarationList.declarations) {
      if (!ts.isIdentifier(declaration.name)) {
        continue
      }

      const initializer = declaration.initializer
      if (!initializer) {
        continue
      }

      if (ts.isArrowFunction(initializer) || ts.isFunctionExpression(initializer)) {
        functions.push({
          name: declaration.name.text,
          node: initializer,
        })
      }
    }
  }

  return functions
}

function collectCallAndConstructionNames(rootNode) {
  const names = new Set()

  visit(rootNode, (current) => {
    if (ts.isCallExpression(current)) {
      const name = getExpressionName(current.expression)
      if (name) {
        names.add(name)
      }
      return
    }

    if (ts.isNewExpression(current)) {
      const name = getExpressionName(current.expression)
      if (name) {
        names.add(name)
      }
    }
  })

  return names
}

function hasOnceListenerOption(callExpression) {
  if (callExpression.arguments.length < 3) {
    return false
  }

  const options = callExpression.arguments[2]
  if (!ts.isObjectLiteralExpression(options)) {
    return false
  }

  for (const property of options.properties) {
    if (!ts.isPropertyAssignment(property)) {
      continue
    }

    const propertyName = getPropertyName(property.name)
    if (propertyName !== 'once') {
      continue
    }

    if (property.initializer.kind === ts.SyntaxKind.TrueKeyword) {
      return true
    }
    if (ts.isPrefixUnaryExpression(property.initializer) && property.initializer.operator === ts.SyntaxKind.ExclamationToken) {
      return false
    }
    if (ts.isIdentifier(property.initializer) && property.initializer.text === 'true') {
      return true
    }
  }

  return false
}

function getExpressionName(expression) {
  if (ts.isIdentifier(expression)) {
    return expression.text
  }
  if (ts.isPropertyAccessExpression(expression)) {
    return expression.name.text
  }
  if (ts.isElementAccessExpression(expression) && ts.isIdentifier(expression.argumentExpression)) {
    return expression.argumentExpression.text
  }
  return undefined
}

function getPropertyName(name) {
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name)) {
    return name.text
  }
  if (ts.isStringLiteral(name) || ts.isNumericLiteral(name)) {
    return name.text
  }
  return undefined
}

function visit(node, callback) {
  callback(node)
  ts.forEachChild(node, (child) => visit(child, callback))
}

function parseArgs(argv) {
  let rootPath = path.resolve(process.cwd(), 'src')
  let json = false

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index]
    if (arg === '--json') {
      json = true
      continue
    }
    if (arg === '--root') {
      const nextArg = argv[index + 1]
      if (!nextArg) {
        throw new Error('--root expects a path')
      }
      rootPath = path.resolve(process.cwd(), nextArg)
      index += 1
      continue
    }
    if (!arg.startsWith('--') && rootPath === path.resolve(process.cwd(), 'src')) {
      rootPath = path.resolve(process.cwd(), arg)
      continue
    }
    throw new Error(`unknown argument: ${arg}`)
  }

  return { json, rootPath }
}

async function collectSourceFiles(rootPath) {
  const results = []
  await walk(rootPath, results)
  return results
}

async function walk(currentPath, results) {
  const entries = await fs.readdir(currentPath, { withFileTypes: true })
  for (const entry of entries) {
    if (ignoredPathSegments.has(entry.name)) {
      continue
    }

    const nextPath = path.join(currentPath, entry.name)
    if (entry.isDirectory()) {
      await walk(nextPath, results)
      continue
    }

    if (!sourceFileExtensions.has(path.extname(entry.name))) {
      continue
    }
    if (isIgnoredPath(nextPath)) {
      continue
    }
    if (entry.name.endsWith('.d.ts')) {
      continue
    }
    if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(entry.name)) {
      continue
    }

    results.push(nextPath)
  }
}

function isIgnoredPath(filePath) {
  return filePath
    .split(path.sep)
    .some((segment) => ignoredPathSegments.has(segment))
}

function getScriptKind(filePath) {
  if (filePath.endsWith('.tsx') || filePath.endsWith('.jsx')) {
    return ts.ScriptKind.TSX
  }
  if (filePath.endsWith('.mts') || filePath.endsWith('.mjs')) {
    return ts.ScriptKind.JS
  }
  return ts.ScriptKind.TS
}

function isMainModule() {
  return Boolean(process.argv[1]) && pathToFileURL(process.argv[1]).href === import.meta.url
}
