# Runs install.ps1, as `irm … | iex` would, against the snapshot in dist/
# served as a release. It installs to a relative directory, and three
# times: the second and third time while the lopper.exe each replaces is
# still running, the one the second replaced included.
$ErrorActionPreference = 'Stop'
$server = Start-Process python -ArgumentList '.github/scripts/release-server.py', 'dist', '8765' -PassThru -NoNewWindow
$running = @()
try {
	foreach ($i in 1..50) {
		try { Invoke-WebRequest -UseBasicParsing -Method Head 'http://127.0.0.1:8765/releases/tag/v0.0.0' | Out-Null; break }
		catch { Start-Sleep -Milliseconds 200 }
	}
	$script = (Get-Content -Raw install.ps1).Replace("'https://github.com/shinokamix/lopper'", "'http://127.0.0.1:8765'")
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
	Stop-Process $server
}
