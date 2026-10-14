param([string]$Staging='.tools/release/installers',[string]$Output='artifacts/windows-installer-validation.json')
$ErrorActionPreference='Stop'
if ($env:OS -ne 'Windows_NT') { throw 'A disposable Windows runner is required.' }
$beforeRoutes=Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | ConvertTo-Json -Compress
$beforeDns=Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Sort-Object InterfaceIndex,AddressFamily | ConvertTo-Json -Compress
$root=(Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$package=(Resolve-Path 'dist/Meshway-win-x64').Path
$installer=(Resolve-Path (Join-Path $Staging 'Meshway-setup-x64.exe')).Path
$directory=Join-Path $env:TEMP ('MeshwayInstallerLab-'+[Guid]::NewGuid().ToString('N'))
$install=Join-Path $directory 'Meshway'
$debug=Join-Path $root 'artifacts/windows-installer-debug'
New-Item -ItemType Directory -Force $directory,$debug | Out-Null
$state=Join-Path $env:LOCALAPPDATA 'Meshway/installer-lab-preserve.txt'
New-Item -ItemType Directory -Force (Split-Path $state -Parent) | Out-Null
$marker=[Guid]::NewGuid().ToString('N');Set-Content -LiteralPath $state -Value $marker
$shortcuts=Join-Path ([Environment]::GetFolderPath('Programs')) 'Meshway'
if (Test-Path $shortcuts) { throw 'Disposable runner unexpectedly has an existing Meshway shortcut group.' }
$registry='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Meshway-x64_is1'
if (Test-Path $registry) { throw 'Disposable runner unexpectedly has an existing Meshway installation.' }
function Check([bool]$value,[string]$message) { if (!$value) { throw $message } }
function Invoke-Setup([string]$exe,[string[]]$arguments) {
    $p=Start-Process -FilePath $exe -ArgumentList $arguments -PassThru -Wait
    Check ($p.ExitCode -eq 0) "Installer operation failed: exit $($p.ExitCode)"
}
try {
    Invoke-Setup $installer @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',"/DIR=`"$install`"",("/LOG=`""+(Join-Path $debug 'install.log')+"`""))
    $payload=@(Get-ChildItem -LiteralPath $package -File -Recurse | Where-Object { $_.Name -notlike '*.pdb' -and $_.Name -notin @('meshway-firewall-test.exe') -and $_.Name -notlike '*.test.exe' })
    foreach ($file in $payload) {
        $target=Join-Path $install $file.FullName.Substring($package.Length+1)
        Check (Test-Path -LiteralPath $target) "Installed payload missing: $($file.Name)"
        Check ((Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash -eq (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash) "Installed payload differs: $($file.Name)"
    }
    Check (Test-Path $registry) 'Per-user uninstaller registry entry missing.'
    $shell=New-Object -ComObject WScript.Shell
    foreach ($shortcut in @('Meshway.lnk','Uninstall Meshway.lnk')) {
        Check (Test-Path -LiteralPath (Join-Path $shortcuts $shortcut)) "Shortcut missing: $shortcut"
        $link=$shell.CreateShortcut((Join-Path $shortcuts $shortcut))
        $target=$link.TargetPath
        $target | Set-Content -LiteralPath (Join-Path $debug ($shortcut+'.target.txt'))
        $expectedShortcut=if ($shortcut -eq 'Meshway.lnk') { Join-Path $install 'Meshway.exe' } else { Join-Path $install 'unins000.exe' }
        # Inno uses create-shortcut shell APIs; Windows may normalize a target
        # into its existing 8.3 path. Compare the file, not a textual prefix.
        Check ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -eq (Get-FileHash -LiteralPath $expectedShortcut -Algorithm SHA256).Hash) "Shortcut target does not match installed executable: $shortcut"
    }
    Check (@(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -in @('Meshway','meshway-daemon','meshway-vpn-helper') }).Count -eq 0) 'Installer automatically started Meshway networking.'
    Check ((Get-Content -LiteralPath $state -Raw).Trim() -eq $marker) 'Install modified separate private user state.'
    $uninstaller=Join-Path $install 'unins000.exe'
    Check (Test-Path -LiteralPath $uninstaller) 'Uninstaller executable missing.'
    Invoke-Setup $uninstaller @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',("/LOG=`""+(Join-Path $debug 'uninstall.log')+"`""))
    Check (!(Test-Path -LiteralPath (Join-Path $install 'Meshway.exe'))) 'Application remained after uninstall.'
    Check (!(Test-Path $registry)) 'Uninstaller registry entry remained.'
    Check (!(Test-Path $shortcuts)) 'Start menu group remained.'
    Check ((Get-Content -LiteralPath $state -Raw).Trim() -eq $marker) 'Uninstall removed separate private user state.'
    $afterRoutes=Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | ConvertTo-Json -Compress
    $afterDns=Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Sort-Object InterfaceIndex,AddressFamily | ConvertTo-Json -Compress
    Check ($beforeRoutes -eq $afterRoutes) 'Installer modified routing.'
    Check ($beforeDns -eq $afterDns) 'Installer modified DNS.'
    $arm=(Resolve-Path (Join-Path $Staging 'Meshway-setup-arm64.exe')).Path
    $result=@{status='PASS';time=[DateTime]::UtcNow.ToString('o');platform='Windows x64';installer_sha256=(Get-FileHash $installer -Algorithm SHA256).Hash.ToLower();arm64_installer_sha256=(Get-FileHash $arm -Algorithm SHA256).Hash.ToLower();exact_installed_payload=$true;payload_file_count=$payload.Count;per_user_install=$true;start_menu_and_uninstaller=$true;no_auto_start=$true;private_state_preserved=$true;uninstall_cleanup=$true;routes_unchanged=$true;dns_unchanged=$true;arm64_validation='Cross-built Inno installer; actual ARM64 installation requires an ARM64 Windows runner.';platform_signing='Experimental unsigned application/installer; legitimate Authenticode signing not claimed.'}
    New-Item -ItemType Directory -Force (Split-Path $Output -Parent) | Out-Null
    $result | ConvertTo-Json | Set-Content -LiteralPath $Output
    $result | ConvertTo-Json
} finally {
    if (Test-Path (Join-Path $install 'unins000.exe')) { Start-Process -FilePath (Join-Path $install 'unins000.exe') -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART' -Wait }
    Remove-Item -LiteralPath $state -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $directory -Recurse -Force -ErrorAction SilentlyContinue
}
