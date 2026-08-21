# Contribuer à meds-api

Merci de l'intérêt porté au projet. Ce document décrit le circuit de contribution, les contrôles
que doit passer un changement, et les conventions du dépôt.

Toute participation est régie par le [Code de conduite](CODE_OF_CONDUCT.md).

## Sommaire

- [Avant d'écrire du code](#avant-décrire-du-code)
- [Environnement de développement](#environnement-de-développement)
- [Circuit de contribution](#circuit-de-contribution)
- [Contrôles exigés](#contrôles-exigés)
- [Conventions](#conventions)
- [Ce que touche un changement](#ce-que-touche-un-changement)
- [Publication des versions](#publication-des-versions)
- [Signaler un bogue](#signaler-un-bogue)
- [Proposer une évolution](#proposer-une-évolution)

## Avant d'écrire du code

Le projet suit une conception écrite : les spécifications vivent dans [`docs/`](docs/README.md) et
les décisions structurantes dans [`docs/adr/`](docs/adr/). Un changement qui contredit une
spécification n'est pas un bogue à corriger dans le code seul — il demande d'abord de trancher la
spécification.

Ouvrez donc une **issue de discussion avant d'écrire le code** si le changement :

- modifie le contrat HTTP (routes, paramètres, schémas de réponse, codes d'erreur) ;
- ajoute une dépendance externe — le module n'en a que trois directes, et c'est délibéré ;
- change une structure de données du `store` ou le pipeline d'ingestion ;
- touche à l'authentification, à la limitation de débit ou à la validation de configuration.

Pour une correction évidente — faute de frappe, message d'erreur, cas limite manifestement mal
géré, test manquant — une pull request directe est la bienvenue, sans issue préalable.

## Environnement de développement

Prérequis : **Go 1.26.6** (version exacte de [`go.mod`](go.mod)), `make`, et Docker 27+ pour les
cibles de conteneurisation et les campagnes de charge.

```bash
git clone https://github.com/MyOps-xyz/meds-api.git
cd meds-api
make tools          # installe golangci-lint et govulncheck (versions épinglées) dans ./tools/bin
make build          # compile meds-api et bdpm-sync dans ./bin
make help           # liste toutes les cibles
```

Pour lancer le service localement, sans attendre une synchronisation ANSM à chaque redémarrage :

```bash
cp .env.example .env                 # renseigner MEDS_API_KEYS
make sync                            # une synchronisation one-shot dans MEDS_DATA_DIR
make run                             # démarre le serveur (charge .env s'il est présent)
```

Les versions d'outillage sont **épinglées à deux endroits qui doivent rester cohérents** : les
variables `GOLANGCI_LINT_VERSION` et `GOVULNCHECK_VERSION` du [`Makefile`](Makefile) et celles de
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). Une montée de version modifie les deux.

## Circuit de contribution

1. **Forkez** le dépôt et créez une branche depuis `main`.
2. **Écrivez le test avant le correctif** pour un bogue : il doit échouer sans le correctif.
3. **Vérifiez localement** — voir [Contrôles exigés](#contrôles-exigés).
4. **Ouvrez la pull request** contre `main`, en remplissant le gabarit proposé.
5. **La CI rejoue tout** : lint, tests avec détection de races, seuils de couverture,
   `govulncheck`, Trivy, Spectral, et la conformité du titre de la PR à Conventional Commits.
   Aucune compilation croisée ni construction d'image n'a lieu sur une PR — cela n'arrive qu'à
   la release. Une pull request dont la CI est rouge n'est pas relue.

Gardez une pull request focalisée sur un seul sujet. Un renommage massif noyé dans un correctif
fonctionnel rend la relecture impossible : séparez-les en deux pull requests.

## Contrôles exigés

Avant de pousser :

```bash
make fmt          # gofmt + go vet ; échoue si du code n'est pas formaté
make lint         # golangci-lint run
make test         # go test -race ./...
make cover-check  # seuils de couverture
```

Les seuils de couverture sont ceux appliqués par la CI, et ils sont bloquants :

| Périmètre | Seuil |
|---|---|
| Module entier | 80 % |
| `internal/bdpm` | 85 % |
| `internal/store` | 80 % |
| `internal/api` | 75 % |

Un package **nommé dans ces seuils, présent dans le profil, mais à 0 %** est un échec et jamais un
cas ignoré : c'est précisément la situation que le contrôle cherche à détecter
([`check-coverage.sh`](.github/scripts/check-coverage.sh)).

Contrôles complémentaires, non exécutés par la CI mais attendus lorsque le changement les concerne :

| Cible | Quand la lancer |
|---|---|
| `make openapi-lint` | toute modification de `api/openapi.yaml` |
| `make alerts-lint` | toute modification des règles d'alerte Prometheus |
| `make fuzz` | modification d'un parseur, d'un décodeur ou d'un validateur d'entrée |
| `make bench` / `make bench-budgets` | modification d'un chemin chaud du `store` ou de la recherche |
| `make load-mixed`, `make load-reload`, `make load-spike` | modification du chemin de lecture HTTP ou de la bascule de snapshot |

## Conventions

### Code

- Le code, les commentaires, la documentation et les messages d'erreur sont **en français**, à
  l'exception des identifiants Go, qui suivent les conventions du langage.
- Le sens des dépendances est strictement `api → store → bdpm`, **sans retour**. Une pull request
  qui introduit un cycle ou une dépendance remontante sera refusée sur ce seul motif.
- Le chemin de lecture ne prend **aucun verrou** et n'alloue pas plus que nécessaire : l'index est
  immuable et remplacé atomiquement. Toute introduction d'un `sync.Mutex` sur ce chemin demande
  une justification explicite.
- Les erreurs sont enveloppées avec `%w` et portent un contexte exploitable par un exploitant, pas
  seulement par un développeur.
- Aucun secret — clé API, clé d'administration — ne doit pouvoir atteindre les journaux. Toute
  structure qui en contient expose un `LogValue`.

### Messages de commit

Le dépôt suit [Conventional Commits](https://www.conventionalcommits.org/fr/v1.0.0/), déjà employé
par Dependabot (`chore(deps)`, `chore(ci)`) :

```
type(portée): résumé à l'impératif, sans point final

Corps facultatif expliquant le pourquoi plutôt que le comment.

Refs: #123
```

Types employés : `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`.
Portées usuelles : `api`, `store`, `bdpm`, `config`, `deploy`, `docs`.

Ces messages ne sont pas décoratifs : ils déterminent le numéro de version publié
(voir [Publication des versions](#publication-des-versions)).

| Message | Effet sur la version |
| --- | --- |
| `feat: …` | mineure — `1.2.3` → `1.3.0` |
| `fix: …`, `perf: …`, `revert: …` | correctif — `1.2.3` → `1.2.4` |
| `feat!: …`, ou un pied de page `BREAKING CHANGE:` | majeure — `1.2.3` → `2.0.0` |
| `docs:`, `test:`, `ci:`, `chore:`, `refactor:`, `build:`, `style:` | aucune release |

Le dépôt fusionne en **squash** : c'est le **titre de la pull request** qui devient le message
de commit sur `main`, et donc lui que la CI vérifie (job `convention de commit`) et que
semantic-release analyse. Un titre hors convention fait échouer la CI.

### Tests

- Tests de table (`map[string]struct{...}` + `t.Run`) pour les cas multiples.
- `t.Parallel()` partout où c'est licite ; la CI tourne avec `-race`.
- Aucun accès réseau dans un test unitaire : les échanges avec l'ANSM se simulent avec
  `httptest.Server` et les fixtures de [`testdata/`](testdata/).
- Un test de non-régression cite l'issue ou le symptôme qu'il verrouille.

## Ce que touche un changement

Certains changements ne sont complets que s'ils sont répercutés ailleurs :

| Si vous modifiez… | …mettez aussi à jour |
|---|---|
| le contrat HTTP | [`api/openapi.yaml`](api/openapi.yaml) et [`docs/04-specification-api.md`](docs/04-specification-api.md) |
| une variable d'environnement | [`.env.example`](.env.example), le tableau de configuration du [README](README.md) et les règles de validation |
| une décision structurante | un nouvel ADR dans [`docs/adr/`](docs/adr/) — on n'amende pas un ADR accepté, on en écrit un qui le remplace |
| une métrique ou un journal | [`docs/10-observabilite.md`](docs/10-observabilite.md) et les règles d'alerte |
| le déploiement | [`docs/08-deploiement-docker.md`](docs/08-deploiement-docker.md) et [`deploy/edge/README.md`](deploy/edge/README.md) |

## Publication des versions

Il n'y a **rien à faire manuellement** : ni tag à poser, ni numéro de version à choisir.

À chaque merge sur `main`, [`.github/workflows/release.yml`](.github/workflows/release.yml)
rejoue les tests et `govulncheck`, puis lance
[semantic-release](https://semantic-release.gitbook.io/). Celui-ci lit les commits accumulés
depuis le dernier tag et, s'il en trouve au moins un publiable :

1. calcule la version SemVer suivante d'après le tableau ci-dessus ;
2. compile `meds-api` et `bdpm-sync` pour `linux/amd64` et `linux/arm64`, version injectée par
   `ldflags` — le binaire répond donc juste au numéro publié via `meds-api --version` ;
3. crée le tag `vX.Y.Z` ;
4. publie la release GitHub : notes générées à partir des commits, les deux archives `.tar.gz`
   et leurs sommes de contrôle SHA-256 ;
5. construit et pousse l'image Docker multi-arch sur
   `ghcr.io/myops-xyz/meds-api`, étiquetée `X.Y.Z`, `X.Y`, `X` et `latest`, avec provenance
   SLSA et SBOM.

Si aucun commit publiable n'est trouvé (que des `chore:`, `ci:`, `test:`…), le workflow se
termine en succès sans rien publier — ni tag, ni release, ni image. C'est le comportement
attendu.

Aucune image n'est jamais construite depuis une pull request : `release.yml` n'est déclenché que
par un push sur `main`. La CI des PR se limite au lint, aux tests, à `govulncheck`, à Trivy et à
Spectral ; la compilation croisée `linux/arm64` n'est donc vérifiée qu'au moment de la release.

Le pipeline **n'écrit jamais dans le dépôt** : aucun commit de release n'est poussé sur `main`.
`main` peut donc être protégé par un ruleset strict sans avoir à en exempter qui que ce soit.
Deux conséquences à connaître :

- **Le journal des modifications vit sur la page
  [Releases](https://github.com/MyOps-xyz/meds-api/releases)**, pas dans un `CHANGELOG.md`
  versionné.
- **`info.version` dans [`api/openapi.yaml`](api/openapi.yaml) décrit la version du *contrat***
  — celui servi sous `/v1` et par `/openapi.json` — et non celle du binaire. Il ne suit pas les
  releases : il se modifie à la main, dans une PR, quand le contrat évolue. La version du
  binaire s'obtient par `meds-api --version`.

Pour prévisualiser ce que produirait une release sans rien publier : onglet **Actions** →
workflow **Release** → **Run workflow**, en cochant *Simulation*.

La configuration tient en deux fichiers : [`.releaserc.json`](.releaserc.json) pour les règles,
[`.github/scripts/prepare-release.sh`](.github/scripts/prepare-release.sh) pour la fabrication
des artefacts.

## Signaler un bogue

Ouvrez une [issue de bogue](https://github.com/MyOps-xyz/meds-api/issues/new/choose) avec la
version (`meds-api --version`), la requête exacte, la réponse obtenue et celle attendue.

**N'ouvrez jamais d'issue publique pour une vulnérabilité** : suivez la procédure de
[SECURITY.md](SECURITY.md).

Une donnée erronée servie par l'API n'est pas nécessairement un bogue : si le fichier source de
l'ANSM porte déjà l'erreur, l'API la restitue fidèlement, par conception. `GET /v1/dataset` indique
la version du jeu de données servi et permet de trancher.

## Proposer une évolution

Ouvrez une [issue d'évolution](https://github.com/MyOps-xyz/meds-api/issues/new/choose) décrivant
le besoin avant la solution. Le périmètre du projet est délibérément fermé et documenté dans
[`docs/00-vision-et-perimetre.md`](docs/00-vision-et-perimetre.md) : une proposition hors périmètre
peut être refusée sans que sa qualité soit en cause.
