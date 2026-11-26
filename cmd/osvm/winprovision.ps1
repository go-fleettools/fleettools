# osvm image-build-windows: first-logon provisioning, run once by autounattend.xml.
# Progress goes to COM1 so `osvm` can read it from the Nova console log; the
# last step prints the marker and generalizes the image. On any failure the
# machine stays up (no marker, no sysprep) so the build times out, never
# publishing a half-built image.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Say($m) {
    Add-Content -Path C:\osvm-provision.log -Value $m
    try { $p = New-Object System.IO.Ports.SerialPort 'COM1', 115200; $p.Open(); $p.WriteLine("OSVM: $m"); $p.Close() } catch { }
}

function Fetch($url, $out) {
    for ($i = 1; $i -le 10; $i++) {
        try { Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $out; return } catch { Say "download retry $i of ${url}: $_"; Start-Sleep 5 }
    }
    throw "download failed: $url"
}

function Install-Exe($path, $argList) {
    $p = Start-Process -FilePath $path -ArgumentList $argList -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "$path exited $($p.ExitCode)" }
}

function Install-Msi($path, $props) {
    $log = "C:\osvm-msi-$([IO.Path]::GetFileNameWithoutExtension($path)).log"
    $p = Start-Process -FilePath msiexec.exe -ArgumentList (@('/i', "`"$path`"", '/qn', '/norestart', '/l*v', "`"$log`"") + $props) -Wait -PassThru
    if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) { throw "msiexec $path exited $($p.ExitCode), see $log" }
}

try {
    Say 'provision start'
    $work = 'C:\osvm-work'
    New-Item -ItemType Directory -Force -Path $work | Out-Null

    # A CI machine is not a desktop: no sleep, no automatic updates (a Store or
    # Windows update between install and sysprep makes generalize fail).
    powercfg /change standby-timeout-ac 0 | Out-Null
    powercfg /change monitor-timeout-ac 0 | Out-Null
    reg add 'HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU' /v NoAutoUpdate /t REG_DWORD /d 1 /f | Out-Null
    reg add 'HKLM\SOFTWARE\Policies\Microsoft\WindowsStore' /v AutoDownload /t REG_DWORD /d 2 /f | Out-Null
    try { Add-MpPreference -ExclusionPath 'C:\' } catch { Say "defender exclusion refused: $_" }

    Say 'git'
    $rel = Invoke-RestMethod -UseBasicParsing -Uri 'https://api.github.com/repos/git-for-windows/git/releases/latest' -Headers @{ 'User-Agent' = 'osvm' }
    $asset = $rel.assets | Where-Object { $_.name -match '^Git-[0-9.]+-64-bit\.exe$' } | Select-Object -First 1
    if (-not $asset) { throw 'no Git for Windows 64-bit installer in the latest release' }
    Fetch $asset.browser_download_url "$work\git.exe"
    Install-Exe "$work\git.exe" '/VERYSILENT /NORESTART /SUPPRESSMSGBOXES /NOCANCEL /SP-'

    Say 'go'
    $rels = Invoke-RestMethod -UseBasicParsing -Uri 'https://go.dev/dl/?mode=json'
    $file = ($rels | Where-Object { $_.stable } | Select-Object -First 1).files | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'installer' } | Select-Object -First 1
    if (-not $file) { throw 'no Go windows-amd64 installer in the newest stable release' }
    Fetch "https://go.dev/dl/$($file.filename)" "$work\$($file.filename)"
    $got = (Get-FileHash -Algorithm SHA256 "$work\$($file.filename)").Hash.ToLower()
    if ($got -ne $file.sha256) { throw "go sha256 $got is not the published $($file.sha256)" }
    Install-Msi "$work\$($file.filename)" @()

    Say 'cloudbase-init'
    Fetch 'https://cloudbase.it/downloads/CloudbaseInitSetup_Stable_x64.msi' "$work\cloudbase-init.msi"
    Say ("cloudbase-init sha256 " + (Get-FileHash -Algorithm SHA256 "$work\cloudbase-init.msi").Hash.ToLower())
    Install-Msi "$work\cloudbase-init.msi" @('USERNAME=Administrator', 'LOGGINGSERIALPORTNAME=COM1')

    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    Say ((& git --version) -join ' ')
    Say ((& go version) -join ' ')
    $cb = 'C:\Program Files\Cloudbase Solutions\Cloudbase-Init'
    if (-not (Test-Path "$cb\conf\Unattend.xml")) { throw 'cloudbase-init Unattend.xml is missing' }
    if (-not (Get-Service cloudbase-init -ErrorAction SilentlyContinue)) { throw 'cloudbase-init service is missing' }

    # virtio drivers (when the virtio-win CD is attached): staged in the driver
    # store so a virtio NIC or disk can be switched on later; best effort.
    try {
        $vol = Get-Volume | Where-Object { $_.FileSystemLabel -like 'virtio-win*' } | Select-Object -First 1
        if ($vol) {
            $n = 0
            Get-ChildItem -Path "$($vol.DriveLetter):\" -Recurse -Filter '*.inf' | Where-Object { $_.FullName -match '\\w11\\amd64\\' } | ForEach-Object { pnputil /add-driver $_.FullName /install | Out-Null; $n++ }
            Say "virtio drivers staged: $n inf files"
        } else { Say 'no virtio-win CD attached' }
    } catch { Say "virtio staging failed (not fatal): $_" }

    # A fresh firmware (a new instance has an empty NVRAM) boots only the
    # removable-media path of the ESP: give it Windows Boot Manager there.
    $esp = 'S:'
    mountvol $esp /S
    if (-not (Test-Path "$esp\EFI\Microsoft\Boot\bootmgfw.efi")) { throw 'bootmgfw.efi is not on the ESP' }
    New-Item -ItemType Directory -Force -Path "$esp\EFI\Boot" | Out-Null
    Copy-Item "$esp\EFI\Microsoft\Boot\bootmgfw.efi" "$esp\EFI\Boot\bootx64.efi" -Force
    mountvol $esp /D
    Say 'ESP fallback boot path written'

    Remove-Item -Recurse -Force $work
    Say 'OSVM-IMAGE-BUILD-OK'
    Start-Sleep 20
    & "$env:windir\System32\Sysprep\sysprep.exe" /generalize /oobe /shutdown /unattend:"$cb\conf\Unattend.xml"
    Start-Sleep 600
    throw 'sysprep returned without shutting down'
}
catch {
    Say "FAILED: $_"
    foreach ($log in 'C:\Windows\System32\Sysprep\Panther\setuperr.log', 'C:\Windows\System32\Sysprep\Panther\setupact.log') {
        if (Test-Path $log) { Get-Content $log -Tail 25 | ForEach-Object { Say "$([IO.Path]::GetFileName($log)): $_" } }
    }
}
