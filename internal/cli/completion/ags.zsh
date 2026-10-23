#compdef ags

_ags() {
  local -a lines candidates prefixes
  local line value description entry
  lines=("${(@f)$(ags __complete "${(@Q)words[2,CURRENT]}" 2>/dev/null)}")
  if [[ ${lines[1]%%$'\t'*} == :files ]]; then
    [[ $PREFIX == -*=* ]] && compset -P '*='
    _files
    return
  fi
  for line in "${lines[@]}"; do
    [[ -n $line ]] || continue
    value=${line%%$'\t'*}
    description=${line#*$'\t'}
    entry=${value//:/\\:}
    [[ -n $description && $description != $line ]] && entry+=":$description"
    if [[ $value == */ ]]; then
      prefixes+=("$entry")
    else
      candidates+=("$entry")
    fi
  done
  if (( ${#prefixes} )); then
    _describe -t values ags candidates -- prefixes -S ''
  else
    _describe -t values ags candidates
  fi
}

if [[ $funcstack[1] == _ags ]]; then
  _ags "$@"
else
  compdef _ags ags
fi
