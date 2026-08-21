#!/usr/bin/env bash
#
# Étape « prepare » de semantic-release (@semantic-release/exec).
#
# Appelée avec la version SemVer calculée à partir des Conventional Commits,
# sans le préfixe « v » (ex. « 1.2.0 »). Elle compile `meds-api` et
# `bdpm-sync` pour linux/amd64 et linux/arm64 avec cette version injectée par
# ldflags, les archive dans ./dist et calcule leurs sommes de contrôle. Les
# archives sont ensuite attachées à la release par @semantic-release/github.
#
# Le tag n'existe pas encore à cet instant : le `git describe --tags` du
# Makefile ne peut donc pas le trouver, d'où le passage explicite de
# GIT_VERSION.
#
# Ce script n'écrit rien dans le dépôt : la release ne pousse aucun commit sur
# `main` (voir CONTRIBUTING.md § Publication des versions). En particulier,
# `info.version` de api/openapi.yaml décrit la version du *contrat* — servi
# sous /v1 — et n'est pas asservi au numéro de release ; il se modifie à la
# main, dans une PR, quand le contrat évolue.
#
set -euo pipefail

VERSION="${1:?usage: prepare-release.sh <version sans « v », ex. 1.2.0>}"
TAG="v${VERSION}"
DIST="dist"
PLATFORMS=(linux/amd64 linux/arm64)

# --- Binaires multi-arch ------------------------------------------------------
rm -rf "$DIST"
mkdir -p "$DIST"

for platform in "${PLATFORMS[@]}"; do
  goos="${platform%/*}"
  goarch="${platform#*/}"
  outdir="${DIST}/${goos}-${goarch}"

  # Les variables passées en ligne de commande à `make` l'emportent sur celles
  # définies dans le Makefile, y compris les affectations `:=`.
  GOOS="$goos" GOARCH="$goarch" make build BIN_DIR="$outdir" GIT_VERSION="$TAG"

  archive="${DIST}/meds-api_${TAG}_${goos}_${goarch}.tar.gz"
  tar -C "$outdir" -czf "$archive" .
  echo "→ ${archive}"
done

# --- Sommes de contrôle -------------------------------------------------------
# sha256sum (GNU) n'existe pas sur macOS, où l'équivalent est `shasum -a 256`.
if command -v sha256sum >/dev/null 2>&1; then sum() { sha256sum "$@"; }
else sum() { shasum -a 256 "$@"; }
fi

checksums="meds-api_${TAG}_checksums.txt"
(cd "$DIST" && sum meds-api_"${TAG}"_*.tar.gz > "$checksums")
echo "→ ${DIST}/${checksums}"
cat "${DIST}/${checksums}"
