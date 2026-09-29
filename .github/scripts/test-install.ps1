# Runs install.ps1 as `irm … | iex` would: to a relative directory, and
# three times, the second and third while the lopper.exe each replaces is
# still running, the one the second replaced included.
$ErrorActionPreference = 'Stop'
$running = @()
try {
	$script = Get-Content -Raw install.ps1
	$name = "bin-$($PSVersionTable.PSEdition)"
	$dir = Join-Path $env:RUNNER_TEMP $name
	Push-Location $env:RUNNER_TEMP
	$env:LOPPER_INSTALL_DIR = ".\$name"
	try {
		foreach ($i in 1..3) {
			$script | Invoke-Expression
			# Scanning the whole drive keeps this lopper.exe busy for a while.
			$running += Start-Process "$dir\lopper.exe" -ArgumentList 'scan', 'C:\' -PassThru -WindowStyle Hidden
			Start-Sleep -Seconds 1
			if ($running[-1].HasExited) { throw 'lopper.exe exited before the next install could replace it' }
		}
	} finally {
		Pop-Location
	}

	& "$dir\lopper.exe" --version
	if ($LASTEXITCODE) { throw "installed lopper.exe exited with $LASTEXITCODE" }
	$path = (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';'
	if (@($path | Where-Object { $_ -eq $dir }).Count -ne 1 -or $path -contains ".\$name") {
		throw "PATH does not list $dir exactly once, as an absolute path: $path"
	}
} finally {
	$running | Stop-Process -ErrorAction SilentlyContinue
}
