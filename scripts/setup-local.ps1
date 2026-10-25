param([string]$PostgresBin = 'C:\Program Files\PostgreSQL\18\bin')
$ErrorActionPreference = 'Stop'
$refundRoot = Split-Path -Parent $PSScriptRoot
$refundLocal = Join-Path $refundRoot '.local'
$refundData = Join-Path $refundLocal 'postgres'
New-Item -ItemType Directory -Force -Path $refundLocal | Out-Null
function New-Secret([int]$Length = 32) {
    $bytes = New-Object byte[] $Length
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    return [Convert]::ToBase64String($bytes)
}
if (!(Test-Path (Join-Path $refundRoot '.env'))) {
    $dbPassword = (New-Secret).Replace('+','a').Replace('/','b').Replace('=','')
    $adminPassword = (New-Secret 18).Replace('+','a').Replace('/','b')
    $masterKey = New-Secret
    @"
DATABASE_URL=postgres://llm_refund:${dbPassword}@127.0.0.1:18742/llm_refund?sslmode=disable
MASTER_KEY=$masterKey
PORT=18740
LISTEN_HOST=127.0.0.1
COOKIE_SECURE=false
BOOTSTRAP_EMAIL=admin@local.test
BOOTSTRAP_PASSWORD=$adminPassword
BOOTSTRAP_NAME=Admin
POSTGRES_PASSWORD=$dbPassword
"@ | Set-Content -Encoding utf8 (Join-Path $refundRoot '.env')
    @"
# 本地开发登录

地址：http://127.0.0.1:18740

邮箱：admin@local.test

密码：$adminPassword

这是独立新项目的本地账号，与参考网站账号无关。该文件和 .env 已加入 Git 忽略。
默认余额为 0，无演示交易或真实充值。
"@ | Set-Content -Encoding utf8 (Join-Path $refundLocal 'access.md')
}
Get-Content (Join-Path $refundRoot '.env') | ForEach-Object {
    if ($_ -match '^([A-Z_]+)=(.*)$') { [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process') }
}
if (!(Test-Path (Join-Path $PostgresBin 'initdb.exe'))) { throw "PostgreSQL binaries missing: $PostgresBin" }
if (!(Test-Path (Join-Path $refundData 'PG_VERSION'))) {
    $pwFile = Join-Path $refundLocal 'postgres-init-password.tmp'
    try {
        [IO.File]::WriteAllText($pwFile, $env:POSTGRES_PASSWORD, [Text.UTF8Encoding]::new($false))
        & (Join-Path $PostgresBin 'initdb.exe') -D $refundData -U llm_refund --encoding=UTF8 --locale=C --auth=scram-sha-256 --pwfile=$pwFile
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL initialization failed' }
    } finally { if (Test-Path $pwFile) { Remove-Item -LiteralPath $pwFile } }
}
& (Join-Path $PostgresBin 'pg_ctl.exe') -D $refundData status *> $null
if ($LASTEXITCODE -ne 0) {
    & (Join-Path $PostgresBin 'pg_ctl.exe') -D $refundData -l (Join-Path $refundLocal 'postgres.log') -o '-p 18742 -h 127.0.0.1' -w start
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed; inspect .local/postgres.log' }
}
$oldPgPassword = $env:PGPASSWORD
try {
    $env:PGPASSWORD = $env:POSTGRES_PASSWORD
    $exists = & (Join-Path $PostgresBin 'psql.exe') -w -h 127.0.0.1 -p 18742 -U llm_refund -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='llm_refund'"
    if ($LASTEXITCODE -ne 0) { throw 'Unable to connect to isolated PostgreSQL' }
    if ($exists -ne '1') {
        & (Join-Path $PostgresBin 'createdb.exe') -w -h 127.0.0.1 -p 18742 -U llm_refund llm_refund
        if ($LASTEXITCODE -ne 0) { throw 'Unable to create llm_refund database' }
    }
} finally { $env:PGPASSWORD = $oldPgPassword }
Write-Output 'Local PostgreSQL ready on 127.0.0.1:18742. Login details: .local/access.md'
