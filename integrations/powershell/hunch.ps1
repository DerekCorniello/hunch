$HunchBin = if ($env:HUNCH_BIN) { $env:HUNCH_BIN } else { "hunch" }
# Accept keys: right, end (set $env:_HUNCH_ACCEPT_KEYS env var to override)
if (-not $env:_HUNCH_ACCEPT_KEYS) { $env:_HUNCH_ACCEPT_KEYS = "right,end" }

$script:HunchPrev1 = ""
$script:HunchPrev2 = ""
$script:HunchSuggestion = ""

function Invoke-HunchDaemonEnsure {
	if (& $HunchBin daemon status 2>$null) { return }
	Start-Process -FilePath $HunchBin -ArgumentList "daemon", "start" -WindowStyle Hidden
}

function Invoke-HunchRecord {
	param([string]$Cmd, [int]$ExitCode)

	if ([string]::IsNullOrEmpty($Cmd)) { return }

	$outcome = if ($ExitCode -eq 0) { "success" } else { "failure" }
	$at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

	Start-Process -FilePath $HunchBin -ArgumentList @(
		"client", "record",
		"--state", "$script:HunchPrev1,$script:HunchPrev2",
		"--next", $Cmd,
		"--outcome", $outcome,
		"--cwd", (Get-Location).Path,
		"--at", $at
	) -WindowStyle Hidden -RedirectStandardOutput $null -RedirectStandardError $null

	$script:HunchPrev1 = $script:HunchPrev2
	$script:HunchPrev2 = $Cmd
}

function Invoke-HunchPredict {
	param([string]$Buffer)

	if ([string]::IsNullOrEmpty($Buffer)) {
		$script:HunchSuggestion = ""
		return
	}

	try {
		$suggestion = & $HunchBin client predict `
			--state "$script:HunchPrev1,$script:HunchPrev2" `
			--prefix $Buffer `
			--limit 1 `
			--template 2>$null

		if ($suggestion -and $suggestion -ne $Buffer) {
			$script:HunchSuggestion = $suggestion
			return
		}
	} catch {}
	$script:HunchSuggestion = ""
}

[Microsoft.PowerShell.PSConsoleReadLine]::SetKeyHandler([ConsoleKey]::RightArrow, {
	param($key, $arg)
	if (-not [string]::IsNullOrEmpty($script:HunchSuggestion)) {
		[Microsoft.PowerShell.PSConsoleReadLine]::Replace(0, $script:HunchSuggestion.Length, $script:HunchSuggestion)
		[Microsoft.PowerShell.PSConsoleReadLine]::SetCursorPosition($script:HunchSuggestion.Length)
		$script:HunchSuggestion = ""
	} else {
		[Microsoft.PowerShell.PSConsoleReadLine]::ForwardChar($key, $arg)
	}
})

[Microsoft.PowerShell.PSConsoleReadLine]::SetKeyHandler([ConsoleKey]::End, {
	param($key, $arg)
	if (-not [string]::IsNullOrEmpty($script:HunchSuggestion)) {
		[Microsoft.PowerShell.PSConsoleReadLine]::Replace(0, $script:HunchSuggestion.Length, $script:HunchSuggestion)
		[Microsoft.PowerShell.PSConsoleReadLine]::SetCursorPosition($script:HunchSuggestion.Length)
		$script:HunchSuggestion = ""
	} else {
		[Microsoft.PowerShell.PSConsoleReadLine]::EndOfLine($key, $arg)
	}
})

Set-PSReadLineOption -PredictionSource None

Invoke-HunchDaemonEnsure
