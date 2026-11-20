# Builds the Exim Connect desktop installer for ONE server and publishes it in ./downloads, from where the web app offers it
# (profile menu > "PC application"). Run it on a Windows PC with Node.js 20+ (it needs Internet access for npm the first time).
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-desktop.ps1 -Url https://eximphone.169-58-114-169.sslip.io:8443 -PinServerCertificate
#
#   -Url                    address of the server the installed app connects to (baked into the installer)
#   -PinServerCertificate   test servers with a self-signed certificate: read the certificate the server presents now and
#                           trust exactly that one (its fingerprint is baked in). Never use it with a certificate from the
#                           bank's CA: Windows already trusts those.
#
# The build runs in C:\exim-desk-build (a short path: long paths break the installer tool) and is reused on later runs.
# The result is copied to ./downloads (Exim-Connect-Setup.exe and manifest.json). Copy that folder to the server (see the end of the output).
param(
  [Parameter(Mandatory = $true)][string]$Url,
  [switch]$PinServerCertificate
)
$ErrorActionPreference = 'Stop'

$uri = [Uri]$Url
if ($uri.Scheme -ne 'https') { throw 'Url must start with https://' }
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root 'desktop'
$work = Join-Path $env:SystemDrive 'exim-desk-build'
$downloads = Join-Path $root 'downloads'

# 1. settings baked into the installer
$defaults = @{ url = $Url.TrimEnd('/') }
if ($PinServerCertificate) {
  $port = if ($uri.Port -gt 0) { $uri.Port } else { 443 }
  $tcp = New-Object System.Net.Sockets.TcpClient($uri.Host, $port)
  $ssl = New-Object System.Net.Security.SslStream($tcp.GetStream(), $false, ([System.Net.Security.RemoteCertificateValidationCallback] { $true }))
  $ssl.AuthenticateAsClient($uri.Host)
  $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($ssl.RemoteCertificate)
  $sha = [System.Security.Cryptography.SHA256]::Create()
  $defaults.certFingerprint = 'sha256/' + [Convert]::ToBase64String($sha.ComputeHash($cert.RawData))
  $ssl.Dispose(); $tcp.Close()
  Write-Host "Pinned certificate: $($cert.Subject), valid until $($cert.NotAfter.ToString('yyyy-MM-dd'))"
}

# 2. copy the sources to the short build folder (node_modules and dist stay there between runs)
New-Item -ItemType Directory -Force -Path $work | Out-Null
$files = 'main.js', 'offline.html', 'preload.js', 'ui-preload.js', 'remote.js', 'keymap.js', 'winDriver.js', 'picker.html', 'banner.html', 'icon.png', 'package.json', 'package-lock.json'
foreach ($f in $files) { Copy-Item -Force (Join-Path $src $f) (Join-Path $work $f) }
[System.IO.File]::WriteAllText((Join-Path $work 'defaults.json'), ($defaults | ConvertTo-Json), (New-Object System.Text.UTF8Encoding($false)))

# 3. build
Push-Location $work
try {
  if (-not (Test-Path (Join-Path $work 'node_modules'))) { npm ci --no-audit --no-fund; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
  npx electron-builder --win
  if ($LASTEXITCODE -ne 0) { throw 'electron-builder failed' }
} finally { Pop-Location }

# 4. publish
$pkg = Get-Content (Join-Path $work 'package.json') -Raw | ConvertFrom-Json
$built = Join-Path $work ("dist\Exim Connect Setup {0}.exe" -f $pkg.version)
if (-not (Test-Path $built)) { throw "installer not found: $built" }
New-Item -ItemType Directory -Force -Path $downloads | Out-Null
$target = Join-Path $downloads 'Exim-Connect-Setup.exe'
Copy-Item -Force $built $target
$hash = (Get-FileHash -Algorithm SHA256 $target).Hash.ToLower()
$manifest = [ordered]@{
  file = 'Exim-Connect-Setup.exe'; version = $pkg.version; size = (Get-Item $target).Length; sha256 = $hash
  server = $defaults.url; pinned = [bool]$PinServerCertificate; published_at = (Get-Date).ToUniversalTime().ToString('o')
}
[System.IO.File]::WriteAllText((Join-Path $downloads 'manifest.json'), ($manifest | ConvertTo-Json), (New-Object System.Text.UTF8Encoding($false)))

Write-Host ''
Write-Host "Installer published in $downloads" -ForegroundColor Green
Write-Host "  Exim-Connect-Setup.exe  ($([math]::Round($manifest.size / 1MB, 1)) MB, SHA-256 $hash)"
Write-Host ''
Write-Host 'Next: put both files on the server, in the downloads folder of the project, for example:'
Write-Host "  scp downloads\Exim-Connect-Setup.exe downloads\manifest.json root@<server>:~/EximPhone/downloads/"
Write-Host 'The web app then shows the download in the profile menu (nothing to restart).'
