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
		# With or without the v of the tag: 0.1.0 is v0.1.0.
		$tag = 'v' + $env:LOPPER_VERSION.TrimStart('v')
	} else {
		# releases/latest redirects to releases/tag/<latest tag>.
		$req = [Net.WebRequest]::Create("$repo/releases/latest")
		$req.Method = 'HEAD'
		$req.AllowAutoRedirect = $false
		try {
			$res = $req.GetResponse()
		} catch {
			throw "lopper install: cannot reach ${repo}: $($_.Exception.GetBaseException().Message)"
		}
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
		foreach ($asset in $archive, 'checksums.txt') {
			try {
				Invoke-WebRequest -UseBasicParsing "$repo/releases/download/$tag/$asset" -OutFile "$tmp\$asset"
			} catch {
				throw "lopper install: cannot download $asset of ${tag}: $($_.Exception.Message)"
			}
		}

		# Files are named with -LiteralPath: -Path reads [ and ] as wildcards,
		# and a Move-Item whose path matches nothing silently does nothing.
		$want = Get-Content -LiteralPath "$tmp\checksums.txt" | ForEach-Object {
			$sum, $file = $_ -split '  ', 2
			if ($file -eq $archive) { $sum }
		}
		if (-not $want) { throw "lopper install: $archive is not listed in checksums.txt" }
		if ((Get-FileHash -Algorithm SHA256 -LiteralPath "$tmp\$archive").Hash -ne $want) {
			throw "lopper install: $archive does not match its checksum"
		}

		Expand-Archive -LiteralPath "$tmp\$archive" -DestinationPath "$tmp\x"
		New-Item -ItemType Directory -Force $dir | Out-Null
		# Copied beside the target, then renamed over it. A running
		# lopper.exe cannot be replaced or deleted, but can be renamed away;
		# each install renames it to a name of its own, as the one renamed
		# before may still run. Those that no longer do are deleted here.
		Copy-Item -LiteralPath "$tmp\x\lopper.exe" $stage
		# Run before it replaces anything: an antivirus may have taken it away.
		try {
			& $stage --version | Out-Null
		} catch {
			throw "lopper install: the downloaded lopper.exe does not run: $_"
		}
		if ($LASTEXITCODE) { throw "lopper install: the downloaded lopper.exe exited with $LASTEXITCODE" }
		$exe = Join-Path $dir 'lopper.exe'
		Get-ChildItem -LiteralPath $dir -Filter '.lopper-*.old' -Force | Remove-Item -Force -ErrorAction SilentlyContinue
		$old = $null
		if (Test-Path -LiteralPath $exe) {
			$old = Join-Path $dir ".lopper-$([Guid]::NewGuid()).old"
			Move-Item -LiteralPath $exe $old
		}
		try {
			Move-Item -LiteralPath $stage $exe
		} catch {
			if ($old) { Move-Item -LiteralPath $old $exe }
			throw
		}
		Write-Host "Installed lopper $tag to $exe"
	} finally {
		Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue
		Remove-Item -Force -LiteralPath $stage -ErrorAction SilentlyContinue
	}

	# The user's PATH is read and written as stored, unexpanded: written
	# back through [Environment], %USERPROFILE% in it would be frozen.
	$key = Get-Item 'HKCU:\Environment'
	$path = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
	if (($path -split ';') -notcontains $dir) {
		Set-ItemProperty 'HKCU:\Environment' Path (($path.TrimEnd(';'), $dir) -join ';' -replace '^;') -Type ExpandString
		# Setting any variable through [Environment] tells running programs,
		# Explorer among them, to reread the environment; this one, named so
		# that it cannot be one of the user's, goes again.
		$ping = "LOPPER_INSTALL_$([Guid]::NewGuid().ToString('N'))"
		[Environment]::SetEnvironmentVariable($ping, '1', 'User')
		[Environment]::SetEnvironmentVariable($ping, $null, 'User')
		Write-Host "Added $dir to your PATH; open a new terminal to run lopper there"
	}
	# This terminal may have been opened before $dir went into PATH, by this
	# install or an earlier one.
	if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }

	# A lopper.exe installed some other way would still run instead.
	$found = Get-Command lopper -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
	if ($found -and $found.Source -ne $exe) {
		Write-Warning "$($found.Source) comes first in your PATH: lopper runs it, not $exe"
	}
}
