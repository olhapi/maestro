#!/usr/bin/env bash

set -euo pipefail

DB_PATH="${1:-${MAESTRO_DB_PATH:-$HOME/.maestro/maestro.db}}"
RUN_THRESHOLD="${RUN_THRESHOLD:-20}"
ATTEMPT_THRESHOLD="${ATTEMPT_THRESHOLD:-8}"

if [[ ! -f "$DB_PATH" ]]; then
  echo "database not found: $DB_PATH" >&2
  exit 1
fi

sqlite3 -header -column "$DB_PATH" <<SQL
WITH latest_retry AS (
  SELECT issue_id, MAX(seq) AS seq
  FROM runtime_events
  WHERE kind IN ('retry_scheduled', 'retry_paused', 'run_failed', 'run_unsuccessful', 'run_completed')
  GROUP BY issue_id
),
latest_retry_detail AS (
  SELECT r.issue_id, r.kind, r.attempt, r.error
  FROM runtime_events r
  JOIN latest_retry lr ON lr.issue_id = r.issue_id AND lr.seq = r.seq
)
SELECT
  i.identifier,
  i.state,
  COALESCE(w.run_count, 0) AS workspace_run_count,
  COALESCE(es.attempt, 0) AS latest_attempt,
  COALESCE(lrd.kind, '') AS latest_runtime_kind,
  COALESCE(lrd.error, '') AS latest_error
FROM issues i
LEFT JOIN workspaces w ON w.issue_id = i.id
LEFT JOIN issue_execution_sessions es ON es.issue_id = i.id
LEFT JOIN latest_retry_detail lrd ON lrd.issue_id = i.id
WHERE COALESCE(w.run_count, 0) >= $RUN_THRESHOLD
   OR COALESCE(es.attempt, 0) >= $ATTEMPT_THRESHOLD
ORDER BY workspace_run_count DESC, latest_attempt DESC, i.identifier ASC;
SQL
