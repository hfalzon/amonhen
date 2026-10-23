# Run in an ordinary host PowerShell 7 window. Coordinates with the agent
# through fixture-only marker files; never changes production project records.
$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$run = Join-Path $repo 'dist\boundary-c4af5731-r2'
if (Test-Path -LiteralPath $run) { throw 'Boundary run already exists; preserve its evidence and use a new run ID.' }
New-Item -ItemType Directory -Path $run | Out-Null
$binary = Join-Path $repo 'dist\amonhen-fix.exe'
$probe = Join-Path $repo 'dist\credential-probe.exe'
$shared = Join-Path $run 'shared'
function Capture([string]$action, [string]$label) {
    & "$PSScriptRoot\sandbox-fixture.ps1" -Root $shared -Binary $binary -Store Shared -Action $action -ContextLabel "host-$label" -DesktopVersion '26.1016.6573.0' -SandboxVersion 'normal-host' |
        Set-Content -LiteralPath (Join-Path $run "$label.json")
}
function Signal([string]$stage) {
    Set-Content -LiteralPath (Join-Path $run 'stage') -Value $stage -NoNewline
    Write-Host "Host stage: $stage"
    $deadline = [DateTime]::UtcNow.AddMinutes(10)
    while (-not (Test-Path -LiteralPath (Join-Path $run "$stage.ack"))) {
        if ([DateTime]::UtcNow -gt $deadline) { throw "Timed out waiting for agent at $stage; evidence retained." }
        Start-Sleep -Milliseconds 250
    }
}
# Recreate the old AppData split using the dedicated fixture the agent created.
$legacyRoot = Join-Path $env:APPDATA 'amonhen-investigation-c4af5731'
if (-not (Test-Path -LiteralPath $legacyRoot)) {
    & "$PSScriptRoot\sandbox-fixture.ps1" -Root $legacyRoot -Binary (Join-Path $repo 'dist\amonhen-investigation.exe') -Action Init -ContextLabel host-legacy |
        Set-Content -LiteralPath (Join-Path $run 'legacy-host-init.json')
}
& "$PSScriptRoot\sandbox-fixture.ps1" -Root $legacyRoot -Binary (Join-Path $repo 'dist\amonhen-investigation.exe') -Action Add -ContextLabel host-legacy-add |
    Set-Content -LiteralPath (Join-Path $run 'legacy-host-add.json')
Capture Init 'host-init'
& $probe init (Join-Path $run 'host-credential.json')
if ($LASTEXITCODE -ne 0) { throw 'Host synthetic credential initialization failed.' }
Signal 'ready'
Capture Add 'host-add'
Signal 'host-add'
Capture Rename 'host-rename'
Signal 'host-rename'
Capture Delete 'host-delete'
Signal 'host-delete'
foreach ($phase in @('agent-add','agent-rename','agent-delete')) {
    Signal $phase
    Capture Snapshot "host-observed-$phase"
}
& $probe check (Join-Path $repo 'dist\credential-boundary-approved.json')
if ($LASTEXITCODE -ne 0) { throw 'Host cannot read agent-approved synthetic credential.' }
& $probe remove (Join-Path $run 'host-credential.json')
if ($LASTEXITCODE -ne 0) { throw 'Host credential cleanup failed.' }
Set-Content -LiteralPath (Join-Path $run 'stage') -Value complete -NoNewline
Write-Host "Host checks complete. Evidence: $run"
