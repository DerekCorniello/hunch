# Hunch PowerShell integration.
#
# PowerShell's native inline predictor (PSReadLine ICommandPredictor) requires
# a compiled binary module, which this script-only integration cannot provide,
# so hunch shows a dim post-command hint for the most likely next command
# instead. Set $env:HUNCH_HINT = '0' to disable the hint.

$HunchBin = if ($env:HUNCH_BIN) { $env:HUNCH_BIN } else { "hunch" }
$script:HunchPrev1 = ""
$script:HunchPrev2 = ""
$script:HunchPrevOutcome = ""
$script:HunchLastId = -1

function Invoke-HunchDaemonEnsure {
	if (& $HunchBin daemon status 2>$null) { return }
	Start-Process -FilePath $HunchBin -ArgumentList "daemon", "start" -WindowStyle Hidden
}

function Invoke-HunchHint {
	if ($env:HUNCH_HINT -eq '0') { return }
	try {
		$s = & $HunchBin client predict `
			--state "$script:HunchPrev1,$script:HunchPrev2" `
			--cwd "$PWD" `
			--prior-outcome "$script:HunchPrevOutcome" `
			--limit 1 --raw 2>$null
		if ($s) { Write-Host "hunch > $s" -ForegroundColor DarkGray }
	} catch {}
}

function Invoke-HunchPrompt {
	param([bool]$Ok)

	$last = Get-History -Count 1
	if ($last -and $last.Id -ne $script:HunchLastId) {
		$script:HunchLastId = $last.Id
		$cmd = $last.CommandLine
		if (-not [string]::IsNullOrWhiteSpace($cmd)) {
			$outcome = if ($Ok) { "success" } else { "failure" }
			$at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
			Start-Process -FilePath $HunchBin -ArgumentList @(
				"client", "record",
				"--state", "$script:HunchPrev1,$script:HunchPrev2",
				"--next", $cmd,
				"--cwd", "$PWD",
				"--outcome", $outcome,
				"--prior-outcome", $script:HunchPrevOutcome,
				"--at", $at
			) -WindowStyle Hidden
			$script:HunchPrev1 = $script:HunchPrev2
			$script:HunchPrev2 = $cmd
			$script:HunchPrevOutcome = $outcome
		}
	}
	Invoke-HunchHint
}

# Wrap the existing prompt so hunch records the last command and prints its hint
# before the prompt renders. Guard against double-wrapping if re-sourced.
if (-not $script:HunchPromptInstalled) {
	$script:HunchPromptInstalled = $true
	$script:HunchOrigPrompt = $function:prompt
	function prompt {
		$ok = $?
		Invoke-HunchPrompt -Ok $ok
		if ($script:HunchOrigPrompt) {
			& $script:HunchOrigPrompt
		}
		else {
			"PS $($executionContext.SessionState.Path.CurrentLocation)$('>' * ($nestedPromptLevel + 1)) "
		}
	}
}

Invoke-HunchDaemonEnsure
