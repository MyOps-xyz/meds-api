# 12 — Backlog des tâches

## Mode d'emploi

56 tâches réparties en 10 lots. Chaque tâche porte :

- un **identifiant** `T-NN` stable, référencé dans les autres documents ;
- une **estimation** en points (1 pt ≈ une demi-journée pour un développeur Go confirmé) ;
- ses **dépendances** — le graphe est acyclique et vérifié en §12 ;
- des **critères d'acceptation testables**. Jamais « faire au mieux » : une tâche est terminée ou
  ne l'est pas, et la lecture des critères doit suffire à trancher.

**Total : 127 points**, soit environ 13 semaines pour un développeur seul, ou 6 à 7 semaines pour
une équipe de trois en exploitant le parallélisme du §11.

| Lot | Tâches | Points |
|---|---:|---:|
| 0 — Fondations | 4 | 6 |
| 1 — Ingestion | 8 | 21 |
| 2 — Cœur mémoire | 6 | 14 |
| 3 — Recherche | 5 | 13 |
| 4 — API HTTP | 10 | 22 |
| 5 — Sécurité | 6 | 13 |
| 6 — Ordonnancement | 3 | 7 |
| 7 — Observabilité | 3 | 6 |
| 8 — Conteneurisation | 6 | 12 |
| 9 — Qualité | 5 | 13 |
| **Total** | **56** | **127** |

### Définition de terminé (commune à toutes les tâches)

1. Code écrit, avec tests unitaires atteignant la couverture de [11](11-strategie-de-tests.md).
2. `golangci-lint run` sans avertissement.
3. `go test -race ./...` au vert.
4. Critères d'acceptation de la tâche vérifiés.
5. Documentation mise à jour si le comportement décrit dans `docs/` a changé.
6. Revue par un pair.

---

## Lot 0 — Fondations (4 tâches, 6 pts)

> Objectif : un dépôt où l'on peut travailler avec des garde-fous dès la première ligne de code.

