$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$run = Join-Path $repo 'dist\boundary-c4af5731-r2'
$binary = Join-Path $repo 'dist\amonhen-fix.exe'
$shared = Join-Path $run 'shared'
$deadline = [DateTime]::UtcNow.AddMinutes(10)
$seen = @{}
while ([DateTime]::UtcNow -lt $deadline) {
    $stagePath = Join-Path $run 'stage'
    if (-not (Test-Path -LiteralPath $stagePath)) { Start-Sleep -Milliseconds 250; continue }
    $stage = Get-Content -LiteralPath $stagePath -Raw
    if ($stage -notin @('ready','host-add','host-rename','host-delete','agent-add','agent-rename','agent-delete','complete')) {
        Start-Sleep -Milliseconds 250
        continue
    }
    if ($stage -eq 'complete') {
        foreach ($phase in @('agent-add','agent-rename','agent-delete')) {
            $hostReport = Get-Content -LiteralPath (Join-Path $run "host-observed-$phase.json") -Raw | ConvertFrom-Json
            $agentReport = Get-Content -LiteralPath (Join-Path $run "$phase.json") -Raw | ConvertFrom-Json
            if ($hostReport.registrySHA256 -ne $agentReport.registrySHA256 -or $hostReport.list -cne $agentReport.list) { throw "Host did not observe $phase" }
        }
        Write-Output 'PASS: host and running MXC agent observed all six mutations without restart.'
        exit 0
    }
    if ($seen.ContainsKey($stage)) { Start-Sleep -Milliseconds 250; continue }
    if ($stage -eq 'ready') {
        & "$PSScriptRoot\sandbox-fixture.ps1" -Root (Join-Path $env:APPDATA 'amonhen-investigation-c4af5731') -Binary (Join-Path $repo 'dist\amonhen-investigation.exe') -Action Snapshot -ContextLabel agent-legacy |
            Set-Content -LiteralPath (Join-Path $run 'legacy-agent.json')
        $hostLegacy = Get-Content -LiteralPath (Join-Path $run 'legacy-host-add.json') -Raw | ConvertFrom-Json
        $agentLegacy = Get-Content -LiteralPath (Join-Path $run 'legacy-agent.json') -Raw | ConvertFrom-Json
        if ($hostLegacy.list -notmatch 'fixture-4' -or $agentLegacy.list -match 'fixture-4' -or $hostLegacy.registrySHA256 -eq $agentLegacy.registrySHA256) { throw 'Legacy discrepancy was not reproduced.' }
        Write-Output 'PASS: old CLI reproduces AppData split with synthetic metadata.'
        while (-not (Test-Path -LiteralPath (Join-Path $run 'credential-checked.ack'))) {
            if ([DateTime]::UtcNow -gt $deadline) { throw 'Credential test timed out.' }
            Start-Sleep -Milliseconds 250
        }
    }
    $action = switch ($stage) { 'agent-add' {'Add'} 'agent-rename' {'Rename'} 'agent-delete' {'Delete'} default {'Snapshot'} }
    $report = & "$PSScriptRoot\sandbox-fixture.ps1" -Root $shared -Binary $binary -Store Shared -Action $action -ContextLabel "mxc-$stage" -DesktopVersion '26.1016.6573.0' -SandboxVersion 'codex-cli 0.163.0-alpha.11 MXC'
    $reportName = if ($stage -like 'agent-*') { $stage } else { "agent-observed-$stage" }
    $report | Set-Content -LiteralPath (Join-Path $run "$reportName.json")
    if ($stage -like 'host-*') {
        $hostReport = Get-Content -LiteralPath (Join-Path $run "$stage.json") -Raw | ConvertFrom-Json
        $agentReport = $report | ConvertFrom-Json
        if ($hostReport.registrySHA256 -ne $agentReport.registrySHA256 -or $hostReport.list -cne $agentReport.list) { throw "Agent did not observe $stage" }
    }
    $seen[$stage] = $true
    Set-Content -LiteralPath (Join-Path $run "$stage.ack") -Value observed -NoNewline
    Write-Output "Agent observed $stage"
    Start-Sleep -Milliseconds 250
}
throw 'Boundary test timed out; evidence retained.'
