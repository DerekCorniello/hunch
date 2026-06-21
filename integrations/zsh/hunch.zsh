_HUNCH_BIN=${HUNCH_BIN:-hunch}
typeset -ga _HUNCH_PREV
_HUNCH_PREV=("" "")
_HUNCH_SUGGESTION=""
_HUNCH_LAST_BUFFER=""
_HUNCH_LAST_PREFIX=""
_HUNCH_LAST_POSTDISPLAY=""
_HUNCH_LAST_SUGGESTION=""
_HUNCH_HIGHLIGHT_STYLE=${HUNCH_HIGHLIGHT_STYLE:-${ZSH_AUTOSUGGEST_HIGHLIGHT_STYLE:-fg=8}}

_hunch_clear_display() {
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	POSTDISPLAY=""
	_HUNCH_LAST_PREFIX=""
	_HUNCH_LAST_POSTDISPLAY=""
	_HUNCH_LAST_SUGGESTION=""
}

_hunch_show_display() {
	local prefix="$1"
	local display="$2"
	local suggestion="$3"

	if [[ "$display" == "$_HUNCH_LAST_POSTDISPLAY" && "$suggestion" == "$_HUNCH_LAST_SUGGESTION" ]]; then
		_HUNCH_LAST_PREFIX="$prefix"
		return
	fi

	_HUNCH_LAST_PREFIX="$prefix"
	POSTDISPLAY="$display"
	_HUNCH_LAST_POSTDISPLAY="$display"
	_HUNCH_LAST_SUGGESTION="$suggestion"
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	region_highlight+=("$CURSOR $((CURSOR + $#display)) $_HUNCH_HIGHLIGHT_STYLE memo=hunch")
}

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1
	fi
}
_hunch_daemon_ensure

_hunch_record() {
	local cmd="${history[$((HISTCMD-1))]}"
	[[ -z "$cmd" ]] && return

	"$_HUNCH_BIN" client record \
		--state "${(j.,.)_HUNCH_PREV}" \
		--next "$cmd" \
		--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>/dev/null 2>&1 &!

	_HUNCH_PREV[1]=$_HUNCH_PREV[2]
	_HUNCH_PREV[2]=$cmd
}

_hunch_predict() {
	if [[ -n "$_HUNCH_LAST_SUGGESTION" && "$BUFFER" == "$_HUNCH_LAST_SUGGESTION" ]]; then
		_hunch_clear_display
		return
	fi

	local suggestion display
	if [[ -z "$BUFFER" ]]; then
		suggestion=$("$_HUNCH_BIN" client predict \
			--state "${(j.,.)_HUNCH_PREV}" \
			--limit 1 \
			--raw 2>/dev/null)

		if [[ -n "$suggestion" ]]; then
			display="$suggestion"
		else
			display=""
		fi
		_HUNCH_LAST_BUFFER="$BUFFER"
		if [[ -n "$display" ]]; then
			_hunch_show_display "$BUFFER" "$display" "$suggestion"
		else
			_hunch_clear_display
		fi
		return
	fi

	if [[ "$BUFFER" == "$_HUNCH_LAST_BUFFER" ]]; then
		return
	fi
	_HUNCH_LAST_BUFFER="$BUFFER"

	suggestion=$("$_HUNCH_BIN" client predict \
		--state "${(j.,.)_HUNCH_PREV}" \
		--prefix "$BUFFER" \
		--limit 1 \
		--raw 2>/dev/null)

	if [[ -n "$suggestion" ]]; then
		display="${suggestion#$BUFFER}"
		if [[ "$display" == "$suggestion" ]]; then
			display=""
		fi
	else
		display=""
	fi

	if [[ -n "$display" ]]; then
		_hunch_show_display "$BUFFER" "$display" "$suggestion"
	else
		_hunch_clear_display
	fi
}

_hunch_accept_or_forward() {
	if [[ CURSOR -eq ${#BUFFER} && -n "$POSTDISPLAY" && "$BUFFER" == "$_HUNCH_LAST_PREFIX" ]]; then
		local suffix="$POSTDISPLAY"
		_hunch_clear_display
		BUFFER="$BUFFER$suffix"
		CURSOR=${#BUFFER}
		_HUNCH_LAST_BUFFER=""
		if (( $+functions[_zsh_highlight] )); then
			_zsh_highlight
		fi
	else
		zle .forward-char
	fi
}

_hunch_accept_end() {
	if [[ -n "$POSTDISPLAY" && "$BUFFER" == "$_HUNCH_LAST_PREFIX" ]]; then
		local suffix="$POSTDISPLAY"
		_hunch_clear_display
		BUFFER="$BUFFER$suffix"
		CURSOR=${#BUFFER}
		_HUNCH_LAST_BUFFER=""
		if (( $+functions[_zsh_highlight] )); then
			_zsh_highlight
		fi
	fi
}

_hunch_line_finish() {
	_hunch_clear_display
	_HUNCH_SUGGESTION=""
	_HUNCH_LAST_BUFFER=""
}

zle -N _hunch_accept_or_forward
zle -N _hunch_accept_end
zle -N _hunch_predict
zle -N _hunch_line_finish

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

  _hunch_predict
}

zle -N zle-line-init _hunch_on_line_init
zle -N zle-line-finish _hunch_line_finish

_hunch_pre_redraw() {
  if zle -l _hunch_orig_pre_redraw 2>/dev/null; then
    zle _hunch_orig_pre_redraw
  fi
  _hunch_predict
}

bindkey '^[[C' _hunch_accept_or_forward
bindkey '^[[F' _hunch_accept_end

bindkey -M vicmd '^[[C' _hunch_accept_or_forward
bindkey -M vicmd '^[[F' _hunch_accept_end

bindkey -M viins '^[[C' _hunch_accept_or_forward
bindkey -M viins '^[[F' _hunch_accept_end

autoload -Uz add-zsh-hook
add-zsh-hook precmd _hunch_record
