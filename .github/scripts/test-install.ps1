# Runs install.ps1 as `irm … | iex` would, into a relative directory with
# [ ] in its name: a release pinned without its v, then the latest twice
# while the lopper.exe each replaces is still running, the one the second
# replaced included. Each install runs as if from a terminal opened before
# the first, with another lopper earlier in PATH to be warned about.
$ErrorActionPreference = 'Stop'
$running = @()
try {
	$script = Get-Content -Raw install.ps1
	$name = "bin[$($PSVersionTable.PSEdition)]"
	$dir = Join-Path $env:RUNNER_TEMP $name
	$other = Join-Path $env:RUNNER_TEMP "other-$($PSVersionTable.PSEdition)"
	New-Item -ItemType Directory -Force $other | Out-Null
	Set-Content "$other\lopper.cmd" '@echo off'
	$fresh = "$other;$env:Path"
	Push-Location $env:RUNNER_TEMP
	$env:LOPPER_INSTALL_DIR = ".\$name"
	try {
		$env:LOPPER_VERSION = '0.1.0-rc.1'
		$env:Path = $fresh
		$script | Invoke-Expression
		$version = & "$dir\lopper.exe" --version
		if ($version -notlike '* 0.1.0-rc.1 *') { throw "LOPPER_VERSION=0.1.0-rc.1 installed $version" }
		$env:LOPPER_VERSION = $null

		foreach ($i in 1..2) {
			# Scanning the whole drive keeps this lopper.exe busy for a while.
			# Start-Process would read the [ ] in its path as wildcards.
			$start = New-Object Diagnostics.ProcessStartInfo "$dir\lopper.exe", 'scan C:\'
			$start.UseShellExecute = $false
			$start.CreateNoWindow = $true
			$running += [Diagnostics.Process]::Start($start)
			Start-Sleep -Seconds 1
			if ($running[-1].HasExited) { throw 'lopper.exe exited before the next install could replace it' }
			$env:Path = $fresh
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
	if (($env:Path -split ';') -notcontains $dir) { throw "an install already in PATH left this terminal without $dir" }
	$path = (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';'
	if (@($path | Where-Object { $_ -eq $dir }).Count -ne 1 -or $path -contains ".\$name") {
		throw "PATH does not list $dir exactly once, as an absolute path: $path"
	}
	$left = Get-ChildItem -LiteralPath $dir -Force | Where-Object { $_.Name -notlike '.lopper-*.old' }
	if ("$($left.Name)" -ne 'lopper.exe') { throw "$dir holds more than lopper.exe and renamed ones: $($left.Name)" }
} finally {
	$running | Stop-Process -ErrorAction SilentlyContinue
}
