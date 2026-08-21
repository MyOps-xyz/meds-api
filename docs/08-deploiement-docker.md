# 08 — Déploiement Docker

## 1. Objectif

`docker compose --profile caddy up -d` sur une machine vierge doit produire une API fonctionnelle
en HTTPS, avec un certificat valide, sans étape manuelle de génération de certificat.

Versions retenues (dernières stables au 14/08/2026) : **Go 1.26.6**, **Caddy 2.11.4**,
**Traefik 3.7.10**, Docker Engine ≥ 25.

**Vérifié le 15/08/2026**, les deux profils bout en bout sur Docker 29.4 : image de **20,1 Mo**
(budget 25 Mio), healthcheck vert sans `curl` ni `wget`, première synchronisation depuis l'ANSM
aboutie dans le conteneur, HTTPS servi par l'AC interne de Caddy sans aucune étape manuelle,
`/metrics` refusé par le frontal (`403`) mais joignable en direct sur le réseau interne, `401` sans
clé, redirection HTTP → HTTPS, et **en-têtes de sécurité identiques sous les deux profils** — la
parité promise par l'[ADR 0005](adr/0005-caddy-et-traefik-en-profils.md).

---

## 2. Image applicative

### 2.1 Dockerfile

```dockerfile
# syntax=docker/dockerfile:1.7

# ─── Étape de construction ───────────────────────────────────────────────
FROM golang:1.26.6-alpine AS build

WORKDIR /src

# Les dépendances changent rarement : couche mise en cache séparément du code
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildDate=${BUILD_DATE}" \
      -o /out/meds-api  ./cmd/meds-api && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/bdpm-sync ./cmd/bdpm-sync

# ─── Image finale ────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/meds-api  /usr/local/bin/meds-api
COPY --from=build /out/bdpm-sync /usr/local/bin/bdpm-sync

USER nonroot:nonroot
WORKDIR /data
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/meds-api"]
```

**Pourquoi ces choix :**

- `CGO_ENABLED=0` produit un binaire statique : ni glibc, ni `.so` à mettre à jour, ni
  vulnérabilité système à corriger. C'est ce qui rend `distroless/static` possible.
- `distroless/static` ne contient **ni shell, ni gestionnaire de paquets, ni coreutils**. Un
  attaquant qui obtiendrait l'exécution de code n'y trouve aucun outil. Contrepartie assumée :
  aucun `docker exec` de diagnostic (voir §8).
- `-trimpath` retire les chemins absolus de compilation — build reproductible et pas de fuite
  d'arborescence.
- `-s -w` retire la table des symboles : ~30 % de taille en moins.
- Les caches `go mod` et `go build` montés en `--mount=type=cache` accélèrent fortement les
  reconstructions sans grossir l'image.
- Le certificat racine est déjà présent dans `distroless/static`, indispensable au synchroniseur
  pour joindre l'ANSM en HTTPS.

Taille attendue : **~20 Mio** (binaire ~15 Mio + base distroless ~2 Mio).

### 2.2 `.dockerignore`

```
.git/  docs/  testdata/  data/  deploy/  *.md  .github/  tests/
```

Réduit le contexte de build et évite d'expédier un `data/` local de plusieurs dizaines de mégaoctets.

---

## 3. Docker Compose

Un seul fichier, deux profils. Le service `meds-api` est commun ; seul le frontal change.

