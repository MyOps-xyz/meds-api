# syntax=docker/dockerfile:1.7

# ─── Étape de construction ───────────────────────────────────────────────
# --platform=$BUILDPLATFORM : l'étape tourne toujours sur l'architecture du
# constructeur, jamais sous émulation QEMU. C'est Go qui croise vers la cible
# via GOARCH — dix fois plus rapide qu'un build arm64 émulé, et sans QEMU à
# installer sur le runner.
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build

# TARGETOS/TARGETARCH sont fournis automatiquement par BuildKit, mais dans la
# portée globale : sans ce `ARG`, ils s'évaluent à vide dans l'étape et
# `GOARCH=` retombe silencieusement sur l'architecture du constructeur — les
# deux variantes de l'index multi-arch contiendraient alors le même binaire
# amd64.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Les dépendances changent rarement : couche mise en cache séparément du code.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# CGO_ENABLED=0 produit un binaire statique : ni glibc, ni bibliothèque
# partagée à mettre à jour, ni vulnérabilité système à corriger. C'est ce qui
# rend l'image distroless/static possible.
#
# -trimpath retire les chemins absolus de compilation : build reproductible,
# et aucune fuite de l'arborescence du constructeur dans le binaire.
# -s -w retire la table des symboles, soit environ 30 % de taille en moins.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${BUILD_DATE}" \
      -o /out/meds-api  ./cmd/meds-api && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${BUILD_DATE}" \
      -o /out/bdpm-sync ./cmd/bdpm-sync

# ─── Image finale ────────────────────────────────────────────────────────
# distroless/static ne contient ni shell, ni gestionnaire de paquets, ni
# coreutils. Un attaquant qui obtiendrait l'exécution de code n'y trouve aucun
# outil. Contrepartie assumée : aucun `docker exec sh` de diagnostic — c'est
# pourquoi l'observabilité est conçue *dans* l'application
# (docs/10-observabilite.md).
FROM gcr.io/distroless/static-debian13:nonroot

# Le certificat racine est déjà présent dans distroless/static, indispensable
# au synchroniseur pour joindre l'ANSM en HTTPS.
COPY --from=build /out/meds-api  /usr/local/bin/meds-api
COPY --from=build /out/bdpm-sync /usr/local/bin/bdpm-sync

USER nonroot:nonroot
WORKDIR /data
EXPOSE 8080

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="meds-api" \
      org.opencontainers.image.description="API REST sur la Base de données publique des médicaments (ANSM)" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/MyOps-xyz/meds-api" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# Le binaire se sonde lui-même : distroless n'a ni curl, ni wget, ni shell —
# la forme exec est donc obligatoire, et la sous-commande `healthcheck` de
# cmd/meds-api interroge http://127.0.0.1${MEDS_ADDR}/healthz.
#
# /healthz et non /readyz : la sonde répond « le processus sert du HTTP », pas
# « le jeu de données est chargé ». La première synchronisation depuis l'ANSM
# peut durer plusieurs minutes ; sonder /readyz ferait passer le conteneur
# unhealthy pendant tout ce temps et le ferait tuer par un orchestrateur
# (handlers_ref.go, la distinction est explicite). L'état du dataset se
# surveille par /readyz et /v1/dataset côté supervision.
#
# Valeurs alignées sur docker-compose.yml. Pour un conteneur jetable qui
# détourne l'entrypoint vers bdpm-sync, désactiver la sonde :
# `docker run --no-healthcheck` ou `healthcheck: { disable: true }` en compose.
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
  CMD ["/usr/local/bin/meds-api", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/meds-api"]
