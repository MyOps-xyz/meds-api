# meds-api

> API REST en lecture seule exposant la **Base de Données Publique des Médicaments** (BDPM),
> publiée par l'ANSM avec le concours de la HAS et du ministère de la Santé.

[![CI](https://github.com/MyOps-xyz/meds-api/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/MyOps-xyz/meds-api/actions/workflows/ci.yml)
[![Release](https://github.com/MyOps-xyz/meds-api/actions/workflows/release.yml/badge.svg)](https://github.com/MyOps-xyz/meds-api/actions/workflows/release.yml)
[![Go](https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white)](go.mod)
[![Couverture](https://img.shields.io/badge/couverture-%E2%89%A5%2080%25-brightgreen)](.github/scripts/check-coverage.sh)
[![Image](https://img.shields.io/badge/image-distroless%20%3C%2025%20Mio-2496ED?logo=docker&logoColor=white)](Dockerfile)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539?logo=openapiinitiative&logoColor=white)](api/openapi.yaml)
[![Licence code](https://img.shields.io/badge/licence%20code-MIT-blue)](LICENSE)
[![Licence données](https://img.shields.io/badge/licence%20donn%C3%A9es-Etalab%202.0-orange)](https://www.etalab.gouv.fr/licence-ouverte-open-licence/)

Le jeu de données complet — **22 Mo pour 15 857 médicaments** — tient intégralement en RAM.
L'API charge un index immuable au démarrage et le remplace atomiquement à chaque mise à jour :
ni base de données, ni cache externe, ni verrou sur le chemin de lecture.

## Sommaire

- [Fonctionnalités](#fonctionnalités)
- [Démarrage rapide](#démarrage-rapide)
- [Endpoints](#endpoints)
- [Configuration](#configuration)
- [Déploiement](#déploiement)
- [Développement](#développement)
- [Architecture](#architecture)
- [État du projet](#état-du-projet)
- [Contribuer](#contribuer)
- [Sécurité](#sécurité)
- [Licence et attribution](#licence-et-attribution)

## Fonctionnalités

- **Recherche plein texte tolérante aux fautes** — index inversé et trigrammes, sans dépendance
  externe.
- **Index immuable en mémoire** — structures SoA / CSR, remplacement atomique du snapshot, aucun
  verrou sur le chemin de lecture.
- **Synchronisation ANSM intégrée** — ordonnanceur cron avec décalage aléatoire, ou CLI
  `bdpm-sync` en one-shot pour un déploiement sans accès sortant.
- **Bascule à chaud** — un nouveau snapshot est adopté sans redémarrage ni requête perdue.
- **Cache HTTP natif** — `ETag` et `304 Not Modified` court-circuités avant tout travail de
  rendu.
- **Sécurité par défaut** — clés API en comparaison à temps constant, limitation de débit par
  clé, configuration validée au démarrage, image `distroless` sans shell.
- **Observabilité** — métriques Prometheus, journaux JSON structurés, `/healthz`, `/readyz` et
  `/v1/dataset` pour diagnostiquer sans accès au conteneur.
- **Contrat explicite** — OpenAPI 3.1 embarqué, servi sur `/openapi.json` et `/docs`, erreurs au
  format RFC 9457.

## Démarrage rapide

### Avec Docker (recommandé)

Une machine vierge, une commande, une API en HTTPS avec un certificat valide, sans étape manuelle
de génération de certificat :

```bash
git clone https://github.com/MyOps-xyz/meds-api.git
cd meds-api
cp .env.example .env                      # renseigner MEDS_API_KEYS
docker compose --profile caddy up -d
curl -H "Authorization: Bearer $KEY" "https://localhost/v1/medicaments?q=doliprane"
```

Au premier démarrage aucun snapshot n'existe : l'API synchronise depuis l'ANSM (~30 s), `/readyz`
répond `503` pendant ce temps, puis `200`. Le service ne ment jamais sur son état.

### Depuis les sources

Prérequis : **Go 1.26.6**, `golangci-lint` (via `make tools`), Docker 27+ pour les cibles de
conteneurisation.

```bash
make build                                     # compile meds-api et bdpm-sync dans ./bin
MEDS_API_KEYS=$(openssl rand -base64 32 | tr -d '=+/') \
MEDS_DATA_DIR=./data ./bin/meds-api
```

## Endpoints

Toutes les routes `/v1/*` exigent l'en-tête `Authorization: Bearer <clé>`.
Le contrat complet — paramètres, schémas, codes d'erreur — est décrit dans
[`api/openapi.yaml`](api/openapi.yaml) et servi par l'API elle-même sur `/docs`.

| Méthode | Route | Rôle |
|---|---|---|
| `GET` | `/v1/medicaments` | Recherche et liste paginée des médicaments |
| `GET` | `/v1/medicaments/{cis}` | Fiche d'un médicament par code CIS |
| `GET` | `/v1/medicaments/{cis}/presentations` | Présentations (boîtes, CIP) |
| `GET` | `/v1/medicaments/{cis}/composition` | Composition en substances actives |
| `GET` | `/v1/medicaments/{cis}/generiques` | Groupe générique associé |
| `GET` | `/v1/medicaments/{cis}/avis` | Avis de la commission de la transparence |
| `GET` | `/v1/medicaments/{cis}/conditions` | Conditions de prescription et de délivrance |
| `GET` | `/v1/medicaments/{cis}/ruptures` | Ruptures et tensions d'approvisionnement |
| `GET` | `/v1/presentations/{cip}` | Présentation par code CIP7 ou CIP13 |
| `GET` | `/v1/substances` | Liste des substances actives |
| `GET` | `/v1/substances/{code}` | Fiche d'une substance |
| `GET` | `/v1/substances/{code}/medicaments` | Médicaments contenant la substance |
| `GET` | `/v1/groupes-generiques` | Liste des groupes génériques |
| `GET` | `/v1/groupes-generiques/{id}` | Fiche d'un groupe générique |
| `GET` | `/v1/ruptures` | Ruptures d'approvisionnement en cours |
| `GET` | `/v1/mitm` | Médicaments d'intérêt thérapeutique majeur |
| `GET` | `/v1/suggest` | Autocomplétion par préfixe |
| `GET` | `/v1/dataset` | Version, âge, compteurs et quarantaine du jeu de données |

Endpoints hors `/v1` : `/openapi.json` et `/docs` (contrat), `/healthz` et `/readyz` (sondes),
`/metrics` (Prometheus, réseaux privés uniquement), `POST /admin/sync` (synchronisation
déclenchée, désactivé si `MEDS_ADMIN_KEY` est absente).

## Configuration

Toute la configuration passe par des variables d'environnement (12-factor). Elle est **validée au
démarrage, et un démarrage invalide échoue immédiatement** : aucune valeur par défaut n'est
substituée en silence à un paramètre de sécurité. Les erreurs sont **agrégées** — un exploitant
découvre en un seul démarrage l'ensemble des corrections à apporter, plutôt qu'une à la fois au fil
des redémarrages.

| Variable | Défaut | Rôle |
|---|---|---|
| `MEDS_ADDR` | `:8080` | Adresse d'écoute |
| `MEDS_DATA_DIR` | `/data` | Répertoire des snapshots |
| `MEDS_API_KEYS` | *(requis)* | Clés API, séparées par des virgules |
| `MEDS_ADMIN_KEY` | *(vide)* | Clé pour `POST /admin/sync` ; endpoint désactivé si absente |
| `MEDS_SYNC_CRON` | `0 4 * * *` | Planification de la synchronisation |
| `MEDS_SYNC_JITTER` | `30m` | Décalage aléatoire, par courtoisie envers l'ANSM |
| `MEDS_SYNC_ON_START` | `true` | Synchroniser si aucun snapshot n'est présent |
| `MEDS_RATE_LIMIT` | `100` | Requêtes par seconde et par clé |
| `MEDS_RATE_BURST` | `200` | Rafale autorisée |
| `MEDS_CORS_ORIGINS` | *(vide)* | Origines autorisées ; vide ⇒ CORS désactivé |
| `MEDS_MAX_REJECT_RATIO` | `0.01` | Taux de rejet au-delà duquel l'ingestion échoue |
| `MEDS_SNAPSHOT_KEEP` | `3` | Nombre de snapshots conservés |
| `MEDS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

### Règles de validation

| Variable | Contrainte |
|---|---|
| `MEDS_ADDR` | Forme `hôte:port`, port numérique |
| `MEDS_API_KEYS` | Au moins une clé ; chaque clé ≥ 32 caractères ; doublons refusés |
| `MEDS_ADMIN_KEY` | Si présente : ≥ 32 caractères, et **différente de toute clé API** |
| `MEDS_SYNC_CRON` | Expression cron à 5 champs (`*`, listes `,`, plages `-`, pas `/`) |
| `MEDS_SYNC_JITTER` | Durée Go (`30m`, `1h`), positive ou nulle |
| `MEDS_RATE_LIMIT` / `MEDS_RATE_BURST` | Limite > 0 ; rafale ≥ limite |
| `MEDS_CORS_ORIGINS` | URL absolues `http`/`https`, sans chemin ; **le joker `*` est refusé** |
| `MEDS_MAX_REJECT_RATIO` | Dans `[0, 1]` |
| `MEDS_SNAPSHOT_KEEP` | ≥ 1 |

<details>
<summary><code>MEDS_SYNC_CRON</code> : absente et vide ne signifient pas la même chose</summary>

<br>

C'est la seule variable dont l'omission et la vacuité ont des sens opposés, et la distinction est
volontaire :

- **absente** → l'ordonnanceur tourne selon le défaut `0 4 * * *` ;
- **présente mais vide** → l'ordonnanceur est **désactivé**, ce qui est un état valide et non une
  erreur. C'est le réglage des instances secondaires d'un déploiement à volume partagé, où une
  seule instance synchronise ([architecture §8](docs/02-architecture.md#8-mise-à-léchelle-horizontale)).

La configuration est donc lue avec `os.LookupEnv` et non `os.Getenv`. Confondre les deux cas
imposerait de sacrifier l'un des deux comportements : soit un démarrage par défaut qui ne
resynchronise jamais, soit une désactivation devenue inexprimable.

</details>

### Clés API

Une clé se génère avec 32 octets d'entropie :

```bash
openssl rand -base64 32 | tr -d '=+/' | sed 's/^/mk_live_/'
```

Les clés ne sont **jamais journalisées** : la configuration expose un `LogValue` qui les remplace
par `[redacted] (n clés)`, de sorte qu'un `slog` de la configuration au démarrage reste sans fuite.
Le stockage en mémoire et la comparaison en temps constant relèvent de
[la spécification de sécurité](docs/06-securite.md#3-authentification-par-clés-api).

### Exemple

```bash
MEDS_API_KEYS=mk_live_kR3nP8xW2vQ7yT5mB9cF4hJ6dL1sA0zE
MEDS_ADMIN_KEY=mk_admin_9wX4tR7bN2kM5vC8pQ1jH6yF3dZ0sLuG
MEDS_CORS_ORIGINS=https://app.example.fr
MEDS_LOG_LEVEL=info
```

Le fichier [`.env.example`](.env.example) documente chaque variable, ses bornes et ses défauts.

## Déploiement

> **Vérifié de bout en bout le 15/08/2026** sur Docker 29.4, profils `caddy` et `traefik` :
> image de 20,1 Mo, première synchronisation depuis l'ANSM aboutie dans le conteneur, HTTPS servi
> sans étape manuelle. Pour héberger d'autres services sur la même machine, employer plutôt le
> frontal mutualisé [`deploy/edge/`](deploy/edge/README.md).

### Image

Multi-stage, binaire statique (`CGO_ENABLED=0`) sur `gcr.io/distroless/static-debian13:nonroot`.
Taille visée **< 25 Mio**. L'image ne contient **ni shell, ni gestionnaire de paquets, ni
coreutils** : un attaquant qui y obtiendrait l'exécution de code n'y trouve aucun outil. La
contrepartie est assumée — aucun `docker exec sh` de diagnostic, voir plus bas.

Chaque release publie automatiquement l'image sur le registre de conteneurs GitHub, en
`linux/amd64` et `linux/arm64`, avec provenance SLSA et SBOM attachés :

```bash
docker pull ghcr.io/myops-xyz/meds-api:1.0.0   # ou :1.0, :1, :latest
```

L'image est publique : aucune authentification n'est requise pour la tirer. Construction locale
équivalente : `make docker`.

### Deux frontaux, deux profils

| Profil | Frontal | Certificat local | Quand le choisir |
|---|---|---|---|
| `caddy` | Caddy 2.11.4 | AC interne générée automatiquement | **Défaut recommandé** |
| `traefik` | Traefik 3.7.10 | à fournir via `mkcert` | Parc déjà outillé pour Traefik |

```bash
docker compose --profile caddy   up -d     # ou
docker compose --profile traefik up -d
```

Les deux exposent les mêmes en-têtes de sécurité, redirigent HTTP vers HTTPS, activent HTTP/3 et
**refusent `/metrics` et `/debug/*`** — inconditionnellement, et non par filtrage d'adresse
source, qui est trompeur dès que le runtime masque l'adresse du client
([08 §4](docs/08-deploiement-docker.md#4-profil-caddy)). Prometheus scrute l'API en direct sur le
réseau interne. La différence entre les deux profils tient au développement local : Caddy génère
sa propre autorité de certification et signe un certificat sans openssl ni CSR, là où Traefik
exige un certificat fourni. C'est la raison du choix par défaut
([ADR 0005](docs/adr/0005-caddy-et-traefik-en-profils.md)).

### Plusieurs services sur la même machine

Ces deux profils publient `80:80` et `443:443` **depuis le projet** : tant qu'ils tournent, aucun
autre service du serveur ne peut servir en HTTP ou en HTTPS. Un seul processus peut détenir un
port hôte.

Pour un serveur mutualisé, le frontal sort du projet et devient une brique d'infrastructure, sur
un réseau Docker partagé — les applications ne publient alors plus aucun port :

```bash
cd deploy/edge && cp .env.example .env && make up   # crée le réseau `edge`, valide, démarre
cd ../.. && docker compose -f docker-compose.yml -f deploy/edge/compose.meds-api.yml up -d
```

Ajouter un service = un fichier dans `deploy/edge/conf.d/` + `make reload` : rechargement à chaud,
sans coupure, certificat du nouveau domaine obtenu automatiquement.
[`deploy/edge/README.md`](deploy/edge/README.md), [08 §6](docs/08-deploiement-docker.md#6-frontal-mutualisé-pour-plusieurs-services),
[ADR 0006](docs/adr/0006-frontal-mutualise-sur-reseau-externe.md).

### Durcissement

`read_only: true`, `cap_drop: ALL`, `no-new-privileges`, exécution en UID 65532, `tmpfs` sur `/tmp`
pour l'écriture temporaire des snapshots. Compose **refuse de démarrer si `MEDS_API_KEYS` est
absente** : le service ne peut pas être exposé sans authentification par inadvertance.

### Volumes

| Volume | Contenu | À sauvegarder |
|---|---|---|
| `meds-data` | Snapshots NDJSON | **Non** — entièrement reconstructible depuis l'ANSM |
| `caddy-data` / `traefik-acme` | Certificats TLS | **Oui** — sinon chaque restauration reconsomme le quota Let's Encrypt |

### Accès réseau du synchroniseur

L'API doit joindre `base-donnees-publique.medicaments.gouv.fr` en HTTPS. Trois montages, par ordre
de préférence :

| Montage | Principe | Quand l'employer |
|---|---|---|
| **A** — sortie autorisée | Le service est sur les deux réseaux, l'entrée reste filtrée par le proxy | Défaut |
| **B** — synchronisation déportée | `MEDS_SYNC_CRON` vide ; un cron hôte lance `bdpm-sync` dans le volume | Aucune sortie tolérée depuis un service exposé |
| **C** — proxy sortant | `HTTPS_PROXY` vers un mandataire filtrant | Réseau d'entreprise à proxy imposé |

Le montage B est la raison d'être de la CLI séparée :

```bash
0 4 * * * docker compose -f /srv/meds-api/docker-compose.yml \
          run --rm api /usr/local/bin/bdpm-sync --data-dir /data
```

L'API détecte le nouveau snapshot et bascule sans redémarrage ni interruption.

### Diagnostic sans shell

`distroless` supprime toute possibilité de `docker exec sh`. Les moyens de diagnostic sont donc
conçus **dans** l'application :

| Besoin | Moyen |
|---|---|
| État du jeu de données | `GET /v1/dataset` — version, âge, compteurs, quarantaine |
| Vivacité / disponibilité | `GET /healthz`, `GET /readyz` |
| Métriques | `GET /metrics` (réseaux privés uniquement) |
| Journaux | `docker compose logs api` (JSON structuré) |
| Inspection du volume | conteneur jetable : `docker run --rm -v meds-data:/d alpine ls -la /d` |

On échange le confort de diagnostic contre une surface d'attaque quasi nulle. Le prix est payé une
fois, à la conception des endpoints d'observabilité.

### Mise à jour

```bash
docker compose build api && docker compose up -d api
```

Le volume de données est conservé : le nouveau conteneur recharge le snapshot existant en moins
d'une seconde, sans rien retélécharger.

## Développement

```bash
make help     # liste toutes les cibles
make build    # compile les deux binaires dans ./bin
make test     # go test -race ./...
make lint     # golangci-lint run
make fmt      # gofmt + go vet ; échoue si du code n'est pas formaté
make bench    # benchmarks avec -benchmem
```

### Qualité

| Contrôle | Cible | Exécuté en CI |
|---|---|---|
| Analyse statique | `make lint` | oui |
| Tests avec détection de races | `make test` | oui |
| Seuils de couverture (80 % global, 85 % sur `internal/bdpm` et `internal/store/search`) | `make cover-check` | oui |
| Vulnérabilités des dépendances | `make vulncheck` | oui |
| Scan du système de fichiers (Trivy) | — | oui |
| Contrat OpenAPI (Spectral) | `make openapi-lint` | oui |
| Fuzzing (6 cibles) | `make fuzz` | non |
| Charge : fumée, nominal, bascule, pic, endurance | `make load-*` | non |

## Architecture

```text
cmd/meds-api/     serveur HTTP + ordonnanceur intégré
cmd/bdpm-sync/    CLI de synchronisation one-shot
internal/bdpm/    téléchargement, encodage, parsing, validation, snapshot
internal/store/   index immuable en mémoire (SoA, CSR, index inversé, trigrammes)
internal/api/     routage, handlers, rendu JSON, erreurs RFC 9457, OpenAPI
internal/config/  chargement et validation stricte de la configuration
```

Le sens des dépendances est strictement `api → store → bdpm`, sans retour.
Détail dans [docs/02-architecture.md](docs/02-architecture.md), décisions structurantes dans
[docs/adr/](docs/adr/).

## État du projet

**Lots 0 à 9 livrés.** Le service est fonctionnel de bout en bout : `bdpm-sync` produit un snapshot
depuis l'ANSM, et `meds-api` le sert avec recherche, pagination, cache, authentification,
limitation de débit, métriques et documentation embarquée.

| Lot | Objet | État |
|---|---|---|
| 0 | Fondations | terminé |
| 1 | Ingestion | terminé |
| 2 | Cœur mémoire | terminé |
| 3 | Recherche | terminé |
| 4 | API HTTP | terminé |
| 5 | Sécurité | terminé |
| 6 | Ordonnancement | terminé |
| 7 | Observabilité | terminé |
| 8 | Conteneurisation | terminé |
| 9 | Qualité | terminé — reste `make load-soak` sur une heure complète |

Les specs de conception sont complètes et approuvées dans [docs/](docs/README.md) ; l'avancement
détaillé est suivi dans [docs/12-backlog-taches.md](docs/12-backlog-taches.md).

## Contribuer

Les contributions sont bienvenues. Le guide complet — environnement de développement, contrôles
exigés, conventions de code et de commit — est dans [CONTRIBUTING.md](CONTRIBUTING.md). En résumé :

1. Ouvrir une issue pour discuter du changement avant d'écrire du code, s'il touche au contrat
   HTTP, aux dépendances, aux structures du `store` ou à la sécurité.
2. Créer une branche depuis `main`.
3. Vérifier localement : `make fmt lint test cover-check`.
4. Ouvrir une pull request — la CI rejoue lint, tests avec détection de races, seuils de
   couverture, `govulncheck`, Trivy, Spectral et la conformité du titre de PR à Conventional
   Commits.

Toute évolution du contrat HTTP doit être reflétée dans [`api/openapi.yaml`](api/openapi.yaml) et
dans [docs/04-specification-api.md](docs/04-specification-api.md) ; une décision structurante
donne lieu à un ADR dans [docs/adr/](docs/adr/).

La participation au projet est régie par le [Code de conduite](CODE_OF_CONDUCT.md).

## Sécurité

Ne signalez pas une vulnérabilité via une issue publique : ouvrez un
[avis de sécurité privé](https://github.com/MyOps-xyz/meds-api/security/advisories/new) sur le
dépôt. Le périmètre, les délais de traitement et les bonnes pratiques d'exploitation sont décrits
dans [SECURITY.md](SECURITY.md) ; le modèle de menace et l'authentification dans
[docs/06-securite.md](docs/06-securite.md).

## Licence et attribution

Le code est sous licence MIT — voir [LICENSE](LICENSE).

Les **données** servies proviennent de la BDPM et sont diffusées sous
**Licence Ouverte / Open Licence (Etalab)**. Toute réutilisation impose la mention de la source
et de la date de dernière mise à jour, exposées par l'endpoint `/v1/dataset`.

> [!WARNING]
> Cette API est un outil d'information. Elle ne se substitue en aucun cas à l'avis d'un
> professionnel de santé, ni au Résumé des Caractéristiques du Produit.

Source : <https://base-donnees-publique.medicaments.gouv.fr/>
