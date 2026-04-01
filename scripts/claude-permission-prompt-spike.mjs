#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { spawnSync } from 'node:child_process';

const MCP_PROTOCOL_VERSION = '2025-11-25';
const DEFAULT_SERVER_NAME = 'permission-spy';
const DEFAULT_TOOL_NAME = 'approval_prompt';
const DEFAULT_CASES = [
  {
    name: 'command-exec',
    title: 'Command execution',
    responseMode: 'allow',
    prompt:
      'Use Bash to run `pwd` and `git status --short` in this workspace, then report the exact command output.',
  },
  {
    name: 'file-modification',
    title: 'File modification',
    responseMode: 'allow',
    prompt:
      'Use Write to create notes.txt with exactly one line: file-change-ok.',
  },
  {
    name: 'protected-git-write',
    title: 'Protected .git write',
    responseMode: 'deny',
    prompt:
      'Use Edit to add a comment line "# spike" to .git/config, then report what happened.',
  },
  {
    name: 'user-input-interruption',
    title: 'User-input interruption',
    responseMode: 'ask',
    prompt:
      'Use Write to create questions.txt with a short note, but if you need more information, ask me for it first.',
  },
];

if (process.argv[2] === 'server') {
  await runServer();
} else {
  await runDriver();
}

async function runServer() {
  const logPath = process.env.SPIKE_LOG_PATH;
  const rawLogPath = process.env.SPIKE_RAW_LOG_PATH;
  const responseMode = process.env.SPIKE_RESPONSE_MODE || 'allow';

  if (!logPath) {
    throw new Error('SPIKE_LOG_PATH is required');
  }

  let buffer = Buffer.alloc(0);
  let transportMode = null;

  function write(message) {
    const json = JSON.stringify(message);
    if (transportMode === 'jsonl') {
      process.stdout.write(`${json}\n`);
      return;
    }
    const framed = `Content-Length: ${Buffer.byteLength(json, 'utf8')}\r\n\r\n${json}`;
    process.stdout.write(framed);
  }

  function log(entry) {
    fs.appendFileSync(
      logPath,
      JSON.stringify({
        ts: new Date().toISOString(),
        ...entry,
      }) + '\n',
    );
  }

  function logRawChunk(chunk) {
    if (!rawLogPath) {
      return;
    }
    fs.appendFileSync(
      rawLogPath,
      JSON.stringify({
        ts: new Date().toISOString(),
        chunk: chunk.toString('utf8'),
      }) + '\n',
    );
  }

  function respond(id, result) {
    const message = { jsonrpc: '2.0', id, result };
    log({ direction: 'out', message });
    write(message);
  }

  function handleMessage(message) {
    log({ direction: 'in', message });

    switch (message.method) {
      case 'initialize':
    respond(message.id, {
      protocolVersion: MCP_PROTOCOL_VERSION,
      capabilities: { tools: { listChanged: true } },
      serverInfo: {
        name: DEFAULT_SERVER_NAME,
        version: '0.0.0',
      },
        });
        return;
      case 'notifications/initialized':
        return;
      case 'tools/list':
        respond(message.id, {
          tools: [
            {
              name: DEFAULT_TOOL_NAME,
              description: 'Permission prompt spy for Claude Code approvals.',
              inputSchema: {
                type: 'object',
                properties: {
                  tool_name: { type: 'string' },
                  input: {},
                  tool_use_id: { type: 'string' },
                },
                required: ['tool_name', 'input'],
              },
            },
          ],
        });
        return;
      case 'tools/call': {
        const args = message.params?.arguments ?? {};
        const payload = {
          tool_name: args.tool_name ?? null,
          input: args.input ?? null,
          tool_use_id: args.tool_use_id ?? null,
        };
        log({ direction: 'tool_call', payload, responseMode });

        let response;
        if (responseMode === 'deny') {
          response = {
            behavior: 'deny',
            message: `Denied by spike server for ${payload.tool_name ?? 'unknown tool'}`,
          };
        } else if (responseMode === 'ask') {
          response = {
            behavior: 'ask',
            message: `Clarify request for ${payload.tool_name ?? 'unknown tool'}`,
          };
        } else {
          response = {
            behavior: 'allow',
            updatedInput: payload.input,
          };
        }

        respond(message.id, {
          content: [{ type: 'text', text: JSON.stringify(response) }],
          isError: false,
        });
        return;
      }
      default:
        respond(message.id, { ok: true });
    }
  }

  process.stdin.on('data', (chunk) => {
    logRawChunk(chunk);
    buffer = Buffer.concat([buffer, chunk]);
    while (true) {
      const framed = parseFramedMessage(buffer);
      if (framed) {
        transportMode = 'framed';
        buffer = framed.rest;
        handleMessage(JSON.parse(framed.body));
        continue;
      }

      const line = parseJsonLine(buffer);
      if (line) {
        transportMode = 'jsonl';
        buffer = line.rest;
        if (line.body !== null) {
          handleMessage(JSON.parse(line.body));
        }
        continue;
      }

      return;
    }
  });

  process.stdin.resume();
}

