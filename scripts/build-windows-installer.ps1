param(
    [ValidateSet('win-x64','win-arm64')][string]$Runtime='win-x64',
    [string]$Package='',
    [string]$Output='.tools/release/installers',
    [string]$Compiler=''
)
$ErrorActionPreference='Stop'
$root=(Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if (!$Package) { $Package=Join-Path $root "dist/Meshway-$Runtime" }
$packagePath=(Resolve-Path $Package).Path
foreach ($required in @('Meshway.exe','meshway-daemon.exe','meshway-vpn-helper.exe','wintun.dll','WINTUN-LICENSE.txt')) {
    if (!(Test-Path -LiteralPath (Join-Path $packagePath $required))) { throw "Complete tested native package required: $required" }
}
Copy-Item -LiteralPath (Join-Path $root 'LICENSE') -Destination (Join-Path $packagePath 'LICENSE') -Force
Copy-Item -LiteralPath (Join-Path $root 'THIRD_PARTY_NOTICES.txt') -Destination (Join-Path $packagePath 'THIRD_PARTY_NOTICES.txt') -Force
New-Item -ItemType Directory -Force -Path $Output | Out-Null
$outputPath=(Resolve-Path $Output).Path
if (!$Compiler) {
    $Compiler=Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6/ISCC.exe'
    if (!(Test-Path -LiteralPath $Compiler)) {
        $Compiler=(Get-Command ISCC.exe -ErrorAction Stop).Source
    }
}
$arch=if ($Runtime -eq 'win-arm64') {'arm64'} else {'x64'}
& $Compiler "/DPayloadDir=$packagePath" "/DOutputDir=$outputPath" "/DTargetArch=$arch" '/DAppVersion=0.1.0-alpha.1' (Join-Path $root 'apps/windows/installer.iss')
if ($LASTEXITCODE -ne 0) { throw 'Inno Setup compile failed' }
$installer=Join-Path $outputPath "Meshway-setup-$arch.exe"
if (!(Test-Path -LiteralPath $installer)) { throw 'Installer output missing' }
Write-Output "Built $installer. App is experimental; owner Authenticode signing remains required for trusted public distribution."
