# Installs the latest lopper release for Windows. Users run it like this.
#
#   irm https://lopper.dev/install.ps1 | iex
#
# $env:LOPPER_VERSION = 'v0.1.0' installs that release instead.
# $env:LOPPER_INSTALL_DIR sets where lopper.exe goes and defaults to
# %LOCALAPPDATA%\Programs\lopper. The script adds that directory to your PATH.
# It checks the archive against the release's checksums.txt before it installs
# anything.
#
# A script block keeps variables out of your session when iex runs it. It fails
# with throw because exit would close your window.
& {
	$ErrorActionPreference = 'Stop'
	# The progress bar slows downloads down many times over.
	$ProgressPreference = 'SilentlyContinue'
	# Windows PowerShell 5.1 may still default to TLS 1.0, which GitHub refuses.
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	$repo = 'https://github.com/shinokamix/lopper'
	$dir = if ($env:LOPPER_INSTALL_DIR) { $env:LOPPER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\lopper' }
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

		# -Path reads [ and ] as wildcards, and a Move-Item whose path matches
		# nothing does nothing, so files go through -LiteralPath.
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
		# Windows cannot replace or delete a running lopper.exe but can rename it.
		# Each install renames it to a name of its own, since the one renamed
		# before may still run, and deletes those that no longer do.
		Copy-Item -LiteralPath "$tmp\x\lopper.exe" $stage
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

	# The script keeps the user's PATH unexpanded because writing it back through
	# [Environment] would freeze %USERPROFILE% in it.
	$key = Get-Item 'HKCU:\Environment'
	$path = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
	if (($path -split ';') -notcontains $dir) {
		Set-ItemProperty 'HKCU:\Environment' Path (($path.TrimEnd(';'), $dir) -join ';' -replace '^;') -Type ExpandString
		# Setting any variable through [Environment] tells running programs,
		# Explorer among them, to reread the environment. This one has a name
		# that no user variable has, and the script removes it right away.
		$ping = "LOPPER_INSTALL_$([Guid]::NewGuid().ToString('N'))"
		[Environment]::SetEnvironmentVariable($ping, '1', 'User')
		[Environment]::SetEnvironmentVariable($ping, $null, 'User')
		Write-Host "Added $dir to your PATH; open a new terminal to run lopper there"
	}
	if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }

	$found = Get-Command lopper -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
	if ($found -and $found.Source -ne $exe) {
		Write-Warning "$($found.Source) comes first in your PATH: lopper runs it, not $exe"
	}
}