async function runDriver() {
  const harnessRoot = resolveHarnessRoot();
  const workspace = path.join(harnessRoot, 'workspace');
  const configPath = path.join(harnessRoot, 'mcp.json');
  const settingsPath = path.join(harnessRoot, 'settings.json');
  const reportPath = path.join(harnessRoot, 'report.json');

  ensureDir(workspace);

  initWorkspace(workspace);
  writeSettings(settingsPath);

  const cases = [];
  for (const testCase of DEFAULT_CASES) {
    const caseRoot = path.join(harnessRoot, testCase.name);
    const logPath = path.join(caseRoot, 'tool-calls.jsonl');
    const rawLogPath = path.join(caseRoot, 'transport.jsonl');
    writeConfig(configPath, logPath, rawLogPath, testCase.responseMode);
    const caseResult = runClaudeCase({
      harnessRoot,
      workspace,
      configPath,
      settingsPath,
      caseName: testCase.name,
      title: testCase.title,
      prompt: testCase.prompt,
      responseMode: testCase.responseMode,
    });
    cases.push(caseResult);
  }

  const report = {
    harness_root: harnessRoot,
    workspace,
    protocol_version: MCP_PROTOCOL_VERSION,
    server_name: DEFAULT_SERVER_NAME,
    tool_name: DEFAULT_TOOL_NAME,
    cases,
  };
  fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);

  printSummary(report);
}

