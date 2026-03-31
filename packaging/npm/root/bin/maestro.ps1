$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeBin = if ($env:MAESTRO_NODE_BIN) { $env:MAESTRO_NODE_BIN } else { "node" }
& $NodeBin (Join-Path $ScriptDir "maestro.js") @args
exit $LASTEXITCODE
