# 02 — Architecture

## 1. Principe directeur

> Le jeu de données est **petit, en lecture seule et immuable entre deux synchronisations**.
> Toute l'architecture découle de ce fait.

22 Mo pour 15 857 médicaments ([01](01-analyse-source-bdpm.md)). Il n'y a donc ni requête à
optimiser, ni cache à invalider, ni verrou à poser : on construit une **structure immuable en
mémoire**, on la sert sans aucune synchronisation, et on la **remplace en bloc** quand la donnée
change. Le chemin de lecture n'a ni mutex, ni allocation de fond, ni entrée/sortie.

## 2. Vue d'ensemble

```
                        ┌──────────────────────────────────┐
   Internet ──── 443 ──►│  Caddy 2.11.4  ou  Traefik 3.7.10│  TLS auto / HTTP-3
                        │  (profil docker-compose)         │
                        └────────────────┬─────────────────┘
                                         │ HTTP interne (réseau Docker)
                        ┌────────────────▼─────────────────┐
                        │        meds-api  (Go 1.26.6)     │
                        │                                  │
                        │  ┌────────────────────────────┐  │
                        │  │ Middlewares                │  │
                        │  │ requestID → log → recover  │  │
                        │  │ → CORS → auth → ratelimit  │  │
                        │  │ → timeout                  │  │
                        │  └─────────────┬──────────────┘  │
                        │                │                 │
                        │  ┌─────────────▼──────────────┐  │
                        │  │ Handlers  (net/http.ServeMux)│ │
                        │  └─────────────┬──────────────┘  │
                        │                │ lecture sans verrou
                        │  ┌─────────────▼──────────────┐  │
                        │  │  atomic.Pointer[Store]     │  │
                        │  │  ┌──────────────────────┐  │  │
                        │  │  │ Store (immuable)     │  │  │
                        │  │  │  · SoA + interning   │  │  │
                        │  │  │  · index CSR         │  │  │
                        │  │  │  · index inversé     │  │  │
                        │  │  │  · trigrammes        │  │  │
                        │  │  └──────────────────────┘  │  │
                        │  └─────────────▲──────────────┘  │
                        │                │ swap atomique   │
                        │  ┌─────────────┴──────────────┐  │
                        │  │ Scheduler (cron + jitter)  │  │
                        │  └─────────────┬──────────────┘  │
                        └────────────────┼─────────────────┘
                                         │
                        ┌────────────────▼─────────────────┐
                        │ Synchroniseur                    │
                        │ download → encodage → parse →    │
                        │ normalise → valide → snapshot    │
                        └────────────────┬─────────────────┘
                          │                              │
                    ┌─────▼──────┐              ┌────────▼────────┐
                    │ ANSM/BDPM  │              │ volume ./data   │
                    │ 10 fichiers│              │ snapshots NDJSON│
                    └────────────┘              └─────────────────┘
```

## 3. Composants

### 3.1 Deux binaires, une seule image

```
cmd/meds-api/    serveur HTTP + scheduler intégré   (processus principal)
cmd/bdpm-sync/   CLI de synchronisation one-shot    (cron externe, CI, exploitation)
```

Les deux partagent l'intégralité du code d'ingestion (`internal/bdpm`). La CLI existe pour trois
usages : amorcer le volume avant le premier démarrage, permettre un cron système à qui préfère
l'externaliser, et rejouer une ingestion en CI sur des données figées.

**Pourquoi le scheduler est-il dans le serveur plutôt que dans un sidecar cron ?** Un sidecar
imposerait de partager un volume en écriture, de gérer un signal de rechargement inter-conteneurs
et un ordonnanceur supplémentaire (ofelia, crond) — trois pièces mobiles pour un besoin
qu'une goroutine et un `time.Timer` couvrent. Voir
[ADR 0003](adr/0003-snapshot-ndjson-et-hot-swap-atomique.md) pour les conséquences en
déploiement multi-instance.

### 3.2 Arborescence du code

```
cmd/
  meds-api/main.go            câblage, config, arrêt gracieux
  bdpm-sync/main.go           CLI de synchronisation

internal/
  bdpm/                       tout ce qui touche à la source
    client.go                 téléchargement (retry, backoff, timeout, User-Agent)
    encoding.go               détection UTF-8 / Windows-1252  [P1]
    scanner.go                lecture TSV brute                [P7, P8]
    parse_*.go                un parseur par fichier (10)
    normalize.go              prix, dates, taux, libellés      [P4, P5]
    validate.go               intégrité référentielle          [P6]
    snapshot.go               écriture atomique + manifest
    report.go                 rapport d'ingestion et quarantaine

  store/                      index en mémoire
    model.go                  entités (SoA)
    intern.go                 table d'internement
    csr.go                    index de relations 1-N
    build.go                  construction du Store
    store.go                  accès en lecture, atomic.Pointer
    search/
      normalize.go            NFD, diacritiques, casse
      inverted.go             index inversé
      trigram.go              tolérance aux fautes
      rank.go                 scoring

  api/
    router.go                 net/http.ServeMux
    handlers_*.go             un fichier par famille d'endpoints
    render.go                 JSON, ETag, buffers poolés
    problem.go                erreurs RFC 9457
    paginate.go               curseurs opaques
    openapi.go                spec embarquée + /docs

  middleware/                 requestID, logging, recover, CORS, auth, ratelimit, timeout
  config/                     chargement et validation de la configuration
  observability/              métriques Prometheus, slog
  scheduler/                  cron interne avec jitter

api/openapi.yaml              contrat, source de vérité, embarqué via go:embed
testdata/golden/              échantillons figés des 10 fichiers
deploy/                       Caddyfile, traefik/, compose
docs/                         la présente documentation
```

