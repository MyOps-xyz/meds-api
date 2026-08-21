# Frontal mutualisé — opérations courantes.
# Toutes les cibles s'exécutent depuis deploy/edge/.

COMPOSE := docker compose

.DEFAULT_GOAL := help

.PHONY: help
help: ## Afficher cette aide
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: network
network: ## Créer le réseau Docker partagé `edge` (idempotent)
	@docker network inspect edge >/dev/null 2>&1 \
		|| docker network create edge

.PHONY: up
up: network validate ## Démarrer le frontal (ACME)
	$(COMPOSE) up -d

.PHONY: up-cloudflare
up-cloudflare: network validate-cloudflare ## Démarrer le frontal derrière Cloudflare
	@test -f certs/origin.pem || { echo "certs/origin.pem absent — voir README.md §Cloudflare" ; exit 1 ; }
	@test -f certs/cloudflare-origin-pull-ca.pem || \
		{ echo "certs/cloudflare-origin-pull-ca.pem absent — Authenticated Origin Pulls non configuré" ; exit 1 ; }
	$(COMPOSE) -f docker-compose.yml -f compose.cloudflare.yml up -d

.PHONY: validate-cloudflare
validate-cloudflare: ## Valider la configuration Cloudflare sans redémarrer
	docker run --rm -e MEDS_DOMAIN=$${MEDS_DOMAIN:-exemple.test} \
		-v $(PWD)/Caddyfile.cloudflare:/etc/caddy/Caddyfile:ro \
		-v $(PWD)/conf.d:/etc/caddy/conf.d:ro \
		-v $(PWD)/certs:/certs:ro \
		caddy:2.11.4-alpine caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

.PHONY: down
down: ## Arrêter le frontal (les volumes de certificats sont conservés)
	$(COMPOSE) down

.PHONY: validate
validate: ## Valider la configuration sans redémarrer
	$(COMPOSE) run --rm --no-deps --entrypoint caddy caddy \
		validate --config /etc/caddy/Caddyfile --adapter caddyfile

.PHONY: reload
reload: validate ## Recharger à chaud après ajout ou modification d'un service
	$(COMPOSE) exec caddy caddy reload --config /etc/caddy/Caddyfile

.PHONY: fmt
fmt: ## Reformater les fichiers Caddyfile (`caddy fmt` n'accepte qu'un fichier à la fois)
	@for f in Caddyfile conf.d/*.caddy; do \
		docker run --rm -v $(PWD):/w -w /w caddy:2.11.4-alpine \
			caddy fmt --overwrite "$$f" && echo "  formaté : $$f"; \
	done

.PHONY: logs
logs: ## Suivre le journal du frontal
	$(COMPOSE) logs -f caddy

.PHONY: routes
routes: ## Lister les domaines effectivement servis
	@grep -hE '^[a-z0-9{$$].*\{$$' conf.d/*.caddy | sed 's/ *{$$//'

.PHONY: ca
ca: ## Exporter l'AC interne de Caddy (environnements en .localhost)
	$(COMPOSE) exec caddy \
		cat /data/caddy/pki/authorities/local/root.crt > caddy-root.crt
	@echo "Écrit dans caddy-root.crt — l'importer dans le trousseau du poste :"
	@echo "  sudo security add-trusted-cert -d -r trustRoot \\"
	@echo "    -k /Library/Keychains/System.keychain caddy-root.crt"

.PHONY: backup
backup: ## Sauvegarder les certificats et la clé de compte ACME
	docker run --rm -v edge_caddy-data:/d:ro -v $(PWD):/out alpine \
		tar czf /out/caddy-data-backup.tar.gz -C /d .
	@echo "Écrit dans caddy-data-backup.tar.gz"