```yaml
name: meds-api

x-hardening: &hardening
  read_only: true
  cap_drop: [ALL]
  security_opt:
    - no-new-privileges:true
  restart: unless-stopped

services:
  api:
    build:
      context: .
      args:
        VERSION: ${VERSION:-dev}
        COMMIT:  ${COMMIT:-unknown}
    image: meds-api:${VERSION:-dev}
    <<: *hardening
    environment:
      MEDS_ADDR:          ":8080"
      MEDS_DATA_DIR:      "/data"
      MEDS_API_KEYS:      ${MEDS_API_KEYS:?MEDS_API_KEYS est obligatoire}
      MEDS_ADMIN_KEY:     ${MEDS_ADMIN_KEY:-}
      MEDS_SYNC_CRON:     ${MEDS_SYNC_CRON:-0 4 * * *}
      MEDS_SYNC_JITTER:   ${MEDS_SYNC_JITTER:-30m}
      MEDS_RATE_LIMIT:    ${MEDS_RATE_LIMIT:-100}
      MEDS_CORS_ORIGINS:  ${MEDS_CORS_ORIGINS:-}
      MEDS_LOG_LEVEL:     ${MEDS_LOG_LEVEL:-info}
      GOMEMLIMIT:         "400MiB"
      GOGC:               "200"
    volumes:
      - meds-data:/data
      - type: tmpfs
        target: /tmp
        tmpfs: { size: 67108864 }     # 64 Mio, pour les écritures temporaires du snapshot
    healthcheck:
      test: ["CMD", "/usr/local/bin/meds-api", "healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3
      start_period: 40s
    deploy:
      resources:
        limits:   { cpus: "2.0", memory: 512M }
        reservations: { cpus: "0.5", memory: 128M }
    networks: [internal]

  caddy:
    profiles: [caddy]
    image: caddy:2.11.4-alpine
    restart: unless-stopped
    ports: ["80:80", "443:443", "443:443/udp"]     # UDP pour HTTP/3
    environment:
      DOMAIN: ${DOMAIN:-localhost}
      ACME_EMAIL: ${ACME_EMAIL:-}
    volumes:
      - ./deploy/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data          # certificats — DOIT être persisté
      - caddy-config:/config
    depends_on:
      api: { condition: service_healthy }
    networks: [internal, public]

  traefik:
    profiles: [traefik]
    image: traefik:v3.7.10
    restart: unless-stopped
    ports: ["80:80", "443:443", "443:443/udp"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./deploy/traefik/traefik.yml:/etc/traefik/traefik.yml:ro
      - ./deploy/traefik/dynamic:/etc/traefik/dynamic:ro
      - traefik-acme:/acme
    depends_on:
      api: { condition: service_healthy }
    networks: [internal, public]

volumes:
  meds-data: {}
  caddy-data: {}
  caddy-config: {}
  traefik-acme: {}

networks:
  public:   {}
  internal: { internal: true }
```

**Points structurants :**

