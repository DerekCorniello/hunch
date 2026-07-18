_HUNCH_BIN=${HUNCH_BIN:-hunch}
_HUNCH_PREV1=""
_HUNCH_PREV2=""
_HUNCH_PREV_OUTCOME=""
_HUNCH_LAST_HNUM=""

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1 &
		disown 2>/dev/null
	fi
}
_hunch_daemon_ensure

# _hunch_outcome echoes the outcome for an exit code: 0 is success; a signal
# kill (128 < code <= 192) is neutral (empty); any other non-zero is failure.
_hunch_outcome() {
	if ((${1} == 0)); then
		echo success
	elif ((${1} > 128 && ${1} <= 192)); then
		echo ""
	else
		echo failure
	fi
}

# _hunch_hint prints a dim one-line hint for the most likely next command.
# bash has no inline ghost-text primitive, so hunch shows a post-command hint
# rather than fighting readline. Set HUNCH_HINT=0 to disable.
_hunch_hint() {
	[[ ${HUNCH_HINT:-1} == 0 ]] && return
	local s
	s=$("$_HUNCH_BIN" client predict \
		--state "$_HUNCH_PREV1,$_HUNCH_PREV2" \
		--cwd "$PWD" \
		--prior-outcome "$_HUNCH_PREV_OUTCOME" \
		--limit 1 --raw 2>/dev/null)
	[[ -n $s ]] && printf '\033[2mhunch > %s\033[0m\n' "$s"
}

# _hunch_prompt runs before each prompt. It records the command that just ran
# (detected by a change in the history number, so consecutive duplicates are
# still recorded but pressing Enter on an empty line is not) and prints a hint.
_hunch_prompt() {
	local exit_code=$?
	local hnum cmd outcome
	read -r hnum cmd <<<"$(HISTTIMEFORMAT='' history 1)"

	if [[ -n $cmd && $hnum != "$_HUNCH_LAST_HNUM" ]]; then
		_HUNCH_LAST_HNUM=$hnum
		outcome=$(_hunch_outcome "$exit_code")
		"$_HUNCH_BIN" client record \
			--state "$_HUNCH_PREV1,$_HUNCH_PREV2" \
			--next "$cmd" \
			--cwd "$PWD" \
			--outcome "$outcome" \
			--prior-outcome "$_HUNCH_PREV_OUTCOME" \
			--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
			>/dev/null 2>&1 &
		disown 2>/dev/null

		_HUNCH_PREV1=$_HUNCH_PREV2
		_HUNCH_PREV2=$cmd
		_HUNCH_PREV_OUTCOME=$outcome
	fi

	_hunch_hint
}

if [[ -z $PROMPT_COMMAND ]]; then
	PROMPT_COMMAND="_hunch_prompt"
elif [[ $PROMPT_COMMAND != *"_hunch_prompt"* ]]; then
	PROMPT_COMMAND="_hunch_prompt; $PROMPT_COMMAND"
fi
