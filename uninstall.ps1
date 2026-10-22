[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'Programs\AmonHen\bin'
$installedExe = Join-Path $installDir 'amonhen.exe'
$environmentKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)

if ($environmentKey) {
    try {
        if ($environmentKey.GetValueNames() -contains 'Path') {
            $pathValue = [string]$environmentKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $kind = $environmentKey.GetValueKind('Path')
            $remaining = @($pathValue -split ';' | Where-Object {
                $_ -and -not [string]::Equals($_.TrimEnd('\', '/'), $installDir.TrimEnd('\', '/'), [StringComparison]::OrdinalIgnoreCase)
            })
            if ($remaining.Count -ne (@($pathValue -split ';' | Where-Object { $_ })).Count) {
                $environmentKey.SetValue('Path', ($remaining -join ';'), $kind)
            }
        }
    }
    finally {
        $environmentKey.Dispose()
    }
}

if (Test-Path -LiteralPath $installedExe -PathType Leaf) {
    Remove-Item -LiteralPath $installedExe -Force
}
if ((Test-Path -LiteralPath $installDir -PathType Container) -and
    -not (Get-ChildItem -LiteralPath $installDir -Force | Select-Object -First 1)) {
    Remove-Item -LiteralPath $installDir -Force
}

Write-Host 'Removed Amon Hen and its user PATH entry. Project settings and OS credentials were left intact.'