Découpage par **domaine**, pas par couche technique : `bdpm` ne connaît rien du HTTP, `api` ne
connaît rien du format des fichiers ANSM, `store` ne connaît ni l'un ni l'autre. Le sens des
dépendances est strictement `api → store → bdpm`, sans retour.

### 3.3 Dépendances externes

Volontairement réduites au strict nécessaire :

| Dépendance | Usage | Pourquoi elle est indispensable |
|---|---|---|
| `golang.org/x/text` | normalisation Unicode NFD, décodage Windows-1252 | Réimplémenter la décomposition Unicode serait fautif |
| `github.com/prometheus/client_golang` | métriques | Format d'exposition standard |

Tout le reste vient de la bibliothèque standard : routage (`net/http.ServeMux` et ses patrons
méthode+chemin depuis Go 1.22), JSON (`encoding/json`), logs (`log/slog`), crypto
(`crypto/sha256`, `crypto/subtle`, `crypto/rand`), concurrence (`sync/atomic`, `context`).

Aucun framework web, aucun ORM, aucune bibliothèque de routage. Sur une API à 17 endpoints en
lecture seule, ils n'apporteraient rien et élargiraient la surface d'attaque et de maintenance.

## 4. Flux de données

### 4.1 Démarrage

```
1. Charger et valider la configuration                        → échec ⇒ arrêt immédiat
2. Chercher le dernier snapshot valide dans ./data/snapshots
   ├── trouvé   → construire le Store (~300 ms) → /readyz OK
   └── absent   → tenter une synchronisation immédiate
                  ├── succès → construire le Store → /readyz OK
                  └── échec  → /healthz OK, /readyz KO, endpoints en 503
3. Démarrer le scheduler (prochaine occurrence cron + jitter)
4. Écouter en HTTP
```

`/healthz` répond dès que le processus vit ; `/readyz` seulement quand un `Store` est publié.
Cette distinction permet à l'orchestrateur de ne router aucun trafic vers une instance qui n'a
pas encore de donnée, sans pour autant la tuer.

### 4.2 Requête

```
Requête → requestID → log → recover → CORS → auth → ratelimit → timeout → handler
                                                                              │
                                            Store := storePtr.Load()  ◄───────┘
                                            (lecture atomique, sans verrou)
                                                     │
                              lookup O(1) / recherche indexée → sérialisation JSON
                                                     │
                                    ETag calculé ─── si If-None-Match identique ⇒ 304
```

Le `Store` est chargé **une seule fois par requête** et gardé en variable locale : même si un
rechargement survient pendant le traitement, la requête voit un état cohérent de bout en bout.

### 4.3 Synchronisation (détail en [09](09-pipeline-mise-a-jour.md))

```
Scheduler / POST /admin/sync / CLI
        │
        ▼
 Téléchargement des 10 fichiers en parallèle (errgroup, retry, backoff)
        │
        ▼
 SHA-256 de chaque fichier + hash global
        │
   identique au manifest courant ? ──── oui ──► fin, aucune reconstruction
        │ non
        ▼
 Détection d'encodage → parsing → normalisation → validation
        │
   taux de rejet acceptable ? ──── non ──► ÉCHEC : ancien Store conservé, alerte
        │ oui
        ▼
 Écriture du snapshot (tmp puis os.Rename atomique) + manifest.json
        │
        ▼
 Construction du nouveau Store en arrière-plan (~300 ms)
        │
        ▼
 storePtr.Store(nouveau)  ◄── bascule instantanée, requêtes en vol préservées
        │
        ▼
 Purge des snapshots au-delà des N derniers
```

## 5. Concurrence

Le modèle tient en une phrase : **une seule variable partagée, écrite par un seul écrivain.**

- `atomic.Pointer[Store]` est le **seul** état mutable du processus.
- Les lecteurs font `Load()`. Aucun verrou, aucune contention, quel que soit le nombre de cœurs.
- Un seul écrivain (`Store()`), sérialisé par un mutex côté synchroniseur pour empêcher deux
  synchronisations concurrentes.
