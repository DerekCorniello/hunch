_HUNCH_BIN=${HUNCH_BIN:-hunch}
typeset -ga _HUNCH_PREV
_HUNCH_PREV=("" "")
_HUNCH_HIGHLIGHT_STYLE=${HUNCH_HIGHLIGHT_STYLE:-${ZSH_AUTOSUGGEST_HIGHLIGHT_STYLE:-fg=8}}

# Coprocess state. _HUNCH_R/_HUNCH_W are the numbered fds we own (duped from
# the coproc), _HUNCH_PID is the serve process. _HUNCH_SUGGESTION is the full
# raw command last suggested; _HUNCH_DISPLAY is the ghost suffix currently in
# POSTDISPLAY; _HUNCH_SENT is the buffer value of the last query we sent.
_HUNCH_R=""
_HUNCH_W=""
_HUNCH_PID=""
_HUNCH_SUGGESTION=""
_HUNCH_DISPLAY=""
_HUNCH_SENT=$'\0' # sentinel that no real buffer equals, so the first edit queries
_HUNCH_PREV_OUTCOME="" # outcome of the most recently recorded command
_HUNCH_LAST_SHOWN="" # suggestion on screen when the line was accepted (for acceptance detection)
_HUNCH_LAST_HISTCMD=$HISTCMD # history event at load; the first precmd skips until this advances
typeset -ga _HUNCH_CANDIDATES # ranked raw suggestions that can extend the buffer
_HUNCH_CANDIDATES=()
_HUNCH_CYCLE_IDX=0 # 1-based index of the candidate currently shown
_HUNCH_CYCLE_NEXT_KEY=${HUNCH_CYCLE_NEXT_KEY:-'^[n'} # Alt-n: next suggestion
_HUNCH_CYCLE_PREV_KEY=${HUNCH_CYCLE_PREV_KEY:-'^[p'} # Alt-p: previous suggestion

# _hunch_outcome maps an exit code to an outcome string in REPLY: 0 is
# "success"; a signal kill (128 < code <= 192, e.g. 130 Ctrl-C, 143 SIGTERM)
# is "" (neutral — an interrupted command is not a task failure); any other
# non-zero is "failure".
_hunch_outcome() {
	if (( $1 == 0 )); then
		REPLY="success"
	elif (( $1 > 128 && $1 <= 192 )); then
		REPLY=""
	else
		REPLY="failure"
	fi
}

