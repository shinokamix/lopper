# Runs install.ps1, as `irm … | iex` would, against the snapshot in dist/
# served as a release: twice, the second time over the first install.
$ErrorActionPreference = 'Stop'
$server = Start-Process python -ArgumentList '.github/scripts/release-server.py', 'dist', '8765' -PassThru -NoNewWindow
try {
	foreach ($i in 1..50) {
		try { Invoke-WebRequest -UseBasicParsing -Method Head 'http://127.0.0.1:8765/releases/tag/v0.0.0' | Out-Null; break }
		catch { Start-Sleep -Milliseconds 200 }
	}
	$script = (Get-Content -Raw install.ps1).Replace("'https://github.com/shinokamix/lopper'", "'http://127.0.0.1:8765'")
	$env:LOPPER_INSTALL_DIR = Join-Path $env:RUNNER_TEMP "bin-$($PSVersionTable.PSEdition)"
	foreach ($i in 1..2) { $script | Invoke-Expression }

	& "$env:LOPPER_INSTALL_DIR\lopper.exe" --version
	if ($LASTEXITCODE) { throw "installed lopper.exe exited with $LASTEXITCODE" }
	$path = (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';'
	if (@($path | Where-Object { $_ -eq $env:LOPPER_INSTALL_DIR }).Count -ne 1) {
		throw "PATH does not list $env:LOPPER_INSTALL_DIR exactly once: $path"
	}
} finally {
	Stop-Process $server
}
