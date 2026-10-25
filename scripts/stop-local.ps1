param([switch]$Database)
$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
$pidFile = Join-Path $refundRoot '.local/server.pid'
if (Test-Path $pidFile) {
    $refundPid = [int](Get-Content $pidFile)
    $process = Get-Process -Id $refundPid -ErrorAction SilentlyContinue
    $expected = Join-Path $refundRoot '.local/refund-server.exe'
    if ($process -and $process.Path -eq $expected) { Stop-Process -Id $refundPid }
    elseif ($process) { throw 'PID belongs to another process; refusing to stop it.' }
    Remove-Item -LiteralPath $pidFile
}
if ($Database) { & 'C:\Program Files\PostgreSQL\18\bin\pg_ctl.exe' -D (Join-Path $refundRoot '.local/postgres') -m fast -w stop }
