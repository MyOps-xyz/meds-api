#!/usr/bin/env bash
# Vérifie les budgets de latence de docs/07-performance.md §2.1 **côté
# serveur**, en lisant l'histogramme Prometheus après une campagne de charge.
#
# C'est la seule mesure qui isole le temps de traitement : k6 mesure de bout
# en bout, réseau compris, et sur un poste de développement le réseau domine
# de deux ordres de grandeur (mesuré : p99 uniforme de ~207 ms sur toutes les
# routes, quel que soit leur coût réel).
#
#   tests/load/check-budgets.sh http://127.0.0.1:18080
set -euo pipefail

BASE="${1:-http://127.0.0.1:18080}"

curl -sf "${BASE}/metrics" | python3 -c '
import sys, re, collections

# Budgets de docs/07-performance.md §2.1, en secondes.
BUDGETS = {
    "GET /v1/medicaments/{cis}": 0.0002,
    "GET /v1/presentations/{cip}": 0.0002,
    "GET /v1/suggest": 0.001,
    "GET /v1/dataset": 0.0002,
    "GET /v1/medicaments": 0.003,
}
MIN_ECHANTILLON = 500

buckets = collections.defaultdict(dict)
counts = {}
for line in sys.stdin:
    m = re.match(r"meds_http_request_duration_seconds_bucket\{method=\"GET\",route=\"([^\"]+)\",le=\"([^\"]+)\"\} (\S+)", line)
    if m:
        buckets[m.group(1)][float(m.group(2))] = float(m.group(3))
    m2 = re.match(r"meds_http_request_duration_seconds_count\{method=\"GET\",route=\"([^\"]+)\"\} (\S+)", line)
    if m2:
        counts[m2.group(1)] = float(m2.group(2))

echec = False
evalues = 0
print("  %-32s %8s  p99 serveur   budget    verdict" % ("route", "n"))
for route, budget in sorted(BUDGETS.items()):
    total = counts.get(route, 0)
    if total < MIN_ECHANTILLON:
        print("  %-32s %8d  echantillon insuffisant, non evalue" % (route, int(total)))
        continue
    evalues += 1
    b = buckets[route]
    # La borne la plus basse couvrant 99 % des observations.
    p99 = next((le for le in sorted(b) if b[le] >= 0.99 * total), float("inf"))
    ok = p99 <= budget
    echec = echec or not ok
    verdict = "tenu" if ok else "DÉPASSÉ"
    print("  %-32s %8d  <= %7.2f ms  %5.2f ms   %s" % (route, int(total), p99*1000, budget*1000, verdict))

if evalues == 0:
    print("  aucune route suffisamment sollicitee : lancer une campagne de charge d abord")
    sys.exit(1)
sys.exit(1 if echec else 0)
'
