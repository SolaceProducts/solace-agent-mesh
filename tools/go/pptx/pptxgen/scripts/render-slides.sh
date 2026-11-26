#!/usr/bin/env bash
# Copyright Solace Corporation
#
# Use of this software is governed by the Business Source License (BUSL-1.1)
# included in the LICENSE file.

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <deck.pptx> <output-dir>" >&2
  exit 64
fi

deck=$1
outdir=$2

if [[ ! -f $deck ]]; then
  echo "render-slides.sh: input deck not found: $deck" >&2
  exit 66
fi

for tool in soffice pdftoppm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "render-slides.sh: required tool '$tool' is not on PATH" >&2
    echo "  install: libreoffice (soffice) and poppler-utils (pdftoppm)" >&2
    exit 69
  fi
done

mkdir -p -- "$outdir"
abs_outdir=$(cd -- "$outdir" && pwd)

tmp=$(mktemp -d -t pptxgen-render.XXXXXX)
trap 'rm -rf "$tmp"' EXIT

profile=$tmp/lo-profile
mkdir -p "$profile"

soffice \
  --headless \
  --norestore \
  --nofirststartwizard \
  -env:UserInstallation="file://$profile" \
  --convert-to pdf \
  --outdir "$tmp" \
  "$deck" \
  >"$tmp/soffice.log" 2>&1

base=$(basename "$deck" .pptx)
pdf=$tmp/$base.pdf
if [[ ! -f $pdf ]]; then
  echo "render-slides.sh: soffice did not produce $pdf" >&2
  echo "--- soffice log ---" >&2
  cat "$tmp/soffice.log" >&2
  exit 70
fi

pdftoppm -png -r 150 "$pdf" "$abs_outdir/slide" >"$tmp/pdftoppm.log" 2>&1

ls "$abs_outdir"/slide-*.png >/dev/null 2>&1 || {
  echo "render-slides.sh: pdftoppm produced no PNGs" >&2
  echo "--- pdftoppm log ---" >&2
  cat "$tmp/pdftoppm.log" >&2
  exit 71
}

echo "rendered $(ls "$abs_outdir"/slide-*.png | wc -l | tr -d ' ') slide(s) to $abs_outdir"
