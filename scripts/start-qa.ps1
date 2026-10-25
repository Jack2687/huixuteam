# Uses a dedicated database and HTTP port; never writes QA fixtures into llm_refund.
$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'setup-local.ps1')
if (Test-Path (Join-Path $refundRoot '.local/python/Scripts/python.exe')) { $env:REFUND_PYTHON = Join-Path $refundRoot '.local/python/Scripts/python.exe' }
if (Get-NetTCPConnection -State Listen -LocalPort 18743 -ErrorAction SilentlyContinue) { throw 'QA port 18743 is already occupied.' }
Push-Location $refundRoot
try { go build -o .local/refund-qa-server.exe ./cmd/server; if ($LASTEXITCODE -ne 0) { throw 'QA build failed' } } finally { Pop-Location }
$previousPassword = $env:PGPASSWORD
try {
    $env:PGPASSWORD = $env:POSTGRES_PASSWORD
    $exists = & 'C:\Program Files\PostgreSQL\18\bin\psql.exe' -w -h 127.0.0.1 -p 18742 -U llm_refund -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='llm_refund_qa'"
    if ($LASTEXITCODE -ne 0) { throw 'Unable to connect to local PostgreSQL' }
    if ($exists -ne '1') {
        & 'C:\Program Files\PostgreSQL\18\bin\createdb.exe' -w -h 127.0.0.1 -p 18742 -U llm_refund llm_refund_qa
        if ($LASTEXITCODE -ne 0) { throw 'Unable to create QA database' }
    }
} finally { $env:PGPASSWORD = $previousPassword }
$env:DATABASE_URL = $env:DATABASE_URL.Replace('/llm_refund?', '/llm_refund_qa?')
$env:PORT = '18743'
$process = Start-Process -FilePath (Join-Path $refundRoot '.local/refund-qa-server.exe') -WorkingDirectory $refundRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $refundRoot '.local/qa.out.log') -RedirectStandardError (Join-Path $refundRoot '.local/qa.err.log')
$process.Id | Set-Content (Join-Path $refundRoot '.local/qa.pid')
$ready = $false
for ($attempt = 0; $attempt -lt 30; $attempt++) {
    $process.Refresh()
    if ($process.HasExited) { throw 'QA application exited; inspect .local/qa.err.log' }
    try { if ((Invoke-RestMethod 'http://127.0.0.1:18743/health/ready' -TimeoutSec 2).status -eq 'ready') { $ready = $true; break } } catch { }
    Start-Sleep -Milliseconds 200
}
if (!$ready) { throw 'QA readiness timed out' }
Write-Output "QA ready: http://127.0.0.1:18743, PID $($process.Id), database llm_refund_qa"
