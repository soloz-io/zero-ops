#!/usr/bin/env bash
# Make developer tooling findable regardless of who is invoking us.
#
# WHY THIS EXISTS
#
#   These validators run from three places with three different PATHs: an
#   interactive shell (everything on PATH), CI (its own image), and a git commit
#   started from an editor or GUI client. The third gets a MINIMAL PATH -- roughly
#   /usr/bin:/bin:/usr/sbin:/sbin -- because it never sourced a login shell.
#
#   The failure that produced this file, committing from a GUI on 2026-09-28:
#
#     ArgoCD Day-0 Seed Parity ... Failed
#       ❌ required tool not found: helm
#     YAML Lint ... Failed
#       xargs: yamllint: No such file or directory
#     Validate Embedded YAML ... Failed
#       ModuleNotFoundError: No module named 'yaml'
#
#   Every one of those tools was installed. None was on that PATH: helm lives in
#   /opt/homebrew/bin, yamllint in a pyenv shim, and bare `python3` resolved to
#   /usr/bin/python3, which has no PyYAML. The same commit from a terminal passed
#   every hook.
#
#   A gate that passes for one person and fails for another is worse than no gate:
#   it gets diagnosed as "the hooks are broken" and then bypassed with --no-verify,
#   which is how an unvalidated change reaches the cluster.
#
# WHAT IT DOES NOT DO
#
#   It does not make a missing tool pass. A genuinely absent tool must still fail
#   loudly -- see require_tools below, which names what to install. This only
#   widens the search to the places these tools are normally installed.
#
# Source it, do not execute it:  . "$(dirname "$0")/lib/tool-path.sh"

for _d in \
  /opt/homebrew/bin \
  /usr/local/bin \
  /opt/local/bin \
  "${HOME}/.pyenv/shims" \
  "${HOME}/.local/bin" \
  "${HOME}/go/bin" \
  "${GOPATH:-${HOME}/go}/bin" \
  /usr/local/go/bin
do
  # Appended, never prepended: an explicitly chosen tool earlier on PATH -- a
  # version manager's shim, a pinned binary in CI -- must keep winning.
  case ":${PATH}:" in
    *":${_d}:"*) ;;
    *) [ -d "${_d}" ] && PATH="${PATH}:${_d}" ;;
  esac
done
unset _d
export PATH

# require_tools <tool>... -- fail with something actionable rather than a bare
# "not found", because the reader is usually looking at a GUI's hook output with
# no shell in front of them.
require_tools() {
  local missing=()
  local t
  for t in "$@"; do
    command -v "$t" >/dev/null 2>&1 || missing+=("$t")
  done
  [ ${#missing[@]} -eq 0 ] && return 0

  echo "❌ required tool(s) not found: ${missing[*]}" >&2
  echo "   PATH searched: ${PATH}" >&2
  echo "" >&2
  echo "   If these ARE installed, the commit was probably started from an editor" >&2
  echo "   or GUI client, which does not inherit your shell's PATH. Committing from" >&2
  echo "   a terminal will work; so will adding their directory to the GUI's PATH." >&2
  echo "" >&2
  echo "   If they are not installed:" >&2
  for t in "${missing[@]}"; do
    case "$t" in
      helm)      echo "     brew install helm" >&2 ;;
      yq)        echo "     brew install yq" >&2 ;;
      kustomize) echo "     brew install kustomize" >&2 ;;
      kubectl)   echo "     brew install kubectl" >&2 ;;
      go)        echo "     brew install go" >&2 ;;
      yamllint)  echo "     pipx install yamllint   (or let pre-commit manage it)" >&2 ;;
      *)         echo "     install: $t" >&2 ;;
    esac
  done
  return 1
}
