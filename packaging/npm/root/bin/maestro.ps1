$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
& node (Join-Path $ScriptDir "maestro.js") @args
exit $LASTEXITCODE
