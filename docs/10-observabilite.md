# 10 — Observabilité

## 1. Contrainte de départ

L'image `distroless` n'a **ni shell, ni utilitaire de diagnostic**
([08](08-deploiement-docker.md#diagnostic-sans-shell)). Il n'y a pas de `docker exec` de secours :
tout ce qui n'est pas exposé délibérément par l'application est invisible.

Cette contrainte est structurante et bénéfique : elle oblige à traiter l'observabilité comme une
**fonctionnalité conçue**, et non comme un filet de sécurité auquel on recourt après coup.

Trois questions doivent trouver réponse sans ouvrir de terminal :

1. Le service répond-il, et sert-il de la donnée ?
2. La donnée est-elle à jour et complète ?
3. Quand une requête échoue, pourquoi ?

---

## 2. Points de contrôle

| Endpoint | Auth | Question à laquelle il répond |
|---|---|---|
| `GET /healthz` | non | Le processus est-il vivant ? |
| `GET /readyz` | non | Peut-il servir de la donnée ? |
| `GET /metrics` | réseau interne | Comment se comporte-t-il ? |
| `GET /v1/dataset` | clé API | Quelle donnée sert-il, et depuis quand ? |

### `/healthz` et `/readyz` — une distinction qui compte

```
/healthz → 200 dès que le processus vit          (sonde de vivacité)
/readyz  → 200 seulement si un Store est publié  (sonde de disponibilité)
```

Confondre les deux est une erreur classique aux conséquences concrètes. Si `/healthz` échouait
faute de dataset, l'orchestrateur **redémarrerait** le conteneur — qui repartirait sans dataset,
en boucle. Avec la distinction : le conteneur reste vivant, ne reçoit aucun trafic, et continue
d'essayer de se synchroniser.

```json
{ "status": "ready", "dataset_version": "2026-08-14T04:12:33Z", "dataset_age_seconds": 46821 }
```

`/readyz` ne juge **pas** la fraîcheur : un dataset de trois jours reste servable. Décider qu'il
est trop vieux relève de l'alerte (§5), pas du routage.

---

## 3. Métriques Prometheus

Préfixe `meds_`. Conventions respectées : unités en suffixe, `_total` pour les compteurs,
étiquettes à **cardinalité bornée**.

### 3.1 Trafic HTTP

| Métrique | Type | Étiquettes | Usage |
|---|---|---|---|
| `meds_http_requests_total` | compteur | `route`, `method`, `status` | Trafic, taux d'erreur |
| `meds_http_request_duration_seconds` | histogramme | `route`, `method` | Latence p50/p95/p99 |
| `meds_http_response_size_bytes` | histogramme | `route` | Charge utile |
| `meds_http_in_flight_requests` | jauge | — | Concurrence instantanée |

> `route` est le **patron** (`/v1/medicaments/{cis}`), jamais le chemin réel. Utiliser le chemin
> créerait une série temporelle par code CIS — 15 857 séries pour un seul endpoint, de quoi
> saturer Prometheus. C'est l'erreur d'instrumentation la plus courante et la plus coûteuse.

Bornes de l'histogramme adaptées aux budgets de [07](07-performance.md) — les bornes par défaut
de Prometheus démarrent à 5 ms et n'auraient aucune résolution utile ici :

```go
Buckets: []float64{0.0001, 0.0002, 0.00025, 0.0005, 0.001, 0.0025, 0.003, 0.005, 0.01, 0.025, 0.1, 0.5, 1}
//        100 µs  200 µs  250 µs   500 µs   1 ms   2,5 ms  3 ms   5 ms  10 ms  25 ms 100 ms
```

**Chaque budget de [07](07-performance.md#21-latence-p99-hors-réseau) est une borne exacte.** La
première rédaction de cette liste proposait 250 µs et 5 ms, qui *encadrent* les budgets de 200 µs
et 3 ms sans les atteindre : un p99 tombant dans le seau (100 µs – 250 µs] ne permet alors pas de
dire s'il respecte 200 µs. Le budget devenait invérifiable par la métrique censée le surveiller.
Constaté le 15/08/2026 en tentant la vérification ; 200 µs et 3 ms ont été ajoutés.

### 3.2 Jeu de données

| Métrique | Type | Usage |
|---|---|---|
| `meds_dataset_age_seconds` | jauge | **Métrique la plus importante** — fraîcheur |
| `meds_dataset_info` | jauge (=1) | Étiquettes `version`, `hash` — identification |
| `meds_dataset_records` | jauge (`entity`) | Volumétrie par entité |
| `meds_dataset_quarantine_records` | jauge (`file`, `reason`) | Qualité de l'ingestion |
| `meds_store_build_duration_seconds` | histogramme | Budget de 500 ms tenu ? |
| `meds_store_swaps_total` | compteur | Nombre de rechargements |

### 3.3 Synchronisation

| Métrique | Type | Usage |
|---|---|---|
| `meds_sync_attempts_total` | compteur (`result`) | `success`, `unchanged`, `failed` |
| `meds_sync_duration_seconds` | histogramme | Durée d'une ingestion |
| `meds_sync_last_success_timestamp` | jauge | Date de la dernière réussite |
| `meds_sync_download_bytes_total` | compteur (`file`) | Volume tiré de l'ANSM |
| `meds_sync_in_progress` | jauge | Synchronisation en cours |

Distinguer `unchanged` de `success` évite un contresens en supervision : la plupart des jours,
l'issue normale est `unchanged` — la source n'a pas bougé. Compter cela comme un échec
déclencherait des alertes permanentes.

### 3.4 Recherche et limitation de débit

| Métrique | Type | Usage |
|---|---|---|
| `meds_search_duration_seconds` | histogramme (`mode`) | `exact` vs `trigram` |
| `meds_search_results_count` | histogramme | Détection de requêtes stériles |
| `meds_search_fallback_total` | compteur | Fréquence du repli trigramme |
| `meds_ratelimit_rejected_total` | compteur (`scope`) | `key` ou `ip` |
| `meds_auth_failures_total` | compteur | Tentatives d'accès invalides |

### 3.5 Runtime Go

Fournies par `collectors.NewGoCollector()` : `go_goroutines`, `go_memstats_heap_inuse_bytes`,
`go_gc_duration_seconds`. Elles servent à détecter fuites de goroutines et dérive mémoire.

---

## 4. Journalisation

`log/slog` en JSON sur la sortie standard, conformément au modèle 12-factor : c'est
l'infrastructure qui collecte, pas l'application qui écrit des fichiers.

```json
{"time":"2026-08-14T09:14:22.481Z","level":"INFO","msg":"request",
 "request_id":"01J8Z9K2M3N4P5Q6R7S8T9V0W1","method":"GET",
 "route":"/v1/medicaments","status":200,"duration_ms":0.84,
 "bytes":2431,"key_id":"mk_live_7f3a…","ip":"203.0.113.42"}
```

| Niveau | Emploi |
|---|---|
| `ERROR` | Anomalie exigeant une action : sync échouée, panic récupérée |
| `WARN` | Anomalie tolérée : quarantaine, dépassement de quota, dataset vieillissant |
| `INFO` | Événements normaux : requêtes, sync, bascule de `Store` |
| `DEBUG` | Diagnostic ; jamais en production (coût d'E/S, [07](07-performance.md#6-anti-patterns-à-surveiller-en-revue-de-code)) |

### Règles strictes

- **`Authorization` n'est jamais journalisé**, même tronqué. Seul un identifiant dérivé
  (`key_id` = 8 premiers caractères du hash) permet de distinguer les consommateurs.
- Aucun corps de requête ni de réponse.
- Le format JSON de `slog` rend l'**injection de logs impossible** : un retour à la ligne dans une
  valeur est échappé, il ne peut pas fabriquer une fausse entrée.
- IP tronquable via `MEDS_LOG_ANONYMIZE_IP` ([06](06-securite.md#9-journalisation-et-données-personnelles)).

### `request_id`

Un ULID généré à l'entrée, propagé dans le `context`, présent dans **toutes** les lignes de log de
la requête et renvoyé en `X-Request-Id`. C'est le lien entre le ticket d'un utilisateur (« j'ai eu
une erreur 500 ») et la trace serveur. Sans lui, un `500` est indiagnosticable.

Un `X-Request-Id` fourni par le client est repris **uniquement s'il est un ULID valide** — sinon
un client pourrait polluer les logs ou usurper l'identifiant d'une autre requête.

---

## 5. Alertes

Règles Prometheus fournies dans `deploy/prometheus/alerts.yml` (T-45).

| Alerte | Condition | Sévérité | Signification |
|---|---|---|---|
| `MedsApiDown` | `up == 0` pendant 2 min | critique | Service injoignable |
| `MedsApiNotReady` | `meds_dataset_age_seconds` absent 5 min | critique | Aucun dataset chargé |
| `MedsDatasetStale` | `meds_dataset_age_seconds > 259200` | avertissement | 3 cycles manqués (72 h) |
| `MedsDatasetVeryStale` | `> 604800` (7 j) | critique | Pipeline durablement cassé |
| `MedsSyncFailing` | 2 échecs consécutifs | avertissement | Source ou format en cause |
| `MedsHighErrorRate` | `5xx` > 1 % sur 5 min | critique | Régression |
| `MedsHighLatency` | p99 > 10 ms sur 5 min | avertissement | Dégradation |
| `MedsQuarantineSpike` | `quarantine_records > 100` | avertissement | Format amont modifié |
| `MedsMemoryHigh` | RSS > 400 Mio sur 10 min | avertissement | Fuite probable |
| `MedsRateLimitSpike` | rejets > 100/s sur 5 min | information | Abus ou client mal réglé |

**`MedsDatasetStale` est l'alerte à ne pas manquer.** Une API qui répond parfaitement avec des
données de trois semaines est plus dangereuse qu'une API en panne : l'utilisateur ne perçoit rien.
Le seuil de 72 h laisse passer deux incidents de synchronisation avant d'alerter, sans laisser
dériver.

---

## 6. Runbook

### « L'API renvoie 503 »

```bash
curl -s localhost:8080/readyz | jq
docker compose logs api --tail 100 | grep -E '"level":"(ERROR|WARN)"'
```

| Cause | Vérification | Correction |
|---|---|---|
| Aucun dataset au démarrage | `/readyz` → `no dataset` | `POST /admin/sync`, ou `bdpm-sync` en CLI |
| Sync jamais réussie | `meds_sync_attempts_total{result="failed"}` | Vérifier la sortie réseau vers l'ANSM |
| Volume vide ou non monté | `docker volume inspect meds-data` | Corriger le montage |

### « Les données sont périmées »

```bash
curl -sH "Authorization: Bearer $KEY" localhost:8080/v1/dataset | jq '.data.age_seconds, .data.last_sync'
```

Si `last_sync.status == "failed"` : lire le motif dans les logs. Cause la plus fréquente :
sortie réseau bloquée, ou changement de format amont ayant fait dépasser le seuil de rejet
([09](09-pipeline-mise-a-jour.md#36-validation)).

### « Une requête échoue »

Récupérer le `X-Request-Id` renvoyé au client, puis :

```bash
docker compose logs api | grep 01J8Z9K2M3N4P5Q6R7S8T9V0W1
```

### « L'API est lente »

```bash
curl -s localhost:8080/metrics | grep -E 'meds_http_request_duration|meds_search_fallback'
```

| Symptôme | Piste |
|---|---|
| Repli trigramme fréquent | Requêtes utilisateurs trop approximatives — chemin dégradé emprunté souvent |
| Latence élevée sur un seul endpoint | Régression localisée ; profiler avec `MEDS_PPROF=true` |
| Latence globale + mémoire haute | Pression GC ; vérifier `GOMEMLIMIT` et la limite mémoire du conteneur |
| Latence pendant quelques secondes | Rechargement en cours — normal si `meds_store_swaps_total` vient d'augmenter |

### « La mémoire monte »

Une hausse transitoire pendant un rechargement est **attendue** (deux `Store` coexistent). Une
hausse continue ne l'est pas :

```bash
MEDS_PPROF=true  # puis
go tool pprof -http=:8081 http://localhost:8080/debug/pprof/heap
```

Suspects habituels : buckets de rate limiting non purgés, goroutines fuyantes (`go_goroutines`
croissant sans retour).

---

## 7. Tableau de bord recommandé

Quatre rangées, dans cet ordre de priorité :

1. **Santé** — disponibilité, `meds_dataset_age_seconds`, version du dataset, dernière sync.
2. **Trafic** — req/s par route, taux d'erreur, répartition des statuts.
3. **Latence** — p50/p95/p99 par route, avec les budgets de [07](07-performance.md) en lignes de
   référence : un tableau de bord qui n'affiche pas l'objectif ne permet pas de juger la mesure.
4. **Ressources** — RSS, goroutines, durée de GC, durée de construction du `Store`.

## 8. Ce qui est écarté en v1

| Écarté | Raison |
|---|---|
| Traçage distribué (OpenTelemetry) | Un seul service, sans appel sortant sur le chemin de requête : une trace n'apprendrait rien de plus que la durée déjà mesurée |
| Journalisation dans un fichier | 12-factor : la collecte incombe à l'infrastructure |
| APM propriétaire | Dépendance et coût disproportionnés au périmètre |
| Métriques par clé API | Cardinalité non bornée ; `key_id` dans les logs suffit à l'analyse |

Le traçage redeviendrait pertinent si l'API venait à appeler d'autres services — ce que
l'architecture actuelle exclut ([02](02-architecture.md#9-ce-que-larchitecture-exclut-délibérément)).
