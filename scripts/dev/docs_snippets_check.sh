#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

DOC_FILES=("$@")
if [ ${#DOC_FILES[@]} -eq 0 ]; then
  mapfile -t DOC_FILES < <(find "${ROOT_DIR}/docs" -type f -name '*.md' | sort)
fi

if [ ${#DOC_FILES[@]} -eq 0 ]; then
  echo "Docs snippet check: no markdown files found." >&2
  exit 0
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT
FENCE='```'

HELP_ERR="${TMP_DIR}/agentlab_help.err"
if ! HELP_OUT="$(cd "${ROOT_DIR}" && go run ./cmd/agentlab --help 2>"${HELP_ERR}")"; then
  cat "${HELP_ERR}" >&2
  echo "Docs snippet check: failed to run 'agentlab --help'." >&2
  exit 1
fi

# Build valid command prefixes from help output.
declare -A VALID_SET=()
declare -a VALID_LIST=()
declare -A CMD_FIRST=()
declare -A SUDO_VALUE_FLAGS=(
  ["-u"]=1 ["-g"]=1 ["-h"]=1 ["-p"]=1 ["-C"]=1 ["-t"]=1 ["-T"]=1 ["-a"]=1
  ["--user"]=1 ["--group"]=1 ["--host"]=1 ["--prompt"]=1
  ["--close-from"]=1 ["--command-timeout"]=1 ["--timestamp-timeout"]=1
  ["--askpass"]=1
)

while IFS= read -r line; do
  [[ "${line}" =~ ^[[:space:]]*agentlab[[:space:]] ]] || continue
  cleaned="$(echo "${line}" | sed -E 's/\[[^]]*\]//g')"
  cleaned="${cleaned#"${cleaned%%[![:space:]]*}"}"
  read -ra tokens <<<"${cleaned}"
  idx=-1
  for i in "${!tokens[@]}"; do
    if [[ "${tokens[$i]}" == "agentlab" ]]; then
      idx=${i}
      break
    fi
  done
  if (( idx < 0 )); then
    continue
  fi
  cmd_tokens=()
  for ((j=idx+1; j<${#tokens[@]}; j++)); do
    tok="${tokens[$j]}"
    if [[ "${tok}" =~ ^[a-z][a-z0-9-]*$ ]]; then
      cmd_tokens+=("${tok}")
    elif (( ${#cmd_tokens[@]} > 0 )); then
      break
    else
      continue
    fi
  done
  if (( ${#cmd_tokens[@]} > 0 )); then
    cmd="${cmd_tokens[*]}"
    if [[ -z "${VALID_SET[$cmd]:-}" ]]; then
      VALID_SET["${cmd}"]=1
      VALID_LIST+=("${cmd}")
    fi
    first="${cmd_tokens[0]}"
    if [[ -n "${first}" ]]; then
      CMD_FIRST["${first}"]=1
    fi
  fi
done <<<"${HELP_OUT}"

declare -a ERRORS=()

check_agentlab_line() {
  local line="$1"
  local file="$2"
  local lineno="$3"

  if [[ "${line}" =~ ^[[:space:]]*# ]]; then
    return
  fi

  read -ra tokens <<<"${line}"
  local i=0
  while (( i < ${#tokens[@]} )); do
    tok="${tokens[$i]}"
    if [[ "${tok}" == *"="* && "${tok}" != -* ]]; then
      i=$((i + 1))
      continue
    fi
    break
  done
  if (( i >= ${#tokens[@]} )); then
    return
  fi

  if [[ "${tokens[$i]}" == "sudo" ]]; then
    i=$((i + 1))
    while (( i < ${#tokens[@]} )); do
      tok="${tokens[$i]}"
      if [[ "${tok}" == "--" ]]; then
        i=$((i + 1))
        break
      fi
      if [[ "${tok}" == -* ]]; then
        if [[ -n "${SUDO_VALUE_FLAGS[$tok]:-}" ]]; then
          i=$((i + 2))
        else
          i=$((i + 1))
        fi
        continue
      fi
      break
    done
  fi

  if (( i >= ${#tokens[@]} )); then
    return
  fi

  cmd_token="${tokens[$i]}"
  if [[ "${cmd_token}" != "agentlab" && "${cmd_token}" != */agentlab ]]; then
    return
  fi

  start=$((i + 1))
  while (( start < ${#tokens[@]} )); do
    tok="${tokens[$start]}"
    if [[ "${tok}" == *"="* && "${tok}" != -* ]]; then
      start=$((start + 1))
      continue
    fi
    if [[ "${tok}" == -* ]]; then
      next=$((start + 1))
      if (( next < ${#tokens[@]} )); then
        next_tok="${tokens[$next]}"
        if [[ -n "${CMD_FIRST[$next_tok]:-}" ]]; then
          start=$((start + 1))
        else
          start=$((start + 2))
        fi
      else
        start=$((start + 1))
      fi
      continue
    fi
    break
  done

  if (( start >= ${#tokens[@]} )); then
    return
  fi

  best_cmd=""
  best_len=0
  for valid in "${VALID_LIST[@]}"; do
    read -ra valid_tokens <<<"${valid}"
    vlen=${#valid_tokens[@]}
    if (( start + vlen > ${#tokens[@]} )); then
      continue
    fi
    match=1
    for ((k=0; k<vlen; k++)); do
      if [[ "${tokens[$((start + k))]}" != "${valid_tokens[$k]}" ]]; then
        match=0
        break
      fi
    done
    if (( match == 1 && vlen > best_len )); then
      best_cmd="${valid}"
      best_len=${vlen}
    fi
  done

  if [[ -z "${best_cmd}" ]]; then
    ERRORS+=("${file}:${lineno}: unknown agentlab command starting at '${tokens[$start]}'")
  fi
}

check_shell_block() {
  local block_file="$1"
  local file="$2"
  local start_line="$3"

  local sanitized="${TMP_DIR}/sanitized_${RANDOM}.sh"
  sed -E 's/<[^>]+>/PLACEHOLDER/g' "${block_file}" > "${sanitized}"

  local errfile="${TMP_DIR}/bash_${RANDOM}.err"
  if ! bash -n "${sanitized}" 2>"${errfile}"; then
    local err_line
    err_line="$(grep -Eo 'line [0-9]+' "${errfile}" | head -1 | awk '{print $2}')"
    local file_line="${start_line}"
    if [[ -n "${err_line}" ]]; then
      file_line=$((start_line + err_line - 1))
    fi
    local msg
    msg="$(head -1 "${errfile}")"
    ERRORS+=("${file}:${file_line}: bash syntax check failed: ${msg}")
  fi

  local lno=0
  while IFS= read -r line || [ -n "${line}" ]; do
    lno=$((lno + 1))
    check_agentlab_line "${line}" "${file}" "$((start_line + lno - 1))"
  done < "${block_file}"
}

check_yaml_block() {
  local block_file="$1"
  local file="$2"
  local start_line="$3"

  local lno=0
  while IFS= read -r line || [ -n "${line}" ]; do
    lno=$((lno + 1))

    if [[ -z "${line//[[:space:]]/}" ]]; then
      continue
    fi
    if [[ "${line}" =~ ^[[:space:]]*# ]]; then
      continue
    fi

    if [[ "${line}" == *$'\t'* ]]; then
      ERRORS+=("${file}:$((start_line + lno - 1)): YAML contains tab characters")
    fi

    indent_prefix="${line%%[! ]*}"
    indent_len=${#indent_prefix}
    if (( indent_len % 2 != 0 )); then
      ERRORS+=("${file}:$((start_line + lno - 1)): YAML indentation is not a multiple of 2 spaces")
    fi

    trimmed="${line#"${line%%[![:space:]]*}"}"
    trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"
    if [[ "${trimmed}" == ":" || "${trimmed}" == "-:" ]]; then
      ERRORS+=("${file}:$((start_line + lno - 1)): YAML key is empty before ':'")
    fi
  done < "${block_file}"
}

for file in "${DOC_FILES[@]}"; do
  if [[ ! -f "${file}" ]]; then
    continue
  fi

  lineno=0
  in_block=0
  block_target=0
  block_skip=0
  block_lang=""
  block_file=""
  block_start=0

  while IFS= read -r line || [ -n "${line}" ]; do
    lineno=$((lineno + 1))

    if (( in_block == 0 )); then
      trimmed="${line#"${line%%[![:space:]]*}"}"
      if [[ "${trimmed}" == "${FENCE}"* ]]; then
        info="${trimmed#${FENCE}}"
        info="${info#"${info%%[![:space:]]*}"}"
        read -ra info_tokens <<<"${info}"
        lang="${info_tokens[0]:-}"
        skip=0
        for tok in "${info_tokens[@]}"; do
          if [[ "${tok}" == "skip-snippet-check" ]]; then
            skip=1
            break
          fi
        done

        in_block=1
        block_lang="${lang}"
        block_skip=${skip}
        block_start=$((lineno + 1))
        block_target=0

        if (( skip == 0 )); then
          case "${lang}" in
            bash|sh|yaml|yml)
              block_target=1
              block_file="${TMP_DIR}/block_${RANDOM}.txt"
              : > "${block_file}"
              ;;
            *)
              block_target=0
              block_file=""
              ;;
          esac
        fi
        continue
      fi
    else
      trimmed="${line#"${line%%[![:space:]]*}"}"
      if [[ "${trimmed}" == "${FENCE}"* ]]; then
        if (( block_target == 1 && block_skip == 0 )); then
          case "${block_lang}" in
            bash|sh)
              check_shell_block "${block_file}" "${file}" "${block_start}"
              ;;
            yaml|yml)
              check_yaml_block "${block_file}" "${file}" "${block_start}"
              ;;
          esac
        fi
        in_block=0
        block_target=0
        block_skip=0
        block_lang=""
        block_file=""
        block_start=0
        continue
      fi

      if (( block_target == 1 )); then
        printf '%s\n' "${line}" >> "${block_file}"
      fi
    fi
  done < "${file}"

  if (( in_block == 1 )); then
    ERRORS+=("${file}:${lineno}: unterminated code block")
  fi
done

if (( ${#ERRORS[@]} > 0 )); then
  echo "Docs snippet check failed:" >&2
  for err in "${ERRORS[@]}"; do
    echo "  - ${err}" >&2
  done
  exit 1
fi

echo "Docs snippet check passed."
