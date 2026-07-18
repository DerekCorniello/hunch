if not set -q HUNCH_BIN
	set -g HUNCH_BIN hunch
end
set -g _hunch_prev1 ""
set -g _hunch_prev2 ""
set -g _hunch_prev_outcome ""

function _hunch_daemon_ensure --on-event fish_prompt
	functions -e _hunch_daemon_ensure
	if not $HUNCH_BIN daemon status >/dev/null 2>&1
		$HUNCH_BIN daemon start >/dev/null 2>&1 &
	end
end

# _hunch_outcome echoes the outcome for an exit code: 0 is success; a signal
# kill (128 < code <= 192) is neutral (empty); any other non-zero is failure.
function _hunch_outcome
	if test $argv[1] -eq 0
		echo success
	else if test $argv[1] -gt 128 -a $argv[1] -le 192
		echo ""
	else
		echo failure
	end
end

# _hunch_hint prints a dim one-line hint for the most likely next command.
# fish's native autosuggestion engine owns inline ghost text, so hunch shows a
# post-command hint instead. Set HUNCH_HINT=0 to disable.
function _hunch_hint
	test "$HUNCH_HINT" = 0; and return
	set -l s ($HUNCH_BIN client predict \
		--state "$_hunch_prev1,$_hunch_prev2" \
		--cwd "$PWD" \
		--prior-outcome "$_hunch_prev_outcome" \
		--limit 1 --raw 2>/dev/null)
	test -n "$s"; and echo (set_color brblack)"hunch > $s"(set_color normal)
end

function _hunch_record --on-event fish_postexec
	set -l exit_code $status
	set -l cmd $argv[1]
	test -n "$cmd"; or return

	set -l outcome (_hunch_outcome $exit_code)
	$HUNCH_BIN client record \
		--state "$_hunch_prev1,$_hunch_prev2" \
		--next "$cmd" \
		--cwd "$PWD" \
		--outcome "$outcome" \
		--prior-outcome "$_hunch_prev_outcome" \
		--at (date -u +%Y-%m-%dT%H:%M:%SZ) \
		>/dev/null 2>&1 &

	set _hunch_prev1 $_hunch_prev2
	set _hunch_prev2 $cmd
	set _hunch_prev_outcome $outcome

	_hunch_hint
end
