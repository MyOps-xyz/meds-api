#!/usr/bin/env bash
# check-coverage.sh — contrôle des seuils de couverture décrits dans
# docs/11-strategie-de-tests.md §1 et exigés par T-03 / T-52.
#
# Usage :
#   check-coverage.sh <coverage.out> <seuil-global> [package=seuil ...]
#
# Exemple :
#   check-coverage.sh coverage.out 80 internal/bdpm=85 internal/store/search=85
#
# Règles :
#   - un package absent du profil (aucune instruction exécutable compilée en
#     mode couverture — typiquement un package encore vide en début de
#     projet) est IGNORÉ, jamais compté comme un échec ;
#   - un package présent dans le profil, même à 0 % de couverture réelle,
#     N'EST JAMAIS ignoré : c'est précisément le cas qu'on veut détecter ;
#   - le profil DOIT avoir été produit avec `-coverpkg=./...` (et pas
#     seulement `./...`), sinon un package sans fichier de test n'apparaît
#     jamais dans le profil, quel que soit son contenu réel, et le premier
#     point de cette liste masquerait alors un vrai package à 0 % ;
#   - conséquence de `-coverpkg=./...` : chaque binaire de test émet les blocs
#     de TOUS les packages, donc le même bloc apparaît autant de fois qu'il y
#     a de packages testés (ici ~8 fois, 18 000 lignes pour 2 300 blocs
#     réels). Il faut donc DÉDUPLIQUER par bloc avant d'agréger, exactement
#     comme le fait `go tool cover -func`. Sommer les lignes telles quelles
#     gonfle le dénominateur d'un facteur 8 sans gonfler le numérateur, et
#     transforme 86 % de couverture réelle en 19 % affichés.
set -euo pipefail

usage() {
	echo "usage: $0 <coverage.out> <seuil-global> [package=seuil ...]" >&2
	exit 2
}

if [ "$#" -lt 2 ]; then
	usage
fi

COVERAGE_FILE="$1"
GLOBAL_THRESHOLD="$2"
shift 2

if [ ! -f "$COVERAGE_FILE" ]; then
	echo "check-coverage: fichier de couverture introuvable : $COVERAGE_FILE" >&2
	exit 2
fi

MODULE=$(go list -m 2>/dev/null || true)
if [ -z "$MODULE" ]; then
	echo "check-coverage: impossible de déterminer le module courant (go list -m)" >&2
	exit 2
fi

# Agrège le profil par package (répertoire du fichier), en instructions
# couvertes / instructions totales. Le format du profil est documenté par
# `go help testflag` : chaque ligne après "mode: ..." est
#   <fichier>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
PKG_REPORT=$(awk '
	NR == 1 { next }
	{
		# $1 identifie le bloc de façon unique : <fichier>:<début>,<fin>.
		# Le nombre d\047instructions est invariant d\047une occurrence à
		# l\047autre ; seuls les compteurs d\047exécution s\047additionnent.
		stmt[$1] = $2
		count[$1] += $3
	}
	END {
		for (block in stmt) {
			split(block, parts, ":")
			pkg = parts[1]
			sub(/\/[^\/]+$/, "", pkg)
			total[pkg] += stmt[block]
			if (count[block] > 0) { covered[pkg] += stmt[block] }
		}
		for (p in total) {
			printf "%s\t%d\t%d\n", p, covered[p] + 0, total[p]
		}
	}
' "$COVERAGE_FILE")

if [ -z "$PKG_REPORT" ]; then
	echo "check-coverage: le profil ne contient aucune donnée exploitable — build cassée en amont ?" >&2
	exit 1
fi

FAILED=0
GLOBAL_COVERED=0
GLOBAL_TOTAL=0

echo "Couverture par package :"
while IFS=$'\t' read -r pkg covered total; do
	GLOBAL_COVERED=$((GLOBAL_COVERED + covered))
	GLOBAL_TOTAL=$((GLOBAL_TOTAL + total))
	pct=$(awk -v c="$covered" -v t="$total" 'BEGIN { printf "%.1f", (t > 0) ? (c / t * 100) : 0 }')
	rel="${pkg#"$MODULE"/}"
	printf '  %-55s %6s%%  (%d/%d instructions)\n' "$rel" "$pct" "$covered" "$total"
done <<<"$PKG_REPORT"

if [ "$GLOBAL_TOTAL" -eq 0 ]; then
	echo "check-coverage: aucune instruction instrumentée dans tout le module — rien à vérifier, on ignore." >&2
	exit 0
fi

GLOBAL_PCT=$(awk -v c="$GLOBAL_COVERED" -v t="$GLOBAL_TOTAL" 'BEGIN { printf "%.1f", c / t * 100 }')
echo
echo "Couverture globale : ${GLOBAL_PCT}% (seuil requis : ${GLOBAL_THRESHOLD}%)"

if awk -v got="$GLOBAL_PCT" -v want="$GLOBAL_THRESHOLD" 'BEGIN { exit !(got + 0 < want + 0) }'; then
	gap=$(awk -v got="$GLOBAL_PCT" -v want="$GLOBAL_THRESHOLD" 'BEGIN { printf "%.1f", want - got }')
	echo "ÉCHEC : couverture globale ${GLOBAL_PCT}% < ${GLOBAL_THRESHOLD}% requis (manque ${gap} points)" >&2
	FAILED=1
fi

# Seuils par package (ou préfixe de package, pour couvrir d'éventuels
# sous-packages futurs, ex. internal/bdpm/quelquechose).
for spec in "$@"; do
	name="${spec%%=*}"
	want="${spec##*=}"
	full="$MODULE/$name"

	pkg_covered=0
	pkg_total=0
	found=0
	while IFS=$'\t' read -r pkg covered total; do
		if [ "$pkg" = "$full" ] || [[ "$pkg" == "$full"/* ]]; then
			found=1
			pkg_covered=$((pkg_covered + covered))
			pkg_total=$((pkg_total + total))
		fi
	done <<<"$PKG_REPORT"

	if [ "$found" -eq 0 ] || [ "$pkg_total" -eq 0 ]; then
		echo "IGNORÉ : $name ne contient encore aucune instruction instrumentée (package vide ou non livré)."
		continue
	fi

	pct=$(awk -v c="$pkg_covered" -v t="$pkg_total" 'BEGIN { printf "%.1f", c / t * 100 }')
	if awk -v got="$pct" -v w="$want" 'BEGIN { exit !(got + 0 < w + 0) }'; then
		gap=$(awk -v got="$pct" -v w="$want" 'BEGIN { printf "%.1f", w - got }')
		echo "ÉCHEC : $name couvert à ${pct}% < ${want}% requis (manque ${gap} points)" >&2
		FAILED=1
	else
		echo "OK : $name couvert à ${pct}% (seuil ${want}%)"
	fi
done

if [ "$FAILED" -ne 0 ]; then
	exit 1
fi

echo
echo "Tous les seuils de couverture sont respectés."
