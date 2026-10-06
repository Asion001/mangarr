#!/usr/bin/env sh
# Release notes for a tag from its conventional commits since the previous
# tag (all history for the first one), grouped by type:
#
#   scripts/release-notes.sh v1.0.0 > notes.md
#
# Breaking changes (a "!" after the type, or "BREAKING CHANGE" in the body)
# are listed first. Tests, CI, build and chore commits are left out.
set -eu
tag=${1:?usage: release-notes.sh <tag>}
prev=$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)
range=${prev:+$prev..}$tag

git log --no-merges --format='%h%x09%s%x09%b%x1e' "$range" | awk -v RS='\036' -F '\t' -v prev="$prev" -v tag="$tag" '
  function add(sec, line) { out[sec] = out[sec] "- " line "\n" }
  {
    sub(/^\n+/, "")
    if ($1 == "") next
    hash = $1; subject = $2; body = $3
    type = subject; sub(/[(:!].*/, "", type)
    text = subject; sub(/^[a-z]+(\([^)]*\))?!?: */, "", text)
    scope = ""
    if (match(subject, /^[a-z]+\([^)]*\)/)) { scope = substr(subject, RSTART, RLENGTH); sub(/^[a-z]+\(/, "", scope); sub(/\)$/, "", scope) }
    line = (scope != "" ? "**" scope ":** " : "") text " (" hash ")"
    if (subject ~ /^[a-z]+(\([^)]*\))?!:/ || body ~ /BREAKING CHANGE/) add("breaking", line)
    if (type == "feat") add("feat", line)
    else if (type == "fix") add("fix", line)
    else if (type == "perf") add("perf", line)
    else if (type == "docs") add("docs", line)
    else if (type ~ /^(test|ci|build|chore|style)$/) next
    else add("other", line)
  }
  END {
    split("breaking feat fix perf docs other", order, " ")
    title["breaking"] = "Breaking changes"; title["feat"] = "Features"; title["fix"] = "Fixes"
    title["perf"] = "Performance"; title["docs"] = "Documentation"; title["other"] = "Other changes"
    for (i = 1; i <= 6; i++) if (order[i] in out) printf "## %s\n\n%s\n", title[order[i]], out[order[i]]
    if (prev != "") printf "**Full changelog:** %s...%s\n", prev, tag
  }'
