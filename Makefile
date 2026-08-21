SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help
.SUFFIXES:

# ---------------------------------------------------------------------------
# Versions outillage (épinglées — voir T-02)
# ---------------------------------------------------------------------------
GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION   ?= v1.7.0
# Doit rester identique à SPECTRAL_VERSION dans .github/workflows/ci.yml.
SPECTRAL_VERSION      ?= 6.16.3

# ---------------------------------------------------------------------------
# Variables surchargeables
# ---------------------------------------------------------------------------
MODULE      := github.com/MyOps-xyz/meds-api
BIN_DIR     ?= bin
TOOLS_DIR   ?= tools/bin
DATA_DIR    ?= ./data
COVER_OUT   ?= coverage.out
COVER_HTML  ?= coverage.html
GOLANGCI_LINT ?= $(TOOLS_DIR)/golangci-lint
GOVULNCHECK   ?= $(TOOLS_DIR)/govulncheck
FUZZ_TIME     ?= 1m
DOCKER        ?= docker
IMAGE_NAME    ?= meds-api
IMAGE_TAG     ?= dev

CMDS := ./cmd/meds-api ./cmd/bdpm-sync

# ---------------------------------------------------------------------------
# Métadonnées de version — build reproductible (T-46).
#
# Priorité : SOURCE_DATE_EPOCH (norme reproducible-builds) > date du commit
# HEAD. Jamais `date` courant : deux builds du même commit à des instants
# différents doivent produire des binaires identiques.
# ---------------------------------------------------------------------------
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

ifdef SOURCE_DATE_EPOCH
BUILD_DATE  := $(shell date -u -d "@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
else
BUILD_DATE  := $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)
endif

LDFLAGS := -s -w \
	-X main.version=$(GIT_VERSION) \
	-X main.commit=$(GIT_COMMIT) \
	-X main.date=$(BUILD_DATE)

.PHONY: help
help: ## Affiche cette aide
	@echo "Cibles disponibles :"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_.-]+:.*?## / { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST) | sort

.PHONY: build
build: ## Compile meds-api et bdpm-sync dans ./bin (version injectée, build reproductible)
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ $(CMDS)

.PHONY: test
test: ## Exécute la suite de tests avec détection de races
	go test -race -count=1 ./...

.PHONY: test-short
test-short: ## Exécute la suite de tests en mode court (sans les tests lents)
	go test -race -count=1 -short ./...

.PHONY: cover
cover: ## Calcule la couverture de tests et génère un rapport HTML
	go test -race -count=1 -coverprofile=$(COVER_OUT) -covermode=atomic ./...
	go tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
	go tool cover -func=$(COVER_OUT) | tail -1

# Seuils de couverture (T-52, docs/11-strategie-de-tests.md §1). Ils sont
# imposés en intégration continue : une couverture qui régresse sans que
# personne ne le remarque ne protège plus de rien.
COVER_MIN_GLOBAL ?= 80
COVER_MIN_BDPM   ?= 85
COVER_MIN_STORE  ?= 80
COVER_MIN_API    ?= 75

# La cible délègue au même script que la CI, avec les mêmes seuils : deux
# implémentations du contrôle finiraient par diverger, et c'est toujours la
# locale qui rassure à tort.
.PHONY: cover-check
cover-check: ## Vérifie les seuils de couverture par paquet (identique à la CI)
	@go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=$(COVER_OUT) ./... > /dev/null || \
		{ echo "les tests échouent, couverture non évaluable" ; exit 1 ; }
	@./.github/scripts/check-coverage.sh $(COVER_OUT) $(COVER_MIN_GLOBAL) \
		internal/bdpm=$(COVER_MIN_BDPM) \
		internal/store=$(COVER_MIN_STORE) \
		internal/api=$(COVER_MIN_API)

# Tests de charge (T-55). K6_BASE et K6_KEY doivent pointer sur une instance
# en service ; les budgets côté serveur sont vérifiés séparément, voir
# tests/load/README.md.
K6_BASE  ?= http://127.0.0.1:18080
K6_IMAGE ?= grafana/k6:latest
K6_RUN    = docker run --rm -i --network host -v "$(PWD)/tests/load:/scripts:ro" \
              -e BASE_URL=$(K6_BASE) -e API_KEY=$(K6_KEY) -e ADMIN_KEY=$(K6_ADMIN_KEY) $(K6_IMAGE) run

.PHONY: load-smoke
load-smoke: ## Fumée : un appel par endpoint
	$(K6_RUN) /scripts/smoke.js

.PHONY: load-mixed
load-mixed: ## Charge nominale : 200 VU / 5 min, puis vérification des budgets
	$(K6_RUN) /scripts/mixed.js
	@tests/load/check-budgets.sh $(K6_BASE)

.PHONY: load-reload
load-reload: ## Bascule sous charge : zéro erreur exigée (test décisif)
	$(K6_RUN) /scripts/reload.js

