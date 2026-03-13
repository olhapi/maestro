# Maestro Architecture Graph

This graph is derived from the current code structure and runtime behavior in `cmd/maestro`, `internal/orchestrator`, `internal/providers`, `internal/mcp`, `internal/httpserver`, `internal/dashboardapi`, `internal/observability`, and `internal/agent`.

```mermaid
flowchart TB
    subgraph Clients["Clients"]
        MCPClient["Codex / ChatGPT<br>via MCP client"]
        Browser["Browser"]
        CLI["CLI live helpers<br>status --dashboard<br>sessions / events / runtime-series<br>issue execution / retry"]
    end

    subgraph Entry["CLI Entrypoints"]
        Run["maestro run"]
        MCPBridge["maestro mcp"]
        LocalCLI["maestro project / epic / issue / board / verify / workflow"]
    end

    subgraph Runtime["Long-lived Runtime"]
        MCPDaemon["Private MCP daemon<br>loopback + bearer token"]
        HTTP["Public HTTP server<br>optional --port"]
        Orch["Orchestrator"]
        Agent["Agent runner"]
        Workflow["WORKFLOW.md<br>config manager"]
    end

    subgraph APIs["HTTP Surfaces"]
        ObsAPI["/api/v1/*<br>live observability API"]
        AppAPI["/api/v1/app/*<br>dashboard application API"]
        WS["/api/v1/ws<br>invalidate stream"]
        UI["Embedded dashboard UI<br>/"]
    end

    subgraph Domain["Local Domain Layer"]
        ProviderSvc["providers.Service"]
        KanbanProvider["kanban provider"]
        LinearProvider["linear provider<br>limited support"]
    end

    subgraph Storage["Local Persistence"]
        Store["SQLite store<br>projects / issues / epics / workspaces<br>sessions / commands / runtime events"]
    end

    subgraph External["External Systems"]
        Linear["Linear GraphQL API"]
        Codex["Codex CLI<br>app-server or stdio mode"]
        Workspaces["Per-issue workspaces"]
    end

    MCPClient --> MCPBridge
    MCPBridge --> MCPDaemon
    Run --> MCPDaemon
    Run --> HTTP
    Run --> Orch
    LocalCLI --> ProviderSvc
    LocalCLI --> Store

    Browser --> UI
    UI --> AppAPI
    Browser -. live refresh .-> WS
    CLI --> ObsAPI

    HTTP --> UI
    HTTP --> ObsAPI
    HTTP --> AppAPI
    HTTP --> WS

    ObsAPI --> Orch
    ObsAPI --> Store
    AppAPI --> ProviderSvc
    AppAPI --> Store
    AppAPI --> Orch

    MCPDaemon --> ProviderSvc
    MCPDaemon --> Store
    MCPDaemon --> Orch

    Orch --> Workflow
    Orch --> ProviderSvc
    Orch --> Agent
    Orch --> Store

    Agent --> Workflow
    Agent --> Store
    Agent --> Workspaces
    Agent --> Codex

    ProviderSvc --> KanbanProvider
    ProviderSvc --> LinearProvider
    ProviderSvc --> Store

    KanbanProvider --> Store
    LinearProvider --> Linear
    LinearProvider --> Store
```

## Reading notes

- `maestro run` is the only long-lived daemon for a database. It owns orchestration, the private MCP daemon, and the optional public HTTP server.
- `maestro mcp` is only a bridge. It discovers the live daemon for the same DB and forwards MCP over stdio.
- The public HTTP server exposes two API layers:
  - `/api/v1/*` for live observability and CLI helpers
  - `/api/v1/app/*` for the embedded dashboard control plane
- Project provider choice lives in the project record, not in `WORKFLOW.md`.
- Even provider-backed issues are synchronized into the local SQLite store and then supervised through the same local runtime.
