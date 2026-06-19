if not set -q HUNCH_BIN
	set -g HUNCH_BIN hunch
end
set -g _hunch_prev1 ""
set -g _hunch_prev2 ""
set -g _hunch_suggestion ""

function _hunch_daemon_ensure --on-event fish_prompt
	functions -e _hunch_daemon_ensure
	if not $HUNCH_BIN daemon status >/dev/null 2>&1
		$HUNCH_BIN daemon start >/dev/null 2>&1 &
	end
end

function _hunch_record --on-event fish_postexec
	set -l exit_code $status
	set -l cmd $argv[1]
	test -n "$cmd" || return

	$HUNCH_BIN client record \
		--state "$_hunch_prev1,$_hunch_prev2" \
		--next "$cmd" \
		--at (date -u +%Y-%m-%dT%H:%M:%SZ) \
		>/dev/null 2>&1 &

	set _hunch_prev1 $_hunch_prev2
	set _hunch_prev2 $cmd
end

function _hunch_predict
	set -l cmdline (commandline -p)
	test -n "$cmdline" || return

	set -l suggestion
	set suggestion ($HUNCH_BIN client predict \
		--state "$_hunch_prev1,$_hunch_prev2" \
		--prefix "$cmdline" \
		--limit 1 \
		--template 2>/dev/null)

	if test -n "$suggestion" -a "$suggestion" != "$cmdline"
		set -g _hunch_suggestion $suggestion
	else
		set -g _hunch_suggestion ""
	end
end

function _hunch_accept
	if test -n "$_hunch_suggestion"
		commandline -r "$_hunch_suggestion"
		commandline -C (string length "$_hunch_suggestion")
		set -g _hunch_suggestion ""
	end
end

function _hunch_right
	set -l cl (commandline -p)
	set -l full (commandline)
	if test (string length "$cl") -eq (string length "$full") -a -n "$_hunch_suggestion"
		_hunch_accept
	else
		commandline -f forward-char
	end
end

function _hunch_end
	if test -n "$_hunch_suggestion"
		_hunch_accept
	end
end

bind right _hunch_right
bind end _hunch_end
bind --mode default right _hunch_right
bind --mode default end _hunch_end
bind --mode visual right _hunch_right
bind --mode visual end _hunch_end