### T-01 — Squelette du projet · 1 pt · aucune dépendance
Arborescence de [02](02-architecture.md#32-arborescence-du-code), `go.mod` en Go 1.26.6,
`.gitignore`, `.gitattributes` (marquant `testdata/golden/input/** binary`), licence, `README.md`.

**Acceptation** — `go build ./...` réussit ; l'arborescence correspond à celle documentée ;
`.gitattributes` empêche toute conversion d'encodage sur les golden files.

### T-02 — Outillage de développement · 1 pt · T-01
`Makefile` (`build`, `test`, `lint`, `run`, `sync`, `bench`, `docker`), `.golangci.yml` activant
gosec, errcheck, revive, staticcheck, ineffassign — plus govet, unused, bodyclose, errorlint,
copyloopvar et nolintlint.

**misspell est écarté**, contrairement à l'énoncé initial. Il vérifie l'orthographe anglaise, or
les commentaires de ce dépôt sont en français. Mesuré le 15/08/2026 : 86 signalements, **aucun
vrai positif** — jusqu'à `licence` → `license`, qui serait une faute puisque « Licence Ouverte »
est le nom officiel de la licence des données. Un linter qui n'émet que du bruit apprend à ignorer
la sortie du lint, ce qui coûte plus cher que son absence.

**Acceptation** — `make lint` et `make test` fonctionnent sur un dépôt propre ; `make help`
liste les cibles.

### T-03 — Intégration continue · 2 pts · T-02
GitHub Actions selon [11](11-strategie-de-tests.md#9-intégration-continue) : lint, test `-race`,
seuils de couverture, `govulncheck`, build multi-arch, `trivy`, `spectral`.

**Acceptation** — le pipeline passe au vert sur le squelette ; une régression volontaire (test
cassé, vulnérabilité connue) le fait échouer.

### T-04 — Configuration · 2 pts · T-01
Chargement et **validation stricte** des variables de
[02](02-architecture.md#7-configuration), avec valeurs par défaut sûres.

**Acceptation** — démarrage refusé si `MEDS_API_KEYS` est absent, avec un message explicite ;
une valeur invalide (cron mal formé, limite négative) empêche le démarrage ; tous les paramètres
documentés sont pris en compte.

---

## Lot 1 — Ingestion (8 tâches, 21 pts)

> **Lot le plus critique du projet.** Tous les pièges de
> [01](01-analyse-source-bdpm.md#5-pièges-de-la-source) s'y traitent. Les golden files de T-12
> conditionnent la fiabilité de tout ce qui suit.

### T-05 — Client HTTP ANSM · 2 pts · T-04
Téléchargement parallèle des 10 fichiers ([09](09-pipeline-mise-a-jour.md#31-téléchargement)) :
`errgroup` limité à 4, 3 tentatives, backoff exponentiel avec aléa, délais, `User-Agent`
identifiable, `io.LimitReader` à 100 Mio, TLS strict.

**Acceptation** — les 10 fichiers sont récupérés en moins de 60 s ; une source simulée renvoyant
`500` déclenche 3 tentatives puis un abandon propre ; un fichier en échec annule la totalité ;
aucune requête ne dépasse le délai imparti.

### T-06 — Détection d'encodage · 2 pts · T-05
Par fichier, à chaque synchronisation (P1). UTF-8 valide → tel quel ; sinon Windows-1252.
L'encodage retenu est inscrit au manifest.

**Acceptation** — `CIS_CIP_bdpm.txt` détecté UTF-8, `CIS_bdpm.txt` détecté Windows-1252 ;
`comprimé` correctement décodé dans les deux cas ; un fichier basculant d'un encodage à l'autre
entre deux syncs est traité sans intervention.

### T-07 — Lecteur TSV robuste · 3 pts · T-06
`bufio.Scanner` avec buffer de 1 Mio, `TrimRight("\r")`, saut des lignes vides, découpage sur
tabulation. **Sans `encoding/csv`** (P7, P8).

**Acceptation** — les 8 pièges de terminaison et de guillemets sont couverts par un test ;
`CIS_MITM.txt` restitue bien **7 711** enregistrements malgré l'absence de saut de ligne final ;
un guillemet nu est conservé littéralement ; aucune panique sur entrée malformée.

### T-08 — Parsers et normalisation · 5 pts · T-07
Un parser par fichier (10) + normalisation de
[09](09-pipeline-mise-a-jour.md#35-normalisation) : dates (deux formats), prix en centimes avec
arrondi, taux, multi-valeurs, libellés dérivés des codes, URLs en HTTPS, vide → `null` (P3, P4, P5).

**Acceptation** — les 10 fichiers réels sont parsés intégralement ; `24,34` → `2434` (et non
`2433`) ; `65 %` et `65%` → `65` ; `FT` et `ST` acceptés ; libellé de rupture dérivé du code avec
`libelle_source` conservé ; prix absent → `null` et non `0`.

### T-09 — Validation et quarantaine · 3 pts · T-08
Trois niveaux de contrôle ([09](09-pipeline-mise-a-jour.md#36-validation)) : structure, intégrité
référentielle, vraisemblance globale. Quarantaine par fichier et par motif (P6).

**Acceptation** — les CIS orphelins (8 831 mesurés, tous fichiers confondus) sont comptés **hors
périmètre** et non en rejet, sans faire échouer l'ingestion ([ADR 0007](adr/0007-hors-perimetre-distinct-du-rejet.md)) ;
un taux de rejet simulé de 5 % fait échouer l'ingestion ; un fichier vide fait échouer ; une chute
de 50 % du nombre de spécialités fait échouer ; un taux hors périmètre supérieur à 20 % fait échouer.

### T-10 — Entités dérivées · 2 pts · T-08
Construction du référentiel `substances` par agrégation, résolution des liens HAS dans les avis,
dénormalisation des groupes génériques avec leurs membres.

**Acceptation** — 3 895 substances distinctes produites ; chaque avis SMR/ASMR porte son
`lien_avis_ct` quand le code de dossier existe dans `HAS_LiensPageCT_bdpm.txt` (10 445 avis SMR
sur 11 247 mesurés le 15/08/2026) ; les groupes portent leurs membres triés par ordre.

**1 524 groupes servis, et non 1 671.** La source en compte bien 1 671, mais 147 d'entre eux n'ont
**aucun** membre dans le référentiel courant — tous leurs médicaments ont été retirés du marché.
Les servir reviendrait à exposer des coquilles vides. Le chiffre de 1 671 était un décompte de la
source, pas du résultat.

### T-11 — Écriture du snapshot · 3 pts · T-09, T-10
NDJSON par entité + `manifest.json` ; écriture temporaire puis `os.Rename` ; lien `current` ;
hash global ; purge selon `MEDS_SNAPSHOT_KEEP`.

**Acceptation** — écriture atomique prouvée par interruption du processus en cours d'écriture :
`current` pointe toujours vers un snapshot valide ; le manifest est écrit en dernier ; deux
exécutions sur la même entrée produisent des fichiers **identiques octet pour octet** ; les `.tmp-*`
orphelins sont purgés au démarrage.

### T-12 — Golden files · 1 pt · T-11
Échantillons figés des 10 fichiers **dans leur encodage d'origine**, snapshot attendu, mode
`-update`, tests de non-régression couvrant les 8 pièges
([11](11-strategie-de-tests.md#4-golden-files)).

**Acceptation** — les 8 pièges ont chacun un test nommé ; `-update` régénère ; une modification
de parser non intentionnelle fait échouer le test ; les fichiers d'entrée restent en Latin-1 après
un aller-retour Git.

---

## Lot 2 — Cœur mémoire (6 tâches, 14 pts)

### T-13 — Structures SoA · 2 pts · T-01
Entités de [03](03-modele-de-donnees.md#41-structure-de-tableaux-plutôt-que-tableau-de-structures) :
slices contiguës, CIS en `uint32`, énumérations en `uint8`.

**Acceptation** — `unsafe.Sizeof(Spec{})` ≤ 128 octets ; les énumérations couvrent toutes les
valeurs observées dans la source ; une valeur inattendue est traçable et non silencieuse.

### T-14 — Table d'internement · 1 pt · T-13
Formes, voies, titulaires, statuts en `uint16` + table de correspondance.

**Acceptation** — `Resolve(Intern(s)) == s` pour toute chaîne ; 367 formes, 68 voies et
666 titulaires internés ; gain mémoire mesuré et consigné.

### T-15 — Index CSR · 2 pts · T-13
Relations 1-N en Compressed Sparse Row
([03](03-modele-de-donnees.md#42-relations-1-n-en-format-csr)).

**Acceptation** — `Get` renvoie exactement les éléments du parent, bornes comprises ; un parent
sans enfant renvoie une slice vide non nulle ; aucune allocation dans `Get` (vérifié par
`-benchmem`).

### T-16 — Index d'accès direct · 2 pts · T-13
`byCIS`, `byCIP13`, `byCIP7`, `bySubstance`, `byGroupe`, `byDossierHAS`.

**Acceptation** — les 15 857 CIS et 20 903 CIP13 sont résolus ; CIP7 et CIP13 mènent à la même
présentation ; un code inconnu renvoie « non trouvé » sans panique.

### T-17 — Construction du `Store` · 3 pts · T-11, T-14, T-15, T-16
Lecture du snapshot, construction de toutes les structures et de tous les index.

**Acceptation** — construction en **moins de 500 ms** sur le dataset réel ; heap ≤ 100 Mio après
construction ; le `Store` est complet et cohérent (compteurs égaux au manifest).

### T-18 — Bascule atomique · 4 pts · T-17
`atomic.Pointer[Store]`, publication, immuabilité, surveillance du répertoire de snapshots.

**Acceptation** — `TestStore_Immutable` et `TestConcurrentReadDuringSwap` au vert sous `-race` ;
100 lecteurs concurrents pendant 10 bascules : aucune course, aucune réponse incohérente ;
l'ancien `Store` est bien libéré (RSS revenu à son niveau 30 s après).

---

## Lot 3 — Recherche (5 tâches, 13 pts)

### T-19 — Normalisation Unicode · 2 pts · T-01
Chaîne NFD → suppression des diacritiques → minuscules → ponctuation
([05](05-conception-recherche.md#2-normalisation)). **Code unique** pour l'index et la requête.

**Acceptation** — `PARACÉTAMOL` → `paracetamol` ; `D'AMLODIPINE` → tokens `amlodipine` ;
`TestNormalizeSymmetry` prouve qu'index et requête empruntent le même code.

### T-20 — Index inversé · 3 pts · T-19, T-17
Tokens triés, postings triés, pondération par champ, intersection du plus court au plus long.

**Acceptation** — ~25 000 tokens indexés ; l'intersection démarre par la liste la plus courte
(vérifié par test) ; recherche exacte < 500 µs au p99.

### T-21 — Index trigramme · 3 pts · T-20
Repli déclenché sous 5 résultats, seuil de Jaccard à 0,4, score minoré de 40 %.

**Acceptation** — `paracetamo` et `paracetamoll` retrouvent `PARACÉTAMOL` ; le repli **ne se
déclenche pas** quand la recherche exacte suffit (vérifié par métrique) ; empreinte ≤ 25 Mio.

### T-22 — Classement · 3 pts · T-20
BM25 allégé + bonus métier, paramètres regroupés dans `RankParams`
([05](05-conception-recherche.md#4-classement)).

**Acceptation** — `doliprane` place `DOLIPRANE` en première position ; les spécialités
commercialisées précèdent les non commercialisées à pertinence égale ; les paramètres sont
modifiables sans toucher à l'algorithme.

### T-23 — Autocomplétion et jeu de référence · 2 pts · T-21, T-22
`/v1/suggest` par dichotomie sur les tokens triés ; `testdata/search_golden.json`.

**Acceptation** — les 13 cas de
[05](05-conception-recherche.md#7-jeu-de-requêtes-de-référence) passent ; precision@5 ≥ 0,8 ;
MRR ≥ 0,85 ; autocomplétion < 1 ms au p99.

---

## Lot 4 — API HTTP (10 tâches, 22 pts)

### T-24 — Serveur et arrêt gracieux · 2 pts · T-04
Délais de [06](06-securite.md#5-durcissement-du-serveur-http), `Shutdown` sur `SIGTERM`.

**Acceptation** — `ReadHeaderTimeout` coupe une connexion Slowloris simulée ; à l'arrêt, les
requêtes en cours terminent (délai de grâce 15 s) et aucune nouvelle n'est acceptée.

### T-25 — Routage · 1 pt · T-24
`net/http.ServeMux` avec patrons méthode+chemin, sans dépendance externe.

**Acceptation** — toutes les routes de [04](04-specification-api.md#2-endpoints) sont déclarées ;
une méthode non autorisée renvoie `405` ; le patron de route est disponible pour l'étiquetage des
métriques.

### T-26 — Rendu JSON · 2 pts · T-25
`json.Encoder`, tampons `sync.Pool`, vues distinctes liste/fiche, gzip au-delà de 1 Kio.

**Acceptation** — aucune allocation de tampon par requête (vérifié `-benchmem`) ; la vue liste
omet composition et avis ; gzip appliqué seulement au-delà du seuil et si le client l'accepte.

### T-27 — Erreurs RFC 9457 · 1 pt · T-25
`application/problem+json`, catalogue de [04](04-specification-api.md#13-format-derreur-rfc-9457).

**Acceptation** — les 8 types d'erreur sont produits avec le bon statut ; `request_id` toujours
présent ; aucune trace d'exécution ni détail interne ne fuit dans un `500`.

### T-28 — Pagination par curseur · 2 pts · T-26
Curseur opaque `{offset, hash_dataset}` en base64url.

**Acceptation** — parcours complet d'un jeu de 231 résultats sans doublon ni omission ; un curseur
émis avant un rechargement renvoie `400 cursor_stale` ; un curseur forgé renvoie `400` sans panique.

### T-29 — ETag et 304 · 2 pts · T-26
Hash dataset + route + paramètres normalisés.

**Acceptation** — `ETag` identique pour `?limit=20&q=x` et `?q=x` ; `If-None-Match` concordant →
`304` sans corps ; l'`ETag` change après un rechargement de dataset ; il ne dépend pas de
`Accept-Encoding`.

### T-30 — Handlers médicaments · 4 pts · T-18, T-22, T-26, T-28
`/v1/medicaments`, `/{cis}` avec `include`, et les 5 sous-ressources.

**Acceptation** — tous les filtres de
[04](04-specification-api.md#21-get-v1medicaments--recherche-et-filtres) fonctionnent et se
combinent en ET ; CIS inconnu → `404`, CIS sans présentation → `200` + tableau vide ;
`include=all` renvoie toutes les sections.

### T-31 — Handlers référentiels · 4 pts · T-30
`/v1/presentations/{cip}`, `/v1/substances*`, `/v1/groupes-generiques*`, `/v1/ruptures`,
`/v1/mitm`, `/v1/suggest`, `/v1/dataset`.
Inclut la **mention de licence et l'avertissement d'usage**
([04](04-specification-api.md#6-avertissement-obligatoire)).

**Acceptation** — CIP7 et CIP13 mènent au même résultat, parent inclus ; `/v1/dataset` expose
version, âge, compteurs, quarantaine, licence et éditeur ; l'avertissement figure dans l'OpenAPI
et sur `/docs`.

### T-32 — Validation des entrées · 2 pts · T-30, T-31
Liste blanche sur tous les paramètres ([06](06-securite.md#validation-des-entrées)).

**Acceptation** — CIS non numérique, `limit=99999`, `q` de 10 000 caractères, énumération inconnue
→ `400` explicites ; un paramètre inconnu est ignoré sans erreur ; aucune panique sur entrée
hostile.

### T-33 — OpenAPI et documentation · 2 pts · T-31
`api/openapi.yaml` embarqué (`go:embed`), servi sur `/openapi.json`, documentation sur `/docs`.

Le contrat existe en **un seul exemplaire**. `go:embed` ne pouvant pas remonter au-dessus de son
paquet, c'est `api/` qui est un paquet Go (`api/openapi.go`, paquet `openapi`) embarquant son
propre fichier ; `internal/api` l'importe. Maintenir une copie synchronisée par un test avait été
essayé puis écarté : deux fichiers versionnés pour un même contrat sont une source de dérive.

**Acceptation** — `spectral lint` sans erreur ; chaque endpoint de
[04](04-specification-api.md) est décrit avec ses paramètres et un exemple ; les réponses réelles
sont conformes au schéma déclaré ; `/docs` fonctionne **hors ligne** (aucune ressource externe).

---

## Lot 5 — Sécurité (6 tâches, 13 pts)

### T-34 — Authentification par clés · 2 pts · T-25
SHA-256 + `subtle.ConstantTimeCompare`, effacement de la variable d'environnement après hachage
([06](06-securite.md#3-authentification-par-clés-api)).

**Acceptation** — clé absente/invalide/valide → `401`/`401`/`200` ; aucune corrélation temporelle
mesurable sur 10 000 essais ; les clés ne sont plus lisibles dans `/proc/self/environ` après le
démarrage ; démarrage refusé sans clé configurée.

### T-35 — Limitation de débit · 3 pts · T-34
Token bucket shardé, par clé et par IP, purge des buckets inactifs, `X-Forwarded-For` honoré
**uniquement** depuis un proxy de confiance.

**Acceptation** — le dépassement renvoie `429` avec `Retry-After` et les en-têtes `RateLimit-*` ;
un `X-Forwarded-For` falsifié depuis une IP non déclarée est ignoré ; la table de buckets ne croît
pas indéfiniment sous attaque distribuée (test de 1 M d'IP distinctes).

### T-36 — Durcissement HTTP · 1 pt · T-24
`MaxBytesReader`, `MaxHeaderBytes`, timeout de contexte par requête.

**Acceptation** — corps de 10 Mio → `413` ; en-têtes de 1 Mio → rejet ; un handler lent est
interrompu à 5 s.

### T-37 — En-têtes et CORS · 2 pts · T-25
En-têtes de sécurité, CORS sur liste blanche, retrait de `Server`.

**Acceptation** — tous les en-têtes de [06](06-securite.md#6-en-têtes-de-réponse) sont présents ;
CORS désactivé par défaut ; jamais `*` avec `Allow-Credentials` ; une origine non listée est
refusée.

### T-38 — Journalisation et request ID · 2 pts · T-25
ULID propagé dans le `context`, logs `slog` JSON, masquage, `recover`.

**Acceptation** — `X-Request-Id` présent sur chaque réponse et dans chaque log associé ;
`Authorization` jamais journalisé ; une panic simulée produit un `500` générique sans trace, le
processus survit ; un `X-Request-Id` client non valide est régénéré.

### T-39 — Tests d'abus · 3 pts · T-34, T-35, T-36, T-37, T-38
Les 10 scénarios de [06](06-securite.md#11-vérification).

**Acceptation** — les 10 scénarios passent et sont automatisés en CI.

---

## Lot 6 — Ordonnancement (3 tâches, 7 pts)

### T-40 — Scheduler interne · 3 pts · T-11, T-18
Expression cron, jitter, verrou anti-concurrence, synchronisation au démarrage si aucun snapshot.

**Acceptation** — le cron déclenche à l'heure prévue avec un décalage aléatoire dans la fenêtre ;
deux déclenchements simultanés n'en exécutent qu'un ; sans snapshot au démarrage, une
synchronisation est lancée ; `MEDS_SYNC_CRON` vide désactive proprement le scheduler.

### T-41 — `POST /admin/sync` · 2 pts · T-40, T-34
Asynchrone (`202`), `409` si déjà en cours, clé d'administration distincte, endpoint désactivé si
`MEDS_ADMIN_KEY` est absent.

**Acceptation** — `202` avec `sync_id` ; `409` en cas de concurrence ; une clé API ordinaire est
refusée ; l'endpoint renvoie `404` si aucune clé admin n'est configurée.

### T-42 — CLI `bdpm-sync` · 2 pts · T-11
Synchronisation one-shot, options `--data-dir`, `--force`, `--dry-run`, code de sortie explicite.

**Acceptation** — écrit un snapshot exploitable par le serveur ; `--dry-run` n'écrit rien et
affiche le rapport ; code de sortie non nul en cas d'échec (utilisable en cron) ; sortie lisible
en mode interactif et JSON en mode non interactif.

---

## Lot 7 — Observabilité (3 tâches, 6 pts)

### T-43 — Métriques Prometheus · 3 pts · T-25, T-40
Les 4 familles de [10](10-observabilite.md#3-métriques-prometheus), avec le collecteur Go.

**Acceptation** — `/metrics` expose toutes les métriques documentées ; l'étiquette `route` est le
**patron** et non le chemin réel (vérifié : 15 857 requêtes CIS distinctes ne créent qu'une série) ;
les bornes d'histogramme correspondent aux budgets de [07](07-performance.md).

### T-44 — Sondes de santé · 1 pt · T-18
`/healthz` et `/readyz` avec la distinction de [10](10-observabilite.md).

**Acceptation** — `/healthz` répond `200` dès le démarrage, même sans dataset ; `/readyz` renvoie
`503` tant qu'aucun `Store` n'est publié, puis `200` ; `/readyz` ne juge pas la fraîcheur.

### T-45 — Alertes et tableau de bord · 2 pts · T-43
`deploy/prometheus/alerts.yml` et tableau de bord Grafana.

**Acceptation** — les 10 alertes de [10](10-observabilite.md#5-alertes) sont définies et validées
par `promtool check rules` ; le tableau de bord affiche les budgets de [07](07-performance.md) en
lignes de référence.

---

## Lot 8 — Conteneurisation (6 tâches, 12 pts)

### T-46 — Dockerfile · 2 pts · T-24, T-42
Multi-stage, distroless, caches de build, injection de version.

**Acceptation** — image < 25 Mio ; `docker run` démarre l'API ; `--version` affiche version,
commit et date de build ; build reproductible (deux builds du même commit → même digest de binaire).

### T-47 — Durcissement du conteneur · 1 pt · T-46
Non-root, rootfs en lecture seule, `cap_drop: ALL`, `no-new-privileges`, tmpfs.

**Acceptation** — le processus tourne en UID 65532 ; toute écriture hors `/data` et `/tmp` échoue ;
`docker exec sh` échoue (aucun shell) ; l'API fonctionne néanmoins normalement, synchronisation
comprise.

### T-48 — Compose et profils · 3 pts · T-46
`docker-compose.yml` de [08](08-deploiement-docker.md#3-docker-compose), volumes, réseaux,
healthcheck, limites de ressources.

**Acceptation** — Compose refuse de démarrer sans `MEDS_API_KEYS` ; le healthcheck fonctionne sans
`curl` ; les volumes persistent après `down`/`up` ; les limites de ressources sont appliquées.

### T-49 — Profil Caddy · 2 pts · T-48
`Caddyfile`, TLS automatique, restriction de `/metrics`, HTTP/3.

**Acceptation** — `--profile caddy up -d` donne une API en HTTPS sur une machine vierge ; en
`DOMAIN=localhost`, Caddy génère son AC interne et le certificat est exploitable après import ;
`/metrics` est refusé depuis l'extérieur ; HTTP redirige vers HTTPS.

### T-50 — Profil Traefik · 2 pts · T-48
`traefik.yml`, middlewares, labels, procédure `mkcert` documentée pour le local.

**Acceptation** — `--profile traefik up -d` donne une API en HTTPS ; `exposedByDefault: false`
vérifié (aucun service non étiqueté n'est exposé) ; mêmes en-têtes de sécurité que le profil
Caddy ; `/metrics` restreint.

### T-51 — Documentation d'exploitation · 2 pts · T-49, T-50
`.env.example`, `README` racine, procédures de [08](08-deploiement-docker.md#8-exploitation).

**Acceptation** — un tiers déploie l'API sur une machine vierge **en suivant uniquement le
README**, sans poser de question ; les procédures de diagnostic sans shell sont documentées ; la
génération d'une clé API est expliquée.

---

## Lot 9 — Qualité (5 tâches, 13 pts)

### T-52 — Couverture unitaire · 3 pts · lots 1 à 5
Atteindre les seuils de [11](11-strategie-de-tests.md#1-ce-quil-faut-réellement-prouver).

**Acceptation** — ≥ 85 % sur `internal/bdpm` et `internal/store/search`, ≥ 80 % global ; seuils
imposés en CI ; aucun test ignoré (`t.Skip`) sans justification écrite.

### T-53 — Tests d'intégration · 3 pts · T-33
Serveur complet sur snapshot figé, contrat, erreurs, pagination, cache, sécurité.

**Acceptation** — chaque endpoint est couvert ; le **test transversal d'atteignabilité des
champs** (objectif O1) passe ; les réponses sont conformes au schéma OpenAPI.

### T-54 — Fuzzing · 2 pts · T-08, T-28
Cibles : 10 parsers, normalisation, curseur, requête de recherche.

**Acceptation** — 1 min par cible en CI sans échec ; le corpus des entrées ayant échoué est
commité dans `testdata/fuzz/` ; aucune panique, aucun blocage, aucune consommation mémoire
anormale.

### T-55 — Tests de charge · 3 pts · T-48
Les 4 scénarios k6 de [11](11-strategie-de-tests.md#7-tests-de-charge).

**Acceptation** — tous les budgets de [07](07-performance.md#2-budgets) tenus ; `reload.js`
produit **zéro requête en erreur** ; `soak.js` montre un RSS stable sur 1 h ; `spike.js` dégrade
en `429` sans effondrement.

### T-56 — Tests de fumée et recette finale · 2 pts · T-51, T-55
Fumée sur les deux profils, puis vérification des 5 critères d'acceptation du projet
([00](00-vision-et-perimetre.md#8-critères-dacceptation-du-projet)).

**Acceptation** — la fumée passe sur `caddy` **et** sur `traefik` ; les 7 objectifs O1–O7 sont
vérifiés ; les 8 pièges ont leur test de non-régression ; `trivy` et `govulncheck` sans
vulnérabilité haute ou critique.

---

## 11. Ordonnancement

### Chemin critique

```
T-01 → T-04 → T-05 → T-06 → T-07 → T-08 → T-09 → T-11 → T-17 → T-18 → T-30 → T-53 → T-56
```

**Le lot 1 est le goulot d'étranglement.** Tant que l'ingestion ne produit pas un snapshot fiable,
rien d'autre ne peut être validé sur des données réelles. Il est donc à attaquer en premier, sans
le paralléliser à l'excès : la qualité y prime sur la vitesse.

### Parallélisation à trois développeurs

| Phase | Dév. A (ingestion) | Dév. B (API) | Dév. C (infra) |
|---|---|---|---|
| S1 | T-01, T-04, T-05 | T-13, T-14 | T-02, T-03 |
| S2 | T-06, T-07, T-08 | T-15, T-16, T-19 | T-46, T-47 |
| S3 | T-09, T-10, T-11 | T-20, T-21, T-24, T-25 | T-48, T-49 |
| S4 | T-12, T-17, T-18 | T-22, T-23, T-26, T-27 | T-50, T-43 |
| S5 | T-40, T-41, T-42 | T-28…T-33 | T-44, T-45, T-51 |
| S6 | T-52, T-54 | T-34…T-39 | T-55 |
| S7 | *recette commune* : T-53 | *recette commune* : T-53 | *recette commune* : T-56 |

Le développeur B peut démarrer les structures mémoire (T-13 à T-16) **avant** que l'ingestion soit
prête, en travaillant sur un snapshot écrit à la main. C'est ce qui permet de ne pas sérialiser
toute l'équipe derrière le lot 1.

### Jalons

| Jalon | Tâches | Démontre |
|---|---|---|
| **J1 — Ingestion fiable** | T-01…T-12 | Un snapshot correct est produit depuis la source réelle |
| **J2 — Lecture en mémoire** | T-13…T-23 | Lookup et recherche fonctionnels en ligne de commande |
| **J3 — API utilisable** | T-24…T-33 | Contrat complet, documenté, testable |
| **J4 — Exposable** | T-34…T-45 | Sécurisée, supervisée, mise à jour automatiquement |
| **J5 — Livrable** | T-46…T-56 | Déployable en une commande, budgets prouvés |

---

## 12. Contrôle de cohérence du backlog

Vérifications à repasser à chaque modification de ce document.

**Acyclicité** — les dépendances ne remontent jamais vers un identifiant supérieur, à l'exception
de T-52 (qui dépend des lots 1 à 5, tous antérieurs). Le graphe est donc trivialement acyclique.

**Couverture des endpoints** — les 17 routes de [04](04-specification-api.md#2-endpoints) sont
couvertes par T-30 (médicaments et sous-ressources), T-31 (référentiels, `/v1/dataset`,
`/v1/suggest`), T-41 (`/admin/sync`), T-44 (`/healthz`, `/readyz`), T-43 (`/metrics`) et
T-33 (`/openapi.json`, `/docs`).

**Couverture des fichiers source** — les 10 fichiers sont traités par T-08 (parsers) et vérifiés
par T-12 (golden files).

**Couverture des pièges** — P1→T-06, P2→T-05 et T-11, P3→T-08, P4→T-08, P5→T-08, P6→T-09,
P7→T-07, P8→T-07. Tous sont testés en T-12.

**Couverture des objectifs** — la matrice de
[11](11-strategie-de-tests.md#11-matrice-de-couverture-des-objectifs) relie O1–O7 à leur preuve.
