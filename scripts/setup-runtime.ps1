$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
Push-Location $refundRoot
try {
    if (!(Test-Path '.local/python/Scripts/python.exe')) {
        python -m venv .local/python
        if ($LASTEXITCODE -ne 0) { throw 'Unable to create isolated Python runtime' }
    }
    & .local/python/Scripts/python.exe -m pip install -r runtime/requirements.txt
    if ($LASTEXITCODE -ne 0) { throw 'Protocol dependency installation failed' }
    Push-Location runtime/manager_oauth
    try {
        npm ci --omit=dev --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { throw 'Sentinel runtime installation failed' }
    } finally { Pop-Location }
    & .local/python/Scripts/python.exe runtime/refund_runner.py --check
    if ($LASTEXITCODE -ne 0) { throw 'Protocol runtime check failed' }
} finally { Pop-Location }
