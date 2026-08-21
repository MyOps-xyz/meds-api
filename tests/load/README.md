# Tests de charge (T-55)

Quatre scénarios k6, plus un vérificateur de budgets côté serveur.

```bash
make load-smoke      # fumée : un appel par endpoint
make load-mixed      # charge nominale, 200 VU / 5 min
make load-reload     # bascule sous charge — le test décisif
make load-spike      # montée brutale, dégradation propre
make load-soak       # endurance 1 h, détection de fuite
```

## Deux mesures distinctes, à ne pas confondre

| Mesure | Outil | Ce qu'elle contient |
|---|---|---|
| **Bout en bout** | seuils k6 | traitement **+ réseau + client** |
| **Côté serveur** | `check-budgets.sh` sur `/metrics` | traitement seul |

Les budgets de [07 §2.1](../../docs/07-performance.md) sont explicitement « hors réseau » : ils ne
peuvent être vérifiés que par la seconde. Mesuré le 15/08/2026, k6 en conteneur sur macOS rapporte
un p99 de **~207 ms identique sur toutes les routes**, y compris les plus légères — une latence
uniforme sur des routes dont le coût serveur varie d'un facteur vingt-cinq ne mesure pas le
serveur, elle mesure la pile réseau de la machine virtuelle.

Les seuils k6 détectent donc un **effondrement** ; `check-budgets.sh` vérifie les **budgets**.

## Résultats du 15/08/2026

Poste de développement (10 cœurs, k6 et serveur sur la même machine), 200 VU pendant 60 s,
156 825 requêtes, **0 erreur**, ~2 600 req/s de bout en bout.

| Route | p99 serveur (histogramme) | Budget |
|---|---:|---:|
| `GET /v1/medicaments/{cis}` | ≤ 0,20 ms | 200 µs |
| `GET /v1/suggest` | ≤ 1,00 ms | 1 ms |
| `GET /v1/medicaments?q=` | ≤ 5,00 ms | 3 ms |

**L'histogramme Prometheus ne suffit pas à trancher les budgets sous charge concurrente.** Ses
bornes sont trop larges pour distinguer 3 ms de 5 ms, et surtout le p99 relevé pendant une campagne
k6 inclut l'attente processeur due au générateur de charge, qui tourne sur la même machine. Une
mesure à faible concurrence ne corrige rien : à 20 VU l'échantillon tombe à 3 746 requêtes et le
p99 est dominé par les valeurs extrêmes du démarrage.

Les budgets se vérifient donc par **`make bench-budgets`**
([docs/07 §2.1](../../docs/07-performance.md#21-latence-p99-hors-réseau)) : appel direct au
handler, 20 000 mesures par opération, `GOMAXPROCS=2`. Les huit budgets y sont tenus — la
recherche à **71,3 µs** pour un budget de 3 ms, soit 70 fois moins que ce que k6 laissait croire.

`check-budgets.sh` garde son utilité : il vérifie l'ordre de grandeur **en production**, sur du
trafic réel, là où aucun banc ne se substitue à l'observation.

## `reload.js` — le test décisif, passé

Exigence E2 de [09](../../docs/09-pipeline-mise-a-jour.md) : mise à jour du jeu de données **sans
coupure**. Critère absolu, pas statistique — une seule requête en erreur invalide la conception.

Campagne du 15/08/2026, 100 VU pendant 120 s, bascule déclenchée à t+25 s :

| Indicateur | Mesure |
|---|---:|
| Requêtes servies | **896 094** |
| Débit | **7 463 req/s** |
| Requêtes en erreur | **0** |
| Réponses incohérentes | **0** |
| Contrôles réussis | 1 792 185 / 1 792 185 (100 %) |
| Bascule effective | oui — `changed=true`, Store reconstruit en **42 ms**, `meds_store_swaps_total` 1 → 2 |

La bascule doit être *réelle* pour que le test prouve quoi que ce soit : une première campagne
s'était terminée sur « source inchangée », l'ANSM n'ayant pas publié entre-temps, et n'avait donc
rien démontré. Le lien `current` est retiré en cours de campagne pour forcer une réingestion
complète.

## `spike.js` — dégradation propre

Montée à 5 000 req/s contre une limitation réglée à 100 req/s, valeurs de production.

| Indicateur | Mesure |
|---|---:|
| Requêtes émises | 944 444 (16 336 req/s) |
| Servies | 3 699 en `200` |
| Refusées | **940 712 en `429`**, toutes avec `Retry-After` |
| Erreurs serveur (`5xx`) | **0** |
| Échecs de connexion | 32 (0,003 %), côté générateur de charge |

Le critère n'est pas « aucune erreur » : produire des `429` est précisément le rôle de la
limitation. Le critère est de **ne jamais s'effondrer** — aucun `5xx`, aucune connexion coupée par
le serveur. Les 32 échecs sont des connexions refusées côté k6, qui saturait ses propres ressources
à 16 000 req/s sur le même hôte ; le serveur n'a journalisé aucun `5xx`.

Cette campagne a révélé un défaut d'instrumentation : les 429 étaient étiquetés `route="other"`,
le marquage de route intervenant après la limitation. En production, impossible de savoir quel
endpoint sature — au moment précis où l'on en a besoin. Corrigé, avec test de non-régression.

## `soak.js` — endurance, aucune fuite

50 VU pendant 6 minutes (version raccourcie ; la campagne nominale dure une heure).

| Indicateur | Mesure | Budget |
|---|---:|---:|
| Requêtes | 53 080, **0 erreur** | 0 % |
| RSS | oscille entre **55 et 116 Mio**, sans dérive | < 150 Mio |
| Heap en usage | 53 à 77 Mio, stable | — |
| Goroutines | **59, constant** après montée en charge | pas de fuite |

Ce que ce scénario cherche n'est pas la performance mais la fuite : une allocation jamais libérée,
une table de seaux qui croît sans purge, une goroutine qui ne se termine pas. Aucune des trois.

## Ce qui reste à exécuter

`soak.js` sur **une heure complète** : la version de six minutes ne montre aucune dérive, mais une
fuite lente resterait invisible à cette échelle.
