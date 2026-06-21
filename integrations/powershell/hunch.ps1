# Hunch requires PowerShell 7.4+
if ($PSVersionTable.PSVersion.Major -lt 7 -or ($PSVersionTable.PSVersion.Major -eq 7 -and $PSVersionTable.PSVersion.Minor -lt 4)) {
	Write-Warning "Hunch requires PowerShell 7.4+. Current: $($PSVersionTable.PSVersion). Integration will not load."
	return
}
# PSReadLine 2.3+ is required for inline predictions.
if (-not (Get-Module -ListAvailable PSReadLine | Where-Object { $_.Version.Major -ge 2 -and $_.Version.Minor -ge 3 })) {
	Write-Warning "Hunch requires PSReadLine 2.3+. Install: Install-Module PSReadLine -Force -Scope CurrentUser"
	return
}

$HunchBin = if ($env:HUNCH_BIN) { $env:HUNCH_BIN } else { "hunch" }
$script:HunchPrev1 = ""
$script:HunchPrev2 = ""
$script:HunchSuggestion = ""
$script:HunchPrefix = ""

function Invoke-HunchDaemonEnsure {
	if (& $HunchBin daemon status 2>$null) { return }
	Start-Process -FilePath $HunchBin -ArgumentList "daemon", "start" -WindowStyle Hidden
}

function Invoke-HunchRecord {
	param([string]$Cmd, [int]$ExitCode)

	if ([string]::IsNullOrEmpty($Cmd)) { return }

	$at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

	Start-Process -FilePath $HunchBin -ArgumentList @(
		"client", "record",
		"--state", "$script:HunchPrev1,$script:HunchPrev2",
		"--next", $Cmd,
		"--at", $at
	) -WindowStyle Hidden -RedirectStandardOutput $null -RedirectStandardError $null

	$script:HunchPrev1 = $script:HunchPrev2
	$script:HunchPrev2 = $Cmd
}

function Invoke-HunchClearSuggestion {
	$script:HunchSuggestion = ""
	$script:HunchPrefix = ""
}

function Invoke-HunchPredict {
	param([string]$Buffer)

	if ([string]::IsNullOrEmpty($Buffer)) {
		Invoke-HunchClearSuggestion
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
			$script:HunchPrefix = $Buffer
			return
		}
	} catch {}
	Invoke-HunchClearSuggestion
}

function Invoke-HunchAccept {
	$line = ""
	$cursor = 0
	[Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)

	Invoke-HunchPredict -Buffer $line
	if ([string]::IsNullOrEmpty($script:HunchSuggestion)) {
		return $false
	}

	if ($line -ne $script:HunchPrefix -or $cursor -ne $line.Length) {
		Invoke-HunchClearSuggestion
		return $false
	}

	[Microsoft.PowerShell.PSConsoleReadLine]::Replace(0, $line.Length, $script:HunchSuggestion)
	[Microsoft.PowerShell.PSConsoleReadLine]::SetCursorPosition($script:HunchSuggestion.Length)
	Invoke-HunchClearSuggestion
	return $true
}

[Microsoft.PowerShell.PSConsoleReadLine]::SetKeyHandler([ConsoleKey]::RightArrow, {
	param($key, $arg)
	if (-not (Invoke-HunchAccept)) {
		[Microsoft.PowerShell.PSConsoleReadLine]::ForwardChar($key, $arg)
	}
})

[Microsoft.PowerShell.PSConsoleReadLine]::SetKeyHandler([ConsoleKey]::End, {
	param($key, $arg)
	if (-not (Invoke-HunchAccept)) {
		[Microsoft.PowerShell.PSConsoleReadLine]::EndOfLine($key, $arg)
	}
})

Set-PSReadLineOption -PredictionSource None

Invoke-HunchDaemonEnsure
