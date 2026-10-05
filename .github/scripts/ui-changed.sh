#!/usr/bin/env bash
set -euo pipefail

files=$(grep -v '^web/screenshots/' || true)
if grep -qE '^(web/|internal/(api|auth|config|engine|github|mcp|runner|store|testkit)/|go\.(mod|sum)$|\.github/(workflows/screenshots\.yml|scripts/ui-changed\.sh)$)' <<<"$files"; then
  echo true
else
  echo false
fi
