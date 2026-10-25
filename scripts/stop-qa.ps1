$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
$pidFile = Join-Path $refundRoot '.local/qa.pid'
if (Test-Path $pidFile) {
    $refundPid = [int](Get-Content $pidFile)
    $process = Get-Process -Id $refundPid -ErrorAction SilentlyContinue
    if ($process -and $process.Path -eq (Join-Path $refundRoot '.local/refund-qa-server.exe')) { Stop-Process -Id $refundPid }
    elseif ($process) { throw 'QA PID belongs to another executable; refusing to stop it.' }
    Remove-Item -LiteralPath $pidFile
}