function resolveHarnessRoot() {
  const explicit = process.argv.findIndex((value) => value === '--harness-root');
  if (explicit !== -1 && process.argv[explicit + 1]) {
    return path.resolve(process.argv[explicit + 1]);
  }
  const defaultRoot = path.join(process.cwd(), '.maestro', 'claude-permission-prompt-spike');
  ensureDir(defaultRoot);
  const unique = `run-${new Date().toISOString().replace(/[:.]/g, '-')}-${process.pid}`;
  return path.join(defaultRoot, unique);
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function parseFramedMessage(buffer) {
  const crlf = buffer.indexOf('\r\n\r\n');
  const lf = crlf === -1 ? buffer.indexOf('\n\n') : -1;
  const separator = crlf !== -1 ? crlf : lf;
  if (separator === -1) {
    return null;
  }
  const separatorLength = crlf !== -1 ? 4 : 2;
  const header = buffer.slice(0, separator).toString('utf8');
  const match = header.match(/Content-Length:\s*(\d+)/i);
  if (!match) {
    return null;
  }
  const bodyLength = Number(match[1]);
  const bodyStart = separator + separatorLength;
  if (buffer.length < bodyStart + bodyLength) {
    return null;
  }
  const body = buffer.slice(bodyStart, bodyStart + bodyLength).toString('utf8');
  return {
    body,
    rest: buffer.slice(bodyStart + bodyLength),
  };
}

function parseJsonLine(buffer) {
  let newline = buffer.indexOf('\n');
  if (newline === -1) {
    return null;
  }
  if (newline > 0 && buffer[newline - 1] === 0x0d) {
    newline -= 1;
  }
  const line = buffer.slice(0, newline).toString('utf8').trim();
  const restStart = buffer.indexOf('\n') + 1;
  if (!line) {
    return {
      body: null,
      rest: buffer.slice(restStart),
    };
  }
  return {
    body: line,
    rest: buffer.slice(restStart),
  };
}

function initWorkspace(workspace) {
  ensureDir(workspace);
  spawnSync('git', ['init', '-q'], { cwd: workspace, stdio: 'ignore' });
  spawnSync('git', ['config', 'user.name', 'Maestro Spike'], {
    cwd: workspace,
    stdio: 'ignore',
  });
  spawnSync('git', ['config', 'user.email', 'spike@example.com'], {
    cwd: workspace,
    stdio: 'ignore',
  });
  fs.writeFileSync(path.join(workspace, 'notes.txt'), 'seed\n');
  fs.writeFileSync(path.join(workspace, 'README.md'), '# permission prompt spike\n');
}

function writeConfig(configPath, logPath, rawLogPath, responseMode) {
  const serverEntryPoint = path.join(process.cwd(), 'scripts', 'claude-permission-prompt-spike.mjs');
  const config = {
    mcpServers: {
      [DEFAULT_SERVER_NAME]: {
        type: 'stdio',
        command: process.execPath,
        args: [serverEntryPoint, 'server'],
        env: {
          SPIKE_LOG_PATH: logPath,
          SPIKE_RAW_LOG_PATH: rawLogPath,
          SPIKE_RESPONSE_MODE: responseMode,
        },
      },
    },
  };
  fs.writeFileSync(configPath, `${JSON.stringify(config, null, 2)}\n`);
}

function writeSettings(settingsPath) {
  const settings = {
    permissions: {
      ask: ['Bash', 'Edit', 'Write', 'MultiEdit'],
    },
  };
  fs.writeFileSync(settingsPath, `${JSON.stringify(settings, null, 2)}\n`);
}

function runClaudeCase({
  harnessRoot,
  workspace,
  configPath,
  settingsPath,
  caseName,
  title,
  prompt,
  responseMode,
}) {
  const caseRoot = path.join(harnessRoot, caseName);
  ensureDir(caseRoot);
  const logPath = path.join(caseRoot, 'tool-calls.jsonl');
  const stdoutPath = path.join(caseRoot, 'claude.stdout.txt');
  const stderrPath = path.join(caseRoot, 'claude.stderr.txt');

  const env = { ...process.env };

  const result = spawnSync(
    'claude',
    [
      '-p',
      '--model',
      'sonnet',
      '--output-format=json',
      '--input-format=text',
      '--max-budget-usd=0.05',
      '--max-turns=4',
      '--permission-mode=default',
      '--allowed-tools',
      'Bash,Edit,Write,MultiEdit',
      '--permission-prompt-tool',
      `mcp__${DEFAULT_SERVER_NAME}__${DEFAULT_TOOL_NAME}`,
      '--mcp-config',
      configPath,
      '--strict-mcp-config',
      '--settings',
      settingsPath,
      prompt,
    ],
    {
      cwd: workspace,
      encoding: 'utf8',
      maxBuffer: 20 * 1024 * 1024,
      env,
      timeout: 120000,
    },
  );

  fs.writeFileSync(stdoutPath, result.stdout ?? '');
  fs.writeFileSync(stderrPath, result.stderr ?? '');

  const rawOutput = safeParseJson(result.stdout ?? '');
  const logEntries = readJsonLines(logPath);
  const permissionCalls = logEntries
    .filter((entry) => entry.direction === 'in' && entry.message?.method === 'tools/call')
    .map((entry) => entry.message);

  return {
    name: caseName,
    title,
    prompt,
    response_mode: responseMode,
    exit_code: result.status,
    signal: result.signal ?? null,
    timed_out: Boolean(result.error && String(result.error).includes('ETIMEDOUT')),
    stdout_path: path.relative(process.cwd(), stdoutPath),
    stderr_path: path.relative(process.cwd(), stderrPath),
    raw_output: rawOutput,
    permission_call_count: permissionCalls.length,
    raw_permission_calls: permissionCalls,
    log_path: path.relative(process.cwd(), logPath),
  };
}

function safeParseJson(text) {
  const trimmed = text.trim();
  if (!trimmed) {
    return null;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return { parse_error: 'invalid_json', text };
  }
}

function readJsonLines(logPath) {
  if (!fs.existsSync(logPath)) {
    return [];
  }
  const raw = fs.readFileSync(logPath, 'utf8').trim();
  if (!raw) {
    return [];
  }
  return raw
    .split('\n')
    .filter(Boolean)
    .map((line) => JSON.parse(line));
}

function printSummary(report) {
  console.log(`Harness: ${report.harness_root}`);
  console.log(`Workspace: ${report.workspace}`);
  console.log('');
  for (const testCase of report.cases) {
    console.log(`[${testCase.title}]`);
    console.log(`  mode: ${testCase.response_mode}`);
    console.log(`  exit: ${testCase.exit_code}`);
    console.log(`  permission calls: ${testCase.permission_call_count}`);
    if (testCase.raw_permission_calls?.length > 0) {
      const call = testCase.raw_permission_calls[0];
      console.log(`  tool: ${call.params?.name ?? 'unknown'}`);
      console.log(`  raw args: ${JSON.stringify(call.params?.arguments ?? null)}`);
    } else {
      console.log('  tool: (none)');
    }
    if (testCase.raw_output?.result) {
      console.log(`  result: ${testCase.raw_output.result}`);
    } else if (testCase.raw_output?.parse_error) {
      console.log(`  result: ${testCase.raw_output.parse_error}`);
    } else {
      console.log('  result: (none)');
    }
    console.log(`  log: ${testCase.log_path}`);
    console.log('');
  }
  console.log(`Report written to ${path.join(report.harness_root, 'report.json')}`);
}