- `MEDS_API_KEYS:?…` — Compose **refuse de démarrer** si la variable est absente. Le service ne
  peut pas être exposé sans authentification par inadvertance ([06](06-securite.md#33-configuration-et-cycle-de-vie)).
- Réseau `internal: true` — le conteneur `api` n'a **aucune route sortante** vers Internet…
  sauf qu'il en a besoin pour joindre l'ANSM. Il est donc aussi attaché au réseau `public` en
  production, ou bien la synchronisation est déportée sur `bdpm-sync` lancé ponctuellement. Ce
  compromis est explicité en §7.
- `read_only: true` impose un `tmpfs` sur `/tmp` : le snapshot s'écrit en temporaire avant
  `os.Rename` ([09](09-pipeline-mise-a-jour.md)).
- Le `healthcheck` invoque le binaire lui-même en sous-commande, puisque `distroless` n'a ni
  `curl`, ni `wget`. Il est **déclaré dans le `Dockerfile`** — mêmes valeurs qu'ici — de sorte
  qu'une image lancée hors de ce `compose.yml` reste sondée ; le bloc ci-dessus ne fait que le
  reprendre explicitement. La sonde vise `/healthz` et non `/readyz` : elle atteste que le
  processus sert du HTTP, pas que le jeu de données est chargé. Sonder `/readyz` rendrait le
  conteneur *unhealthy* pendant toute la première synchronisation.
- Les volumes de certificats **doivent être persistés** : sans cela, chaque redémarrage
  redemande un certificat et atteint vite les quotas de Let's Encrypt.

---

## 4. Profil Caddy

`deploy/caddy/Caddyfile` :

```caddyfile
{
    email "{$ACME_EMAIL}"   # quoté : une variable vide ne doit pas produire zéro argument
    servers {
        protocols h1 h2 h3
    }
}

{$DOMAIN} {
    encode zstd gzip

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "strict-origin-when-cross-origin"
        -Server
    }

    # Endpoints d'exploitation : jamais servis par le frontal
    @ops path /metrics /debug/*
    respond @ops 403

    reverse_proxy api:8080 {
        health_uri /healthz
        health_interval 10s
    }
}
```

**Ce profil détient 80 et 443 pour lui seul.** Il convient au développement local et à un serveur
mono-service. Pour héberger d'autres services sur la même machine, ne pas l'activer et employer le
frontal mutualisé de §6.

**Pourquoi `/metrics` est refusé inconditionnellement.** La rédaction initiale filtrait par
`not remote_ip private_ranges`. Elle a été écartée après mesure : l'adresse source vue par Caddy
dépend du mode de publication des ports. Sous Docker Desktop, OrbStack, un `userland-proxy` ou un
CDN en amont, elle est celle d'une passerelle privée — la règle laisse alors **fuiter `/metrics`
vers Internet en croyant le refuser**. Vérifié le 15/08/2026 : `curl https://…/metrics` depuis
l'hôte renvoyait `200`, pas `403`. Prometheus n'a de toute façon pas à passer par le frontal, il
scrute `api:8080/metrics` directement sur le réseau `internal` ([10](10-observabilite.md)). Un
refus inconditionnel ne dépend d'aucune hypothèse sur la topologie réseau ; une supervision
distante s'obtient par un allowlist explicite de l'adresse du collecteur, jamais par
`private_ranges`.

**Le certificat TLS.** C'est ici que Caddy justifie sa sélection : il gère les deux cas sans
configuration supplémentaire.

- `DOMAIN` est un nom public → Caddy obtient un certificat Let's Encrypt automatiquement
  (HTTP-01 ou TLS-ALPN-01), et le renouvelle.
- `DOMAIN=localhost` ou un nom local → Caddy **génère sa propre autorité de certification interne
  et signe un certificat**, sans openssl, sans script, sans CSR. C'est exactement le besoin de
  certificats auto-générés pour le développement et les environnements internes.

Pour faire reconnaître l'AC locale par le poste de développement :

```bash
docker compose --profile caddy exec caddy \
  cat /data/caddy/pki/authorities/local/root.crt > caddy-root.crt
# macOS
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain caddy-root.crt
```

---

## 5. Profil Traefik

`deploy/traefik/traefik.yml` (configuration statique) :

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint: { to: websecure, scheme: https, permanent: true }
  websecure:
    address: ":443"
    http3: {}

providers:
  docker:
    exposedByDefault: false      # rien n'est exposé sans label explicite
  file:
    directory: /etc/traefik/dynamic
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      email: "{{ env \"ACME_EMAIL\" }}"
      storage: /acme/acme.json
      tlsChallenge: {}

log:      { level: INFO, format: json }
accessLog: { format: json }
```

`deploy/traefik/dynamic/middlewares.yml` :

```yaml
http:
  middlewares:
    security-headers:
      headers:
        stsSeconds: 31536000
        stsIncludeSubdomains: true
        contentTypeNosniff: true
        referrerPolicy: strict-origin-when-cross-origin
        customResponseHeaders: { Server: "" }
    # Refus inconditionnel : aucune adresse ne correspond à cette plage.
    # Même raisonnement que pour Caddy (§4) — le filtrage par plages privées est
    # trompeur dès que le runtime masque l'adresse source.
    block-ops:
      ipAllowList:
        sourceRange: ["255.255.255.255/32"]
    compress:
      compress: {}
```

Labels à ajouter au service `api` pour ce profil :

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.meds.rule=Host(`${DOMAIN}`)"
  - "traefik.http.routers.meds.entrypoints=websecure"
  - "traefik.http.routers.meds.tls.certresolver=letsencrypt"
  - "traefik.http.routers.meds.middlewares=security-headers@file,compress@file"
  - "traefik.http.services.meds.loadbalancer.server.port=8080"
  # /metrics et /debug/* refusés depuis le frontal
  - "traefik.http.routers.meds-ops.rule=Host(`${DOMAIN}`) && (PathPrefix(`/metrics`) || PathPrefix(`/debug`))"
  - "traefik.http.routers.meds-ops.middlewares=block-ops@file"
  - "traefik.http.routers.meds-ops.priority=100"
```

**Le provider Docker voit toute la machine.** Mesuré le 15/08/2026 : lancé sur un poste hébergeant
d'autres projets, ce Traefik captait leurs routeurs (`core-api@docker`) et journalisait une erreur
par cycle pour chacun. `exposedByDefault: false` empêche d'exposer un conteneur *non étiqueté*,
mais un conteneur *étiqueté* d'un autre projet serait bel et bien publié par ce frontal. D'où la
contrainte de périmètre :

```yaml
providers:
  docker:
    exposedByDefault: false
    constraints: "Label(`com.docker.compose.project`,`meds-api`)"
```

C'est un écart de fond avec Caddy, dont la configuration est explicite et ne découvre rien — une
raison de plus de le préférer ([ADR 0005](adr/0005-caddy-et-traefik-en-profils.md)), et l'argument
qui montre que monter le socket en lecture seule ne suffit pas à lui seul.

**Certificats auto-signés en local avec Traefik.** Contrairement à Caddy, Traefik n'a pas d'AC
interne : sans résolveur ACME joignable, il sert un certificat auto-signé générique que le
navigateur rejette. Pour un développement local propre, il faut fournir un certificat, par exemple
avec `mkcert` :

```bash
mkcert -install && mkcert -cert-file deploy/traefik/certs/local.pem \
                          -key-file  deploy/traefik/certs/local-key.pem "meds.localhost"
```

déclaré dans `dynamic/tls.yml`. **C'est la principale raison pour laquelle Caddy est le profil
recommandé par défaut** ([ADR 0005](adr/0005-caddy-et-traefik-en-profils.md)).

---

## 6. Frontal mutualisé pour plusieurs services

Les profils `caddy` et `traefik` des §4 et §5 publient `80:80` et `443:443` **depuis le projet**.
Un seul processus peut détenir un port hôte : tant qu'ils sont actifs, aucun autre service de la
machine ne peut servir en HTTP ou en HTTPS. C'est acceptable en développement et sur un serveur
dédié à meds-api ; ça ne l'est pas sur un serveur mutualisé.

La réponse n'est pas de renoncer à Caddy — c'est exactement l'outil pour cet usage — mais de le
**sortir du projet applicatif** : le frontal devient une brique d'infrastructure de la machine,
avec son propre cycle de vie, et les applications s'y branchent par un réseau Docker partagé.

```
                        :80  :443
                          │
                   ┌──────┴───────┐
                   │  edge-caddy  │   TLS, HTTP/3, en-têtes, compression
                   └──────┬───────┘
              réseau Docker `edge` (externe)
              ┌───────────┼───────────┐
         ┌────┴─────┐ ┌───┴────┐ ┌────┴─────┐
         │ meds-api │ │ blog   │ │ …        │   aucun port hôte publié
         └──────────┘ └────────┘ └──────────┘
```

### 6.1 Arborescence

| Fichier | Rôle |
|---|---|
| `deploy/edge/docker-compose.yml` | La stack frontale, seule à publier 80/443 |
| `deploy/edge/Caddyfile` | Global ACME + fragments réutilisables, puis `import conf.d/*.caddy` |
| `deploy/edge/conf.d/meds-api.caddy` | Le bloc de site de meds-api |
| `deploy/edge/conf.d/*.caddy` | **Un fichier par service supplémentaire** |
| `deploy/edge/compose.meds-api.yml` | Superposition rattachant l'API au réseau `edge` |
| `deploy/edge/Makefile` | `up`, `reload`, `validate`, `routes`, `ca`, `backup` |

Le `Caddyfile` ne déclare aucun nom d'hôte : il n'expose que deux fragments, `hardened` (compression
et en-têtes de sécurité) et `ops-privees` (refus de `/metrics` et `/debug/*`), que chaque bloc de
`conf.d/` importe. La configuration commune vit à un seul endroit ; ajouter un service ne la
touche pas.

### 6.2 Mise en service

```bash
cd deploy/edge && cp .env.example .env && make up    # crée le réseau `edge`, valide, démarre
```

Puis, depuis la racine du dépôt, en **n'activant aucun profil frontal** :

```bash
docker compose -f docker-compose.yml -f deploy/edge/compose.meds-api.yml up -d
```

La superposition rattache `api` au réseau `edge` et lui fixe le `container_name` `meds-api` — c'est
ce nom que Caddy résout. Le réseau `edge` fournit du même coup la sortie Internet dont le
synchroniseur a besoin pour joindre l'ANSM : c'est le montage A du §7.

### 6.3 Ajouter un service

Déposer un fichier dans `conf.d/`, rattacher le conteneur au réseau `edge` sans publier de port,
puis `make reload`. Le rechargement est **à chaud** : les connexions en cours ne sont pas fermées,
et le certificat du nouveau domaine est obtenu automatiquement au premier appel — à condition que
son enregistrement DNS pointe déjà vers ce serveur.

```caddyfile
blog.example.org {
    import hardened
    reverse_proxy blog:3000
}
```

### 6.4 Ce qui est fermé par défaut

- **Domaine absent de `conf.d/`** : aucun certificat n'est demandé pour lui et la poignée de main
  TLS échoue. Fermé, pas « ouvert avec une page d'erreur ».
- **`/metrics` et `/debug/*`** : `403` inconditionnel, pour la raison exposée au §4.
- **Aucune application ne publie de port hôte** : le frontal est leur unique voie d'accès, et une
  règle de pare-feu suffit à décrire toute la surface exposée de la machine.

### 6.5 Vérification

Configuration éprouvée le 15/08/2026 sur `caddy:2.11.4-alpine`, deux services simultanés :

| Vérification | Résultat |
|---|---|
| Routage HTTPS vers l'amont | `200`, corps du backend servi |
| En-têtes HSTS, nosniff, Referrer-Policy | présents ; `Server` retiré |
| HTTP/3 annoncé | `alt-svc: h3=":443"` |
| Redirection HTTP → HTTPS | `308` |
| `/metrics`, `/debug/pprof/*` | `403` |
| Ajout d'un 2ᵉ service puis `caddy reload` | les deux servis, aucune coupure sur le 1ᵉʳ |
| Certificats | un par domaine, SAN distincts, AC interne |
| Domaine inconnu | poignée de main TLS refusée |
| Conteneur `read_only` + `cap_drop: ALL` | démarre et passe `healthy` |

### 6.6 Ce que ce montage coûte

- **Une stack de plus à exploiter**, avec son propre `up`/`down` et sa propre sauvegarde de
  certificats. En contrepartie, redéployer meds-api ne coupe plus les autres services.
- **Une panne du frontal coupe tout.** Le rayon d'action s'élargit ; `make validate` avant
  `make reload` est ce qui l'évite en pratique, puisqu'une configuration invalide ne peut pas être
  appliquée.
- **Le port 80 doit rester libre et joignable** pour le challenge ACME HTTP-01. Un service qui le
  monopolise casse le renouvellement de **tous** les domaines, pas seulement du sien.
- **Un `docker network create edge` préalable**, et un `container_name` stable par service : deux
  prérequis à ne pas oublier, faute de quoi Caddy répond `502`.

Décision tracée en [ADR 0006](adr/0006-frontal-mutualise-sur-reseau-externe.md).

---

## 7. Accès réseau du synchroniseur

Le service `api` doit joindre `base-donnees-publique.medicaments.gouv.fr` en HTTPS. Trois montages,
par ordre de préférence :

| Montage | Description | Quand l'employer |
|---|---|---|
| **A. Sortie autorisée** | `api` sur `public` + `internal`, entrée filtrée par le proxy | Défaut. Simple, la sortie reste limitée à un domaine |
| **B. Synchronisation déportée** | `MEDS_SYNC_CRON` vide ; `bdpm-sync` lancé par un cron hôte écrit dans le volume | Environnements où aucune sortie n'est tolérée depuis un service exposé |
| **C. Proxy sortant** | `HTTPS_PROXY` vers un mandataire filtrant | Réseaux d'entreprise avec proxy imposé |

Le montage B illustre l'intérêt d'avoir gardé une CLI séparée :

```bash
0 4 * * * docker compose -f /srv/meds-api/docker-compose.yml \
          run --rm api /usr/local/bin/bdpm-sync --data-dir /data
```

L'API détecte le nouveau snapshot par surveillance du répertoire et bascule sans redémarrage.

---

## 8. Exploitation

### Démarrage

```bash
cp .env.example .env
openssl rand -base64 32 | tr -d '=+/' | sed 's/^/mk_live_/'   # générer une clé
docker compose --profile caddy up -d
docker compose logs -f api
curl -H "Authorization: Bearer $KEY" https://localhost/v1/dataset
```

Au premier démarrage, aucun snapshot n'existe : l'API synchronise (~30 s), `/readyz` reste en
`503` pendant ce temps, puis passe à `200`.

### Diagnostic sans shell

`distroless` supprime toute possibilité de `docker exec sh`. Les moyens de diagnostic sont donc
conçus **dans** l'application :

| Besoin | Moyen |
|---|---|
| État général | `GET /v1/dataset` — version, âge, compteurs, quarantaine |
| Vivacité / disponibilité | `GET /healthz`, `GET /readyz` |
| Métriques | `GET /metrics` (réseau interne) |
| Logs | `docker compose logs api` (JSON structuré) |
| Profilage | `MEDS_PPROF=true` puis `go tool pprof` |
| Inspection du volume | conteneur jetable : `docker run --rm -v meds-data:/d alpine ls -la /d` |

C'est un compromis explicite : **on échange le confort de diagnostic contre une surface d'attaque
quasi nulle**. Le prix est payé une fois, à la conception des endpoints d'observabilité.

### Mise à jour applicative

```bash
docker compose build api && docker compose up -d api
```

Le volume de données est conservé : le nouveau conteneur recharge le snapshot existant en moins
d'une seconde, sans retélécharger.

### Sauvegarde

Le volume `meds-data` est **entièrement reconstructible** depuis l'ANSM : il n'a pas à être
sauvegardé. Seul le volume de certificats (`caddy-data` / `traefik-acme`) mérite une sauvegarde,
pour éviter de reconsommer le quota Let's Encrypt lors d'une restauration.

---

## 9. Intégration continue

Étapes bloquantes (T-03) :

| Étape | Commande |
|---|---|
| Analyse statique | `golangci-lint run` (gosec, errcheck, revive, staticcheck) |
| Tests | `go test -race -coverprofile=cover.out ./...` |
| Vulnérabilités du code | `govulncheck ./...` |
| Construction multi-arch | `docker buildx build --platform linux/amd64,linux/arm64` |
| Analyse d'image | `trivy image --severity HIGH,CRITICAL --exit-code 1` |
| SBOM | `syft . -o cyclonedx-json` |
| Validation du contrat | `spectral lint api/openapi.yaml` |
| Fumée | démarrage du compose, appel de `/readyz` et de trois endpoints |

Le `-race` est indispensable : il est le seul moyen fiable de vérifier que la lecture sans verrou
du `Store` est correcte ([02](02-architecture.md#5-concurrence)).

Images épinglées **par digest** en production, jamais par étiquette mutable :

```yaml
image: caddy:2.11.4-alpine@sha256:…
```

---

## 10. Journal de bord des versions

| Composant | Version | Vérifiée le |
|---|---|---|
| Go | 1.26.6 | 14/08/2026 |
| Caddy | 2.11.4 | 14/08/2026 |
| Traefik | 3.7.10 | 14/08/2026 |
| distroless/static-debian12 | `nonroot` | 14/08/2026 |

À revérifier à chaque montée de version ; `golang:1.26.6-alpine` et l'image distroless doivent
être remises à jour au moins trimestriellement même sans changement fonctionnel.