- Le `Store` est **entièrement immuable** après construction : aucun champ n'est modifié, aucune
  structure n'est lazy. C'est ce qui rend la lecture sans verrou correcte.
- L'ancien `Store` reste vivant tant qu'une requête le référence ; le GC le libère ensuite.
  Pointe mémoire transitoire : deux `Store` coexistent quelques centaines de millisecondes,
  d'où le budget RSS de 150 Mo pour ~70 Mo de données ([07](07-performance.md)).

Le rate limiter est la seule autre structure concurrente : compteurs shardés par hachage de clé,
un mutex par shard, afin d'éviter un point de contention unique.

## 6. Gestion des erreurs

| Situation | Comportement |
|---|---|
| ANSM injoignable | Retry avec backoff exponentiel ; échec ⇒ **ancien Store conservé**, métrique + log |
| Un fichier sur dix corrompu | Ingestion **rejetée en bloc** : un snapshot partiel serait pire que périmé |
| Quelques lignes invalides | Quarantaine, comptage par fichier et motif, exposition sur `/v1/dataset` |
| Taux de rejet > seuil (défaut 1 %) | Ingestion rejetée : signe d'un changement de format amont |
| Panic dans un handler | Middleware `recover` ⇒ `500` en RFC 9457, requestID journalisé, processus préservé |
| Aucun snapshot au démarrage | `/readyz` KO, endpoints en `503` — le service ne ment jamais sur son état |

**Principe** : servir une donnée périmée mais cohérente vaut toujours mieux que servir une donnée
fraîche mais incohérente. Un référentiel de médicaments incohérent est un risque, pas un service.

## 7. Configuration

Par variables d'environnement (12-factor), validées au démarrage — **échec immédiat si invalide**,
jamais de valeur par défaut silencieuse sur un paramètre de sécurité.

| Variable | Défaut | Rôle |
|---|---|---|
| `MEDS_ADDR` | `:8080` | Adresse d'écoute |
| `MEDS_DATA_DIR` | `/data` | Répertoire des snapshots |
| `MEDS_API_KEYS` | *(requis)* | Clés API, séparées par des virgules |
| `MEDS_ADMIN_KEY` | *(optionnel)* | Clé pour `POST /admin/sync` ; endpoint désactivé si absent |
| `MEDS_SYNC_CRON` | `0 4 * * *` | Planification de la synchronisation |
| `MEDS_SYNC_JITTER` | `30m` | Décalage aléatoire, par courtoisie envers l'ANSM |
| `MEDS_SYNC_ON_START` | `true` | Synchroniser si aucun snapshot n'est présent |
| `MEDS_RATE_LIMIT` | `100` | Requêtes par seconde et par clé |
| `MEDS_RATE_BURST` | `200` | Rafale autorisée |
| `MEDS_CORS_ORIGINS` | *(vide)* | Origines autorisées ; vide ⇒ CORS désactivé |
| `MEDS_MAX_REJECT_RATIO` | `0.01` | Seuil de rejet au-delà duquel l'ingestion échoue |
| `MEDS_SNAPSHOT_KEEP` | `3` | Nombre de snapshots conservés |
| `MEDS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## 8. Mise à l'échelle horizontale

L'état étant intégralement dérivé d'un snapshot immuable, plusieurs instances peuvent servir la
même donnée sans coordination. Deux montages possibles :

1. **Volume partagé en lecture seule** — une instance dédiée à la synchronisation écrit, les
   autres démarrent avec `MEDS_SYNC_CRON` vide et surveillent le répertoire. Simple, cohérent.
2. **Instances indépendantes** — chacune synchronise pour son compte, avec un jitter suffisant.
   Plus de trafic vers l'ANSM, à éviter au-delà de deux ou trois instances.

En pratique, une seule instance suffit largement : le budget de 20 000 req/s sur 2 vCPU
([07](07-performance.md)) couvre un usage national confortable. La mise à l'échelle relève de la
disponibilité, pas de la capacité.

## 9. Ce que l'architecture exclut délibérément

| Écarté | Raison |
|---|---|
| Base de données (SQLite, Postgres…) | [ADR 0002](adr/0002-memoire-plutot-que-sqlite.md) |
| Cache Redis/Memcached | Le `Store` **est** le cache, en mémoire du processus. Un aller-retour réseau serait plus lent que le calcul. |
| Framework web | 17 endpoints en lecture seule ; `ServeMux` suffit depuis Go 1.22 |
| Microservices | Un domaine unique, une donnée unique, 22 Mo. Le découpage n'aurait aucun bénéfice. |
| gRPC / GraphQL | Les intégrateurs santé consomment du REST/JSON ; ajouter un protocole doublerait la surface |
| Mise en cache des réponses | Réponses déjà calculées en microsecondes ; l'`ETag` délègue le cache au client |
