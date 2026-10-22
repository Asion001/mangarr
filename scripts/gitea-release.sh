#!/usr/bin/env sh
# Gitea releases for CI, through the API (curl + jq):
#
#   scripts/gitea-release.sh ensure TAG TITLE NOTES_FILE PRERELEASE [TARGET]
#       creates the release unless it exists (the tag too, at TARGET);
#       an existing one gets the new title and notes
#   scripts/gitea-release.sh upload TAG FILE...
#       attaches the files, replacing ones of the same name
#   scripts/gitea-release.sh prune PREFIX KEEP
#       deletes all but the newest KEEP releases whose tag starts with
#       PREFIX, with their tags
#
# API is the repository's API root (<server>/api/v1/repos/<owner>/<name>),
# TOKEN a token that may write its releases.
set -eu
: "${API:?}" "${TOKEN:?}"

call() { # method path [curl args...]
  m=$1 p=$2; shift 2
  curl -fsS -X "$m" -H "Authorization: token $TOKEN" "$@" "$API$p"
}

release_id() { # tag -> id, empty when there is none
  curl -sS -H "Authorization: token $TOKEN" "$API/releases/tags/$1" | jq -r '.id // empty'
}

case $1 in
  ensure)
    tag=$2 title=$3 notes=$4 pre=$5 target=${6:-}
    body=$(jq -n --arg tag "$tag" --arg title "$title" --rawfile notes "$notes" \
      --argjson pre "$pre" --arg target "$target" \
      '{tag_name: $tag, name: $title, body: $notes, prerelease: $pre}
       + (if $target == "" then {} else {target_commitish: $target} end)')
    id=$(release_id "$tag")
    if [ -z "$id" ]; then
      call POST /releases -H 'Content-Type: application/json' -d "$body" >/dev/null
    else
      call PATCH "/releases/$id" -H 'Content-Type: application/json' -d "$body" >/dev/null
    fi
    ;;
  upload)
    tag=$2; shift 2
    id=$(release_id "$tag")
    [ -n "$id" ] || { echo "no release $tag" >&2; exit 1; }
    for f in "$@"; do
      name=$(basename "$f")
      call GET "/releases/$id/assets" | jq -r --arg n "$name" '.[] | select(.name == $n) | .id' |
        while read -r old; do call DELETE "/releases/$id/assets/$old" >/dev/null; done
      call POST "/releases/$id/assets?name=$name" -F "attachment=@$f" >/dev/null
    done
    ;;
  prune)
    prefix=$2 keep=$3 page=1 all=
    while :; do
      got=$(call GET "/releases?limit=50&page=$page" | jq -c --arg p "$prefix" '[.[] | select(.tag_name | startswith($p)) | {id, tag_name, created_at}]')
      [ "$got" = "[]" ] && [ "$(call GET "/releases?limit=50&page=$page" | jq length)" = 0 ] && break
      all=$(printf '%s\n%s' "$all" "$got")
      page=$((page + 1))
    done
    printf '%s\n' "$all" | jq -s -r --argjson keep "$keep" 'add // [] | sort_by(.created_at) | reverse | .[$keep:] | .[] | "\(.id) \(.tag_name)"' |
      while read -r id tag; do
        call DELETE "/releases/$id" >/dev/null
        call DELETE "/tags/$tag" >/dev/null || true
      done
    ;;
  *) echo "usage: $0 ensure|upload|prune ..." >&2; exit 2 ;;
esac
