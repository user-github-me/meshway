param(
    [Parameter(Mandatory = $true)][string]$Directory,
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Fa-f0-9]{40}$')][string]$Thumbprint,
    [Parameter(Mandatory = $true)][ValidatePattern('^https://')][string]$TimestampUrl,
    [string]$SignTool = "signtool.exe"
)
$ErrorActionPreference = "Stop"
if (-not $IsWindows -and $env:OS -ne "Windows_NT") { throw "Authenticode signing requires Windows." }
$Files = @(Get-ChildItem -LiteralPath $Directory -Recurse -File | Where-Object { $_.Extension -in '.exe', '.dll', '.msi', '.sys' })
if ($Files.Count -eq 0) { throw "No Windows signing artifacts found." }
foreach ($File in $Files) {
    $Existing = Get-AuthenticodeSignature -LiteralPath $File.FullName
    if ($Existing.Status -eq 'Valid') {
        # Preserve verified upstream runtime/driver signatures, including Wintun.
        & $SignTool verify /pa /all $File.FullName
        if ($LASTEXITCODE -ne 0) { throw "Existing upstream signature verification failed." }
        continue
    }
    # Use only the explicitly supplied certificate; never enumerate stores or print keys.
    & $SignTool sign /sha1 $Thumbprint /fd SHA256 /tr $TimestampUrl /td SHA256 $File.FullName
    if ($LASTEXITCODE -ne 0) { throw "Authenticode signing failed." }
    & $SignTool verify /pa /all $File.FullName
    if ($LASTEXITCODE -ne 0) { throw "Authenticode verification failed." }
}
Write-Output "Windows artifact signatures verified."
