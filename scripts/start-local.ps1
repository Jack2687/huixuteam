param([switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'setup-local.ps1')
if (Test-Path (Join-Path $refundRoot '.local/python/Scripts/python.exe')) { $env:REFUND_PYTHON = Join-Path $refundRoot '.local/python/Scripts/python.exe' }
Push-Location $refundRoot
try {
    if (!$SkipBuild) {
        Push-Location (Join-Path $refundRoot 'frontend')
        try {
            if (!(Test-Path 'node_modules')) { npm ci --no-audit --no-fund; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
            npm run build
            if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
        } finally { Pop-Location }
        go build -o .local/refund-server.exe ./cmd/server
        if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    }
    $existing = Get-NetTCPConnection -State Listen -LocalPort ([int]$env:PORT) -ErrorAction SilentlyContinue
    if ($existing) { throw "Port $env:PORT is occupied. Stop the previous project process explicitly before restarting." }
    $process = Start-Process -FilePath (Join-Path $refundRoot '.local/refund-server.exe') -WorkingDirectory $refundRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $refundRoot '.local/server.out.log') -RedirectStandardError (Join-Path $refundRoot '.local/server.err.log')
    $process.Id | Set-Content (Join-Path $refundRoot '.local/server.pid')
    $ready = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        $process.Refresh()
        if ($process.HasExited) { throw 'Application exited; inspect .local/server.err.log' }
        try {
            $health = Invoke-RestMethod "http://127.0.0.1:$env:PORT/health/ready" -TimeoutSec 2
            if ($health.status -eq 'ready') { $ready = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 200
    }
    if (!$ready) { throw 'Application readiness check timed out; inspect .local/server.err.log' }
    Write-Output "Started PID $($process.Id): http://127.0.0.1:$env:PORT. Login details: .local/access.md"
} finally { Pop-Location }
