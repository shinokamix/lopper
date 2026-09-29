# Installs the latest lopper release for Windows:
#
#   irm https://raw.githubusercontent.com/shinokamix/lopper/main/install.ps1 | iex
#
# $env:LOPPER_VERSION = 'v0.1.0' installs that release instead, and
# $env:LOPPER_INSTALL_DIR (default %LOCALAPPDATA%\Programs\lopper) is where
# lopper.exe goes; that directory is added to your PATH. The archive is
# checked against the release's checksums.txt before anything is installed.
#
# Runs in a script block so that, run with iex, it leaves no variables in
# your session, and fails with throw: exit would close your window.
& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue' # the progress bar slows downloads down many times over
	# Windows PowerShell 5.1 may still default to TLS 1.0, which GitHub refuses.
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	$repo = 'https://github.com/shinokamix/lopper'
	$dir = if ($env:LOPPER_INSTALL_DIR) { $env:LOPPER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\lopper' }
	# Absolute, from PowerShell's location: it goes into PATH, where .\bin
	# would mean another directory in every terminal.
	$dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)

	# A 32-bit PowerShell on 64-bit Windows reports x86 here, the system's
	# architecture in PROCESSOR_ARCHITEW6432.
	$cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	$arch = switch ($cpu) {
		'AMD64' { 'amd64' }
		'ARM64' { 'arm64' }
		default { throw "lopper install: unsupported architecture $cpu; see $repo/releases" }
	}

	if ($env:LOPPER_VERSION) {
		$tag = $env:LOPPER_VERSION
	} else {
		# releases/latest redirects to releases/tag/<latest tag>.
		$req = [Net.WebRequest]::Create("$repo/releases/latest")
		$req.Method = 'HEAD'
		$req.AllowAutoRedirect = $false
		$res = $req.GetResponse()
		$tag = ([string]$res.Headers['Location']).TrimEnd('/').Split('/')[-1]
		$res.Close()
		if ($tag -notlike 'v*') { throw "lopper install: no release found at $repo/releases" }
	}

	$archive = "lopper_windows_$arch.zip"
	$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
	$stage = Join-Path $dir ".lopper-$([Guid]::NewGuid()).exe"
	New-Item -ItemType Directory -Force $tmp | Out-Null
	try {
		Write-Host "Downloading lopper $tag for windows/$arch"
		Invoke-WebRequest -UseBasicParsing "$repo/releases/download/$tag/$archive" -OutFile "$tmp\$archive"
		Invoke-WebRequest -UseBasicParsing "$repo/releases/download/$tag/checksums.txt" -OutFile "$tmp\checksums.txt"

		$want = Get-Content "$tmp\checksums.txt" | ForEach-Object {
			$sum, $file = $_ -split '  ', 2
			if ($file -eq $archive) { $sum }
		}
		if (-not $want) { throw "lopper install: $archive is not listed in checksums.txt" }
		if ((Get-FileHash -Algorithm SHA256 "$tmp\$archive").Hash -ne $want) {
			throw "lopper install: $archive does not match its checksum"
		}

		Expand-Archive "$tmp\$archive" -DestinationPath "$tmp\x"
		New-Item -ItemType Directory -Force $dir | Out-Null
		# Copied beside the target, then renamed over it. A running
		# lopper.exe cannot be replaced or deleted, but can be renamed away;
		# each install renames it to a name of its own, as the one renamed
		# before may still run. Those that no longer do are deleted here.
		Copy-Item "$tmp\x\lopper.exe" $stage
		$exe = Join-Path $dir 'lopper.exe'
		Get-ChildItem $dir -Filter '.lopper-*.old' -Force | Remove-Item -Force -ErrorAction SilentlyContinue
		$old = $null
		if (Test-Path $exe) {
			$old = Join-Path $dir ".lopper-$([Guid]::NewGuid()).old"
			Move-Item $exe $old
		}
		try {
			Move-Item $stage $exe
		} catch {
			if ($old) { Move-Item $old $exe }
			throw
		}
		Write-Host "Installed lopper $tag to $exe"
	} finally {
		Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
		Remove-Item -Force $stage -ErrorAction SilentlyContinue
	}

	# The user's PATH is read and written as stored, unexpanded: written
	# back through [Environment], %USERPROFILE% in it would be frozen.
	$key = Get-Item 'HKCU:\Environment'
	$path = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
	if (($path -split ';') -notcontains $dir) {
		Set-ItemProperty 'HKCU:\Environment' Path (($path.TrimEnd(';'), $dir) -join ';' -replace '^;') -Type ExpandString
		# Setting any variable through [Environment] tells running programs,
		# Explorer among them, to reread the environment; this one goes again.
		[Environment]::SetEnvironmentVariable('LOPPER_INSTALL', '1', 'User')
		[Environment]::SetEnvironmentVariable('LOPPER_INSTALL', $null, 'User')
		$env:Path = "$env:Path;$dir"
		Write-Host "Added $dir to your PATH; open a new terminal to run lopper there"
	}
}
