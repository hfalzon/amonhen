param(
    [Parameter(Mandatory)][string]$Root,
    [Parameter(Mandatory)][string]$Binary,
    [Parameter(Mandatory)][ValidateSet('Init','Add','Rename','Delete','Snapshot')][string]$Action,
    [Parameter(Mandatory)][string]$ContextLabel,
    [ValidateSet('Legacy','Shared')][string]$Store = 'Legacy',
    [string]$DesktopVersion = 'unrecorded',
    [string]$SandboxVersion = 'unrecorded'
)
$ErrorActionPreference = 'Stop'
if (-not [IO.Path]::IsPathFullyQualified($Root)) { throw 'Root must be absolute.' }
$Root = [IO.Path]::GetFullPath($Root)
$Binary = (Resolve-Path -LiteralPath $Binary).Path
$marker = Join-Path $Root 'amonhen-investigation-fixture'
$registry = Join-Path $Root 'amonhen/projects.json'
if ($Action -eq 'Init') {
    # Never adopt an existing directory: it may contain real user configuration.
    if (Test-Path -LiteralPath $Root) { throw 'Init requires a new, nonexistent fixture directory.' }
    New-Item -ItemType Directory -Path (Join-Path $Root 'amonhen') | Out-Null
    Set-Content -LiteralPath $marker -Value 'v1' -NoNewline
    $records = @(1..3 | ForEach-Object {
        @{ name = "fixture-$_"; uuid = ('00000000-0000-4000-8000-{0:d12}' -f $_) }
    })
} else {
    if (-not (Test-Path -LiteralPath $marker)) { throw 'Not an initialized investigation fixture.' }
    $records = @(Get-Content -LiteralPath $registry -Raw | ConvertFrom-Json)
    switch ($Action) {
        'Add' {
            if ($records.name -contains 'fixture-4') { throw 'fixture-4 already exists.' }
            $records += [pscustomobject]@{name='fixture-4'; uuid='00000000-0000-4000-8000-000000000004'}
        }
        'Rename' {
            $record = @($records | Where-Object name -eq 'fixture-4')
            if ($record.Count -ne 1) { throw 'Expected fixture-4.' }
            $record[0].name = 'fixture-renamed'
        }
        'Delete' {
            if ($records.name -notcontains 'fixture-renamed') { throw 'Expected fixture-renamed.' }
            $records = @($records | Where-Object name -ne 'fixture-renamed')
        }
    }
}
if ($Action -ne 'Snapshot') {
    ConvertTo-Json -InputObject @($records) | Set-Content -LiteralPath $registry -Encoding utf8NoBOM
}
$oldAppData = $env:APPDATA
$oldConfigDir = $env:AMONHEN_CONFIG_DIR
try {
    $env:APPDATA = $Root
    if ($Store -eq 'Shared') { $env:AMONHEN_CONFIG_DIR = Join-Path $Root 'amonhen' }
    else { $env:AMONHEN_CONFIG_DIR = $null }
    $list = @(& $Binary ls 2>&1)
    $listExit = $LASTEXITCODE
    $expectedList = @("NAME`tBWS_PROJECT_UUID") + @($records | ForEach-Object { "$($_.name)`t$($_.uuid)" })
    $listMatches = ($list -join "`n") -ceq ($expectedList -join "`n")
    $resolution = @($records | ForEach-Object {
        $value = @(& $Binary project uuid -N $_.name 2>&1)
        @{name=$_.name; expected=$_.uuid; output=($value -join "`n"); exit=$LASTEXITCODE}
    })
    $item = Get-Item -LiteralPath $registry
    # This fixture contains metadata only. No credential-store reads or writes.
    [ordered]@{
        utc = [DateTime]::UtcNow.ToString('o')
        context = $ContextLabel
        store = $Store
        desktopVersion = $DesktopVersion
        sandboxVersion = $SandboxVersion
        windows = [Environment]::OSVersion.VersionString
        identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
        processId = $PID
        binary = $Binary
        binarySHA256 = (Get-FileHash -LiteralPath $Binary -Algorithm SHA256).Hash
        cliVersion = (@(& $Binary --version) -join "`n")
        registry = $registry
        registrySHA256 = (Get-FileHash -LiteralPath $registry -Algorithm SHA256).Hash
        registryModifiedUTC = $item.LastWriteTimeUtc.ToString('o')
        fileIdentity = (@(& fsutil file queryfileid $registry 2>&1) -join "`n")
        fileIdentityExit = $LASTEXITCODE
        list = ($list -join "`n")
        listExit = $listExit
        listMatchesFixture = $listMatches
        resolution = $resolution
    } | ConvertTo-Json -Depth 5
    if ($listExit -ne 0 -or -not $listMatches -or @($resolution | Where-Object { $_.exit -ne 0 -or $_.output -ne $_.expected }).Count) {
        throw 'Fixture lookup failed; inspect the captured report.'
    }
} finally {
    $env:APPDATA = $oldAppData
    $env:AMONHEN_CONFIG_DIR = $oldConfigDir
}
