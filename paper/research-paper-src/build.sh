#!/bin/sh
# Builds n9n_paper.pdf. Needs pdflatex (TeX Live); IEEEtran.cls is vendored here.
# Regenerate the result tables first if results.json changed:
#   python3 -I experiments/gen_tables.py experiments/results.json .
set -e
cd "$(dirname "$0")"
pdflatex -interaction=nonstopmode -halt-on-error n9n_paper.tex >/dev/null
pdflatex -interaction=nonstopmode -halt-on-error n9n_paper.tex >/dev/null
echo "built n9n_paper.pdf"
