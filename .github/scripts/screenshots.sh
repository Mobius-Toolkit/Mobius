#!/usr/bin/env bash
set -euo pipefail
shopt -s nullglob

marker='<!-- screenshots -->'
remote=${REMOTE:-https://github.com/$REPO.git}
work=$(mktemp -d)

set_comment() {
  local body id
  body="$marker"$'\n### UI screenshots\n\n'"$1"
  id=$(gh api "repos/$REPO/issues/$PR/comments" --paginate \
    --jq ".[] | select(.user.login == \"github-actions[bot]\" and (.body | startswith(\"$marker\"))) | .id" | sed -n 1p)
  if [ -n "$id" ]; then
    gh api --method PATCH "repos/$REPO/issues/comments/$id" -f body="$body" --silent
  else
    gh api --method POST "repos/$REPO/issues/$PR/comments" -f body="$body" --silent
  fi
}

set_label() {
  local present
  present=$(gh api "repos/$REPO/issues/$PR/labels" --jq '.[] | select(.name == "screenshots") | .name')
  if [ "$1" = on ] && [ -z "$present" ]; then
    gh api --method POST "repos/$REPO/issues/$PR/labels" -f 'labels[]=screenshots' --silent
  elif [ "$1" = off ] && [ -n "$present" ]; then
    gh api --method DELETE "repos/$REPO/issues/$PR/labels/screenshots" --silent
  fi
}

has_artifact() {
  [ "$(gh api "repos/$REPO/actions/runs/$RUN_ID/artifacts" --jq '[.artifacts[] | select(.name == "screenshots")] | length')" -gt 0 ]
}

# The folder SHOTS comes from the code of a pull request. Copy only the files with a plain name.
push_folder() {
  local attempt file name
  git -C "$work" init -q -b screenshots
  git -C "$work" config user.name 'github-actions[bot]'
  git -C "$work" config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  git -C "$work" remote add origin "$remote"
  for attempt in 1 2 3 4 5; do
    if git -C "$work" fetch -q origin screenshots; then
      git -C "$work" reset -q --hard FETCH_HEAD
    fi
    rm -rf "${work:?}/$1"
    mkdir "$work/$1"
    for file in "$SHOTS"/*.png; do
      name=${file##*/}
      if [[ $name =~ ^[a-z0-9-]+\.png$ ]]; then
        cp "$file" "$work/$1/$name"
      fi
    done
    git -C "$work" add -A
    if ! git -C "$work" diff --cached --quiet; then
      git -C "$work" commit -q -m "chore: update the screenshots of $1"
    fi
    if git -C "$work" push -q origin screenshots; then
      return
    fi
  done
  echo "Cannot push the branch screenshots after $attempt attempts" >&2
  return 1
}

cell() {
  if [ -e "$work/$1/$2" ]; then
    echo "<img width=\"300\" src=\"$base/$1/$2\">"
  else
    echo "none"
  fi
}

case "$1" in
pending)
  set_comment "The screenshots are pending. See the [checks of the commit](https://github.com/$REPO/commit/$SHA/checks)."
  set_label off
  ;;
gate-pr)
  if [ "$(gh api "repos/$REPO/pulls/$PR" --jq .head.sha)" != "$SHA" ]; then
    echo proceed=false >> "$GITHUB_OUTPUT"
  elif has_artifact; then
    echo proceed=true >> "$GITHUB_OUTPUT"
  else
    set_comment "The screenshots failed. See the [run]($RUN_URL)."
    set_label off
    echo proceed=false >> "$GITHUB_OUTPUT"
  fi
  ;;
gate-main)
  if has_artifact; then
    echo proceed=true >> "$GITHUB_OUTPUT"
  else
    echo proceed=false >> "$GITHUB_OUTPUT"
  fi
  ;;
publish)
  push_folder "pr-$PR"
  base="https://raw.githubusercontent.com/$REPO/$(git -C "$work" rev-parse HEAD)"
  rows=
  names=$(for file in "$work/main"/*.png "$work/pr-$PR"/*.png; do echo "${file##*/}"; done | sort -u)
  for name in $names; do
    if [ ! -e "$work/main/$name" ]; then
      state=added
    elif [ ! -e "$work/pr-$PR/$name" ]; then
      state=removed
    elif cmp -s "$work/main/$name" "$work/pr-$PR/$name"; then
      continue
    else
      state=changed
    fi
    rows+="| \`${name%.png}\` | $state | $(cell main "$name") | $(cell "pr-$PR" "$name") |"$'\n'
  done
  if [ -z "$rows" ]; then
    set_comment "No screen changed."
    set_label off
  else
    set_comment $'| Screen | State | main | This pull request |\n|---|---|---|---|\n'"$rows"
    set_label on
  fi
  ;;
main)
  push_folder main
  ;;
esac
