# ney installer for Windows (PowerShell 5.1+ or PowerShell 7).
#
#   irm https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.ps1 | iex
#
# Installs ney.exe to %LOCALAPPDATA%\Programs\ney (no admin), adds it to your
# user PATH, verifies the release SHA-256, then starts `ney init` on a first
# install. Re-running it updates ney in place.
#
# Environment:
#   NEY_VERSION=vX.Y.Z     install that version instead of the latest
#   NEY_INSTALL_DIR=DIR    install somewhere else
#   NEY_NO_MODIFY_PATH=1   never touch the user PATH
#   NEY_NO_INIT=1          do not start `ney init`
#
# Everything lives inside a function and errors are thrown, never `exit`ed:
# under `irm | iex` an `exit` would close the user's PowerShell window.

function Install-Ney {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is very slow with the progress bar on PS 5.1
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  $repo = 'naay99999/neything'

  # --- platform -----------------------------------------------------------
  $cpu = $env:PROCESSOR_ARCHITEW6432 # set when 32-bit PowerShell runs on 64-bit Windows
  if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
  switch ($cpu) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { throw "Unsupported architecture: $cpu" }
  }

  # --- version ------------------------------------------------------------
  $version = $env:NEY_VERSION
  if (-not $version) {
    $latest = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases/latest"
    $version = $latest.tag_name
  }
  if (-not $version) { throw 'Could not determine the latest version (set NEY_VERSION=vX.Y.Z to pin one).' }
  if (-not $version.StartsWith('v')) { $version = "v$version" }
  $bare = $version.Substring(1)

  $fileName  = "ney_${bare}_windows_${arch}.zip"
  $checksums = "ney_${bare}_checksums.txt"
  $baseUrl   = "https://github.com/$repo/releases/download/$version"
  if ($env:NEY_BASE_URL) { $baseUrl = $env:NEY_BASE_URL.TrimEnd('/') } # CI smoke test only

  $installDir = $env:NEY_INSTALL_DIR
  if (-not $installDir) { $installDir = Join-Path $env:LOCALAPPDATA 'Programs\ney' }
  $installDir = $installDir.TrimEnd('\', '/')
  $target = Join-Path $installDir 'ney.exe'

  # State before installing: a finished setup and any ney already on PATH.
  $setupDone = Test-Path (Join-Path $HOME '.ney\setup-complete')
  $prev = Get-Command ney -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
  $prevNey = if ($prev) { $prev.Source } else { $null }

  Write-Host "Installing ney $version (windows/$arch) to $target..."

  # --- download + verify --------------------------------------------------
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("ney-install-" + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $zip = Join-Path $tmp $fileName
    $sums = Join-Path $tmp $checksums
    try {
      Invoke-WebRequest -UseBasicParsing "$baseUrl/$fileName" -OutFile $zip
      Invoke-WebRequest -UseBasicParsing "$baseUrl/$checksums" -OutFile $sums
    } catch {
      throw "Download failed for $version ($fileName): $($_.Exception.Message)"
    }

    $actual = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLowerInvariant()
    $expected = $null
    foreach ($line in Get-Content $sums) {
      $parts = $line.Trim() -split '\s+'
      if ($parts.Count -ge 2 -and ($parts[1] -eq $fileName -or $parts[1] -eq "*$fileName")) {
        $expected = $parts[0].ToLowerInvariant()
        break
      }
    }
    if (-not $expected) { throw "No checksum entry for $fileName in $checksums." }
    if ($actual -ne $expected) {
      throw "CHECKSUM MISMATCH for $fileName - refusing to install.`n  expected: $expected`n  actual:   $actual"
    }

    $unpacked = Join-Path $tmp 'unpacked'
    Expand-Archive -Path $zip -DestinationPath $unpacked -Force
    $newExe = Join-Path $unpacked 'ney.exe'
    if (-not (Test-Path $newExe)) { throw "Archive $fileName has no ney.exe." }

    # --- install ----------------------------------------------------------
    # A running ney.exe (e.g. `ney mcp` inside an agent) cannot be
    # overwritten, but it can be renamed: move it aside, then put the new one
    # in place. The old copy is deleted now if possible, else on the next run.
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Get-ChildItem -Path $installDir -Filter 'ney.exe.old*' -ErrorAction SilentlyContinue |
      Remove-Item -Force -ErrorAction SilentlyContinue
    if (Test-Path $target) {
      $aside = "$target.old-" + [Guid]::NewGuid().ToString('N').Substring(0, 8)
      Move-Item -Path $target -Destination $aside -Force
      Remove-Item -Path $aside -Force -ErrorAction SilentlyContinue
    }
    Copy-Item -Path $newExe -Destination $target -Force
  } finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }

  Write-Host "ney $version installed to $target (checksum verified)."

  # --- PATH ---------------------------------------------------------------
  $sameDir = { param($a, $b) $a.TrimEnd('\', '/') -ieq $b.TrimEnd('\', '/') }
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $userEntries = @(if ($userPath) { $userPath -split ';' | Where-Object { $_ } })
  $inUserPath = @($userEntries | Where-Object { & $sameDir ([Environment]::ExpandEnvironmentVariables($_)) $installDir }).Count -gt 0
  if (-not $inUserPath) {
    if ($env:NEY_NO_MODIFY_PATH) {
      Write-Host ""
      Write-Host "$installDir is not on your PATH. Add it under Settings > System > About > Advanced system settings > Environment Variables."
    } else {
      $newPath = (@($installDir) + $userEntries) -join ';'
      [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
      Write-Host ""
      Write-Host "Added $installDir to your user PATH. Open a new terminal (and restart your AI apps) to use ``ney``."
    }
  }
  # Make `ney` usable in this session right away.
  $sessionEntries = $env:Path -split ';'
  if (@($sessionEntries | Where-Object { $_ -and (& $sameDir $_ $installDir) }).Count -eq 0) {
    $env:Path = "$installDir;$env:Path"
  }

  # --- an older ney elsewhere ---------------------------------------------
  $otherNey = $null
  if ($prevNey -and -not ((Resolve-Path $prevNey).Path -ieq (Resolve-Path $target).Path)) {
    $otherNey = $prevNey
    Write-Host ""
    Write-Warning "Another ney is installed at $otherNey. Remove it so it can't shadow this one:`n  Remove-Item `"$otherNey`""
  }

  # --- setup --------------------------------------------------------------
  # First install, or moving from another location (clients still point at
  # the old binary): run `ney init`. A plain update needs nothing more.
  if ($setupDone -and -not $otherNey) {
    Write-Host ""
    Write-Host "Updated. Your AI clients keep using $target; nothing else to do."
    return
  }
  if ($env:NEY_NO_INIT) {
    Write-Host ""
    Write-Host "Next: run  ney init  to connect Claude Code / Codex."
    return
  }
  if ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
    Write-Host ""
    if ($setupDone) { Write-Host "Re-running setup so your AI clients use $target..." } else { Write-Host 'Starting setup...' }
    Write-Host ""
    & $target init
    return
  }
  Write-Host ""
  Write-Host 'No terminal available for the setup wizard. Next: run  ney init'
}

Install-Ney
