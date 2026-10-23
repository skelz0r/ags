#!/bin/sh
set -eu

if [ $# -lt 1 ]; then
  echo "usage: bin/setup_worktree.sh <name> [branch]" >&2
  exit 2
fi

name="$1"
branch="${2:-feature/$name}"
root="$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")"
path="$(dirname "$root")/ags-worktrees/$name"

if [ -d "$path" ]; then
  echo "$path"
  exit 0
fi

git -C "$root" fetch -q origin
if git -C "$root" show-ref -q --verify "refs/heads/$branch"; then
  git -C "$root" worktree add -q "$path" "$branch"
else
  git -C "$root" worktree add -q "$path" -b "$branch" origin/main
fi
echo "$path"
