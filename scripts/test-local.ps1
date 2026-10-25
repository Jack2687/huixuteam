$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'setup-local.ps1')
$env:TEST_DATABASE_URL = $env:DATABASE_URL
Push-Location $refundRoot
try {
    $refundPython = 'python'
    if (Test-Path '.local/python/Scripts/python.exe') { $refundPython = Join-Path $refundRoot '.local/python/Scripts/python.exe' }
    & $refundPython -m unittest discover -s runtime -p 'test_*.py'
    if ($LASTEXITCODE -ne 0) { throw 'Offline refund protocol tests failed' }
    Push-Location frontend
    try {
        npm test
        if ($LASTEXITCODE -ne 0) { throw 'Frontend import tests failed' }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    } finally { Pop-Location }
    go test -count=1 -timeout 120s ./...
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL integration tests failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
} finally { Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue; Pop-Location }