# _hunch_json_escape escapes a string for inclusion in a JSON string literal,
# leaving the result in REPLY. Backslash is handled first so the escapes it
# introduces are not re-escaped.
_hunch_json_escape() {
	local s=$1
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\t'/\\t}
	s=${s//$'\r'/\\r}
	REPLY=$s
}

# _hunch_json_unescape reverses _hunch_json_escape, leaving the result in
# REPLY. A private-use sentinel stands in for escaped backslashes so a literal
# "\n" (backslash then n) is not mistaken for a newline.
_hunch_json_unescape() {
	local s=$1 sentinel=$''
	s=${s//\\\\/$sentinel}
	s=${s//\\n/$'\n'}
	s=${s//\\t/$'\t'}
	s=${s//\\r/$'\r'}
	s=${s//\\\"/\"}
	s=${s//$sentinel/\\}
	REPLY=$s
}

# _hunch_build_request assembles a serve request JSON object in REPLY from the
# given prefix plus the current state window, working directory, and prior
# outcome.
_hunch_build_request() {
	local prefix=$1 e
	_hunch_json_escape "$prefix"; local p=$REPLY
	local elems=()
	for e in "${_HUNCH_PREV[@]}"; do
		_hunch_json_escape "$e"
		elems+=("\"$REPLY\"")
	done
	local state_json="[${(j.,.)elems}]"
	_hunch_json_escape "$PWD"; local c=$REPLY
	_hunch_json_escape "$_HUNCH_PREV_OUTCOME"; local o=$REPLY
	REPLY="{\"prefix\":\"$p\",\"state\":$state_json,\"cwd\":\"$c\",\"prior_outcome\":\"$o\"}"
}

# _hunch_parse_response extracts the prefix and the raws array from a serve
# response JSON line into _HUNCH_RESP_PREFIX and the _HUNCH_RESP_RAWS array. It
# relies on the daemon's compact, fixed field order
# ({"prefix":"…","raws":["…","…"]}): any quote inside a value is escaped, so
# both the field delimiters and the array element separator ("," ) are
# unambiguous.
_hunch_parse_response() {
	local line=$1
	local rest=${line#\{\"prefix\":\"}
	local pfx=${rest%%\",\"raws\":\[*}
	_hunch_json_unescape "$pfx"; _HUNCH_RESP_PREFIX=$REPLY

	local body=${rest#*\",\"raws\":\[}
	body=${body%\]\}}
	_HUNCH_RESP_RAWS=()
	[[ -z "$body" ]] && return # empty array: no suggestions

	# Strip the outer quotes, turn the "," element separators into newlines
	# (the escaped form never contains a literal "," or newline inside an
	# element), then split and unescape each element.
	body=${body#\"}
	body=${body%\"}
	body=${body//\",\"/$'\n'}
	local elem
	for elem in "${(@f)body}"; do
		_hunch_json_unescape "$elem"
		_HUNCH_RESP_RAWS+=("$REPLY")
	done
}

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1
	fi
}

# _hunch_start_coproc launches the persistent prediction loop and wires its
# stdout into zle via zle -F. Idempotent: a live coproc is left untouched.
_hunch_start_coproc() {
	if [[ -n "$_HUNCH_PID" ]] && kill -0 "$_HUNCH_PID" 2>/dev/null; then
		return 0
	fi
	_hunch_stop_coproc

	# Start the coprocess with job control off so zsh does not print the
	# "[job] pid" start notification (and later death notice) to the terminal.
	# local_options restores the previous setting when this function returns;
	# the already-started coprocess is unaffected, and we manage it by PID.
	setopt local_options no_monitor
	coproc "$_HUNCH_BIN" client serve 2>/dev/null || return 1
	_HUNCH_PID=$!
	# Move the coproc pipe ends to stable numbered fds we control.
	exec {_HUNCH_W}>&p 2>/dev/null || { _hunch_stop_coproc; return 1; }
	exec {_HUNCH_R}<&p 2>/dev/null || { _hunch_stop_coproc; return 1; }
	zle -F "$_HUNCH_R" _hunch_on_response
	return 0
}

_hunch_stop_coproc() {
	if [[ -n "$_HUNCH_R" ]]; then
		zle -F "$_HUNCH_R" 2>/dev/null
		exec {_HUNCH_R}<&- 2>/dev/null
		_HUNCH_R=""
	fi
	if [[ -n "$_HUNCH_W" ]]; then
		exec {_HUNCH_W}>&- 2>/dev/null
		_HUNCH_W=""
	fi
	if [[ -n "$_HUNCH_PID" ]]; then
		kill "$_HUNCH_PID" 2>/dev/null
		_HUNCH_PID=""
	fi
}

_hunch_set_display() {
	local suggestion="$1" display="$2"
	_HUNCH_SUGGESTION="$suggestion"
	_HUNCH_DISPLAY="$display"
	POSTDISPLAY="$display"
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	if [[ -n "$display" ]]; then
		region_highlight+=("$CURSOR $((CURSOR + ${#display})) $_HUNCH_HIGHLIGHT_STYLE memo=hunch")
	fi
}

_hunch_clear_display() {
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	# Only wipe POSTDISPLAY if it still holds our ghost text; another plugin
	# may have set its own suggestion since.
	if [[ -n "$_HUNCH_DISPLAY" && "$POSTDISPLAY" == "$_HUNCH_DISPLAY" ]]; then
		POSTDISPLAY=""
	fi
	_HUNCH_SUGGESTION=""
	_HUNCH_DISPLAY=""
	_HUNCH_CANDIDATES=()
	_HUNCH_CYCLE_IDX=0
}

# _hunch_show_candidate paints the candidate at _HUNCH_CYCLE_IDX as ghost text.
_hunch_show_candidate() {
	local sug=$_HUNCH_CANDIDATES[$_HUNCH_CYCLE_IDX]
	if [[ -z "$BUFFER" ]]; then
		_hunch_set_display "$sug" "$sug"
	else
		_hunch_set_display "$sug" "${sug#$BUFFER}"
	fi
}

# _hunch_query writes one request to the coprocess. Requests are cheap (a pipe
# write), never block on a response, and respawn the coproc once if the pipe
# has broken (e.g. the daemon restarted under it).
_hunch_query() {
	local prefix="$1"
	_hunch_start_coproc || return
	_HUNCH_SENT="$prefix"
	_hunch_build_request "$prefix"
	local req=$REPLY
	if ! print -rn -u "$_HUNCH_W" -- "$req"$'\n' 2>/dev/null; then
		_hunch_stop_coproc
		_hunch_start_coproc || return
		print -rn -u "$_HUNCH_W" -- "$req"$'\n' 2>/dev/null
	fi
}

# _hunch_on_response is the zle -F file-descriptor handler invoked when the
# coprocess has output. Crucially, a -F handler runs WITHOUT access to the line
# editor — $BUFFER and $CURSOR are empty here — so it only reads and parses the
# response, then hands off to the _hunch_apply_response widget, which zsh runs
# with the editor state available. (This is the same pattern zsh-autosuggestions
# uses for its async responses.)
_hunch_on_response() {
	local fd="$1" line
	if ! IFS= read -r -u "$fd" line; then
		# EOF: the coprocess died. It will be respawned on the next query.
		_hunch_stop_coproc
		return
	fi

	_hunch_parse_response "$line"
	zle _hunch_apply_response
}

# _hunch_apply_response runs as a widget (invoked from the fd handler), so
# $BUFFER and $CURSOR are valid. It discards stale responses (the echoed prefix
# no longer matches the buffer) and otherwise repaints the ghost text.
_hunch_apply_response() {
	# Drop stale responses for a buffer the user has already moved past.
	[[ "$_HUNCH_RESP_PREFIX" != "$BUFFER" ]] && return

	# Keep only suggestions that can be shown as ghost text extending the
	# current buffer; these become the cycle candidates.
	_HUNCH_CANDIDATES=()
	local r
	for r in "${_HUNCH_RESP_RAWS[@]}"; do
		if [[ -z "$BUFFER" && -n "$r" ]]; then
			_HUNCH_CANDIDATES+=("$r")
		elif [[ -n "$r" && "$r" != "$BUFFER" && "$r" == "$BUFFER"* ]]; then
			_HUNCH_CANDIDATES+=("$r")
		fi
	done

	if (( ${#_HUNCH_CANDIDATES} > 0 )); then
		_HUNCH_CYCLE_IDX=1
		_hunch_show_candidate
	else
		_hunch_clear_display
	fi
	zle -R
}

# _hunch_cycle_next / _hunch_cycle_prev step through the candidate list, but
# only while hunch owns the on-screen suggestion so the keys stay inert
# otherwise.
_hunch_cycle_next() {
	local n=${#_HUNCH_CANDIDATES}
	if _hunch_owns_suggestion && (( n > 1 )); then
		_HUNCH_CYCLE_IDX=$(( _HUNCH_CYCLE_IDX % n + 1 ))
		_hunch_show_candidate
		zle -R
	fi
}

_hunch_cycle_prev() {
	local n=${#_HUNCH_CANDIDATES}
	if _hunch_owns_suggestion && (( n > 1 )); then
		_HUNCH_CYCLE_IDX=$(( (_HUNCH_CYCLE_IDX - 2 + n) % n + 1 ))
		_hunch_show_candidate
		zle -R
	fi
}

# _hunch_update runs on every redraw. It repaints instantly from the cached
# suggestion when that suggestion still extends the buffer (the common case
# while typing into a suggestion), and fires an async query whenever the
# buffer has changed since the last one.
_hunch_update() {
	if [[ -n "$_HUNCH_SUGGESTION" && -n "$BUFFER" && "$_HUNCH_SUGGESTION" == "$BUFFER"* && "$_HUNCH_SUGGESTION" != "$BUFFER" ]]; then
		_hunch_set_display "$_HUNCH_SUGGESTION" "${_HUNCH_SUGGESTION#$BUFFER}"
	elif [[ -z "$_HUNCH_SUGGESTION" || "$_HUNCH_SUGGESTION" != "$BUFFER"* ]]; then
		if [[ "$BUFFER" != "$_HUNCH_SENT" ]]; then
			_hunch_clear_display
		fi
	fi

	if [[ "$BUFFER" != "$_HUNCH_SENT" ]]; then
		_hunch_query "$BUFFER"
	fi
}

_hunch_accept_full() {
	BUFFER="$BUFFER$_HUNCH_DISPLAY"
	CURSOR=${#BUFFER}
	_hunch_clear_display
	_HUNCH_SENT="$BUFFER"
	if (( $+functions[_zsh_highlight] )); then
		_zsh_highlight
	fi
}

# _hunch_owns_suggestion is true only when hunch is the plugin currently
# showing the ghost text, so accept keys defer to other plugins otherwise.
_hunch_owns_suggestion() {
	[[ $CURSOR -eq ${#BUFFER} && -n "$_HUNCH_DISPLAY" && "$POSTDISPLAY" == "$_HUNCH_DISPLAY" ]]
}

_hunch_accept_or_forward() {
	if _hunch_owns_suggestion; then
		_hunch_accept_full
	else
		zle "${_HUNCH_ORIG_WIDGET[$KEYMAP:^[[C]:-.forward-char}"
	fi
}

_hunch_accept_end() {
	if _hunch_owns_suggestion; then
		_hunch_accept_full
	else
		zle "${_HUNCH_ORIG_WIDGET[$KEYMAP:^[[F]:-.end-of-line}"
	fi
}

_hunch_record() {
	# Capture the exit status first: any command below would overwrite $?.
	local exit_code=$?

	# Skip the synthetic precmd that fires once at shell startup (and any
	# precmd where no new command actually ran). Without this guard the first
	# precmd records a stale history entry — e.g. the rc's own `source
	# .../hunch.zsh` line — as a transition and poisons _HUNCH_PREV, so the
	# first real prediction is made from a bogus state.
	if [[ "$HISTCMD" == "$_HUNCH_LAST_HISTCMD" ]]; then
		return
	fi
	_HUNCH_LAST_HISTCMD=$HISTCMD

	local cmd="${history[$((HISTCMD-1))]}"
	[[ -z "$cmd" ]] && return

	local outcome
	_hunch_outcome $exit_code
	outcome=$REPLY

	"$_HUNCH_BIN" client record \
		--state "${(j.,.)_HUNCH_PREV}" \
		--next "$cmd" \
		--cwd "$PWD" \
		--outcome "$outcome" \
		--prior-outcome "$_HUNCH_PREV_OUTCOME" \
		--suggested "$_HUNCH_LAST_SHOWN" \
		--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>/dev/null 2>&1 &!

	_HUNCH_PREV[1]=$_HUNCH_PREV[2]
	_HUNCH_PREV[2]=$cmd
	_HUNCH_PREV_OUTCOME=$outcome
}

_hunch_line_finish() {
	# Remember the suggestion that was on screen when the user pressed Enter so
	# _hunch_record can report it for acceptance detection. Capture before
	# _hunch_clear_display resets it.
	_HUNCH_LAST_SHOWN="$_HUNCH_SUGGESTION"
	_hunch_clear_display
	_HUNCH_SENT=$'\0'
}

zle -N _hunch_accept_or_forward
zle -N _hunch_accept_end
zle -N _hunch_line_finish
zle -N _hunch_cycle_next
zle -N _hunch_cycle_prev
zle -N _hunch_apply_response

typeset -g _HUNCH_HOOKS_INSTALLED=0

if zle -l zle-line-init 2>/dev/null; then
	zle -A zle-line-init _hunch_orig_line_init
fi

_hunch_on_line_init() {
	if zle -l _hunch_orig_line_init 2>/dev/null; then
		zle _hunch_orig_line_init
	fi

	if (( ! _HUNCH_HOOKS_INSTALLED )); then
		_HUNCH_HOOKS_INSTALLED=1
		if zle -l zle-line-pre-redraw 2>/dev/null; then
			zle -A zle-line-pre-redraw _hunch_orig_pre_redraw
		fi
		zle -N zle-line-pre-redraw _hunch_pre_redraw
	fi

	_HUNCH_SENT=$'\0'
	_hunch_start_coproc
	_hunch_update
}

zle -N zle-line-init _hunch_on_line_init
zle -N zle-line-finish _hunch_line_finish

_hunch_pre_redraw() {
	if zle -l _hunch_orig_pre_redraw 2>/dev/null; then
		zle _hunch_orig_pre_redraw
	fi
	_hunch_update
}

# Remember whatever widget each keymap already had bound to these keys (e.g.
# zsh-autosuggestions' own accept-on-arrow wrapper) so that when hunch has no
# suggestion of its own to offer, it forwards to that widget rather than a raw
# builtin.
typeset -gA _HUNCH_ORIG_WIDGET

_hunch_capture_orig_widget() {
	local keymap="$1" keys="$2" line widget
	line=$(bindkey -M "$keymap" "$keys" 2>/dev/null)
	[[ -z "$line" ]] && return
	widget="${line#* }"
	[[ -n "$widget" && "$widget" != "undefined-key" ]] && _HUNCH_ORIG_WIDGET[$keymap:$keys]="$widget"
}

for _hunch_km in main vicmd viins; do
	_hunch_capture_orig_widget "$_hunch_km" '^[[C'
	_hunch_capture_orig_widget "$_hunch_km" '^[[F'
done
unset _hunch_km

bindkey '^[[C' _hunch_accept_or_forward
bindkey '^[[F' _hunch_accept_end
bindkey -M vicmd '^[[C' _hunch_accept_or_forward
bindkey -M vicmd '^[[F' _hunch_accept_end
bindkey -M viins '^[[C' _hunch_accept_or_forward
bindkey -M viins '^[[F' _hunch_accept_end

# Cycle keys (Alt-n / Alt-p by default) in every keymap.
_hunch_km=""
for _hunch_km in main vicmd viins; do
	bindkey -M "$_hunch_km" "$_HUNCH_CYCLE_NEXT_KEY" _hunch_cycle_next
	bindkey -M "$_hunch_km" "$_HUNCH_CYCLE_PREV_KEY" _hunch_cycle_prev
done
unset _hunch_km

autoload -Uz add-zsh-hook
add-zsh-hook precmd _hunch_record

_hunch_daemon_ensure