.PHONY: load-spike
load-spike: ## Montée brutale : dégradation en 429, jamais d'effondrement
	$(K6_RUN) /scripts/spike.js

.PHONY: load-soak
load-soak: ## Endurance : 50 VU pendant 1 h, détection de fuite
	$(K6_RUN) /scripts/soak.js

.PHONY: bench-budgets
bench-budgets: ## Mesure les budgets de latence de docs/07 §2.1 (données réelles, réseau exclu)
	GOMAXPROCS=2 go test -tags live -run TestLive_BudgetsDeLatence -v -timeout 900s ./internal/api/

.PHONY: load-check
load-check: ## Vérifie les budgets de latence côté serveur après une campagne
	@tests/load/check-budgets.sh $(K6_BASE)

.PHONY: openapi-lint
openapi-lint: ## Valide le contrat OpenAPI (spectral, via Docker)
	npx --yes @stoplight/spectral-cli@$(SPECTRAL_VERSION) \
		lint api/openapi.yaml --ruleset api/.spectral.yaml

.PHONY: alerts-lint
alerts-lint: ## Valide les règles d'alerte Prometheus (promtool, via Docker)
	docker run --rm -v "$(PWD)/deploy/prometheus:/rules:ro" --entrypoint promtool \
		prom/prometheus:latest check rules /rules/alerts.yml

.PHONY: lint
lint: ## Analyse statique via golangci-lint (make tools si absent)
	@if [ ! -x "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint introuvable dans $(TOOLS_DIR) — exécutez 'make tools' d'abord." >&2; \
		exit 1; \
	fi
	$(GOLANGCI_LINT) run ./...

.PHONY: fmt
fmt: ## Formate le code (gofmt) et lance go vet ; échoue si du code n'est pas formaté
	@unformatted=$$(gofmt -l -w .); \
	if [ -n "$$unformatted" ]; then \
		echo "Fichiers reformatés par gofmt :" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi
	go vet ./...

.PHONY: run
run: ## Démarre le serveur (charge .env si présent)
	@if [ -f .env ]; then \
		echo "Chargement de .env"; \
		set -a; source .env; set +a; go run ./cmd/meds-api; \
	else \
		go run ./cmd/meds-api; \
	fi

.PHONY: sync
sync: ## Lance une synchronisation one-shot de la BDPM
	go run ./cmd/bdpm-sync --data-dir $(DATA_DIR)

.PHONY: bench
bench: ## Exécute les benchmarks avec mesure des allocations
	go test -run '^$$' -bench . -benchmem ./...

.PHONY: fuzz
fuzz: ## Lance chaque cible de fuzz pendant $(FUZZ_TIME) (6 cibles, T-54)
	@targets=$$(grep -rlE '^func Fuzz[A-Za-z0-9_]*\(f \*testing\.F\)' --include='*_test.go' . 2>/dev/null); \
	if [ -z "$$targets" ]; then \
		echo "Aucune cible de fuzz trouvée : les cibles existent, un renommage les a rendues invisibles." >&2; \
		exit 1; \
	fi; \
	status=0; \
	for file in $$targets; do \
		dir=$$(dirname "$$file"); \
		names=$$(grep -oE '^func (Fuzz[A-Za-z0-9_]*)\(f \*testing\.F\)' "$$file" | sed -E 's/^func (Fuzz[A-Za-z0-9_]*).*/\1/'); \
		for name in $$names; do \
			echo "==> $$dir -run $$name -fuzz $$name -fuzztime $(FUZZ_TIME)"; \
			go test "$$dir" -run "$$name" -fuzz "$$name" -fuzztime $(FUZZ_TIME) || status=1; \
		done; \
	done; \
	exit $$status

.PHONY: docker
docker: ## Construit l'image Docker
	$(DOCKER) build \
		--build-arg VERSION=$(GIT_VERSION) \
		--build-arg COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) .

.PHONY: tools
tools: ## Installe golangci-lint et govulncheck (versions épinglées) dans ./tools/bin
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(abspath $(TOOLS_DIR)) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@echo "Outils installés dans $(TOOLS_DIR) :"
	@$(GOLANGCI_LINT) version
	@$(GOVULNCHECK) -version

.PHONY: vulncheck
vulncheck: ## Recherche de vulnérabilités connues (make tools si absent)
	@if [ ! -x "$(GOVULNCHECK)" ]; then \
		echo "govulncheck introuvable dans $(TOOLS_DIR) — exécutez 'make tools' d'abord." >&2; \
		exit 1; \
	fi
	$(GOVULNCHECK) ./...

.PHONY: clean
clean: ## Supprime les artefacts de build, de couverture et d'outillage local
	rm -rf $(BIN_DIR) $(COVER_OUT) $(COVER_HTML) $(TOOLS_DIR)
