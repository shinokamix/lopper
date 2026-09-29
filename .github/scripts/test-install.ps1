# Runs install.ps1 as `irm … | iex` would, into a relative directory: a
# release pinned without its v, then the latest twice while the lopper.exe
# each replaces is still running, the one the second replaced included. A
# lopper earlier in PATH must be warned about.
$ErrorActionPreference = 'Stop'
$running = @()
try {
	$script = Get-Content -Raw install.ps1
	$name = "bin-$($PSVersionTable.PSEdition)"
	$dir = Join-Path $env:RUNNER_TEMP $name
	$other = Join-Path $env:RUNNER_TEMP "other-$($PSVersionTable.PSEdition)"
	New-Item -ItemType Directory -Force $other | Out-Null
	Set-Content "$other\lopper.cmd" '@echo off'
	Push-Location $env:RUNNER_TEMP
	$env:LOPPER_INSTALL_DIR = ".\$name"
	try {
		$env:LOPPER_VERSION = '0.1.0-rc.1'
		$script | Invoke-Expression
		$version = & "$dir\lopper.exe" --version
		if ($version -notlike '* 0.1.0-rc.1 *') { throw "LOPPER_VERSION=0.1.0-rc.1 installed $version" }
		$env:LOPPER_VERSION = $null

		$env:Path = "$other;$env:Path"
		foreach ($i in 1..2) {
			# Scanning the whole drive keeps this lopper.exe busy for a while.
			$running += Start-Process "$dir\lopper.exe" -ArgumentList 'scan', 'C:\' -PassThru -WindowStyle Hidden
			Start-Sleep -Seconds 1
			if ($running[-1].HasExited) { throw 'lopper.exe exited before the next install could replace it' }
			$warnings = $script | Invoke-Expression 3>&1
		}
	} finally {
		Pop-Location
	}

	$version = & "$dir\lopper.exe" --version
	if ($LASTEXITCODE) { throw "installed lopper.exe exited with $LASTEXITCODE" }
	if ($version -like '*rc.1*') { throw "the latest release did not replace $version" }
	if ("$warnings" -notlike "*$other\lopper.cmd comes first in your PATH*") {
		throw "no warning about $other\lopper.cmd, got: $warnings"
	}
	$path = (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';'
	if (@($path | Where-Object { $_ -eq $dir }).Count -ne 1 -or $path -contains ".\$name") {
		throw "PATH does not list $dir exactly once, as an absolute path: $path"
	}
} finally {
	$running | Stop-Process -ErrorAction SilentlyContinue
}
