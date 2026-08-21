# ADR 0006 — Frontal mutualisé sur réseau Docker externe

- **Statut** : acceptée
- **Date** : 15/08/2026
- **Décideurs** : chef de projet, DevOps
- **Complète** : [ADR 0005](0005-caddy-et-traefik-en-profils.md)

## Contexte

L'ADR 0005 a retenu Caddy et Traefik en profils `docker-compose`. Les deux publient `80:80` et
`443:443` **depuis le projet meds-api** ([08 §3](../08-deploiement-docker.md#3-docker-compose)).

Un seul processus peut détenir un port hôte. Tant que l'un de ces profils est actif, la machine ne
peut plus servir aucun autre site : toute seconde stack échoue au démarrage sur
`Bind for 0.0.0.0:80 failed: port is already allocated`. Le Caddyfile n'a par ailleurs qu'un seul
bloc `{$DOMAIN}` : même en levant le conflit de port, un second nom de domaine ne serait pas servi.

Or meds-api est une API de taille modeste — 22 Mo de données, ~250 Mio de RAM
([02](../02-architecture.md)). Elle sera très souvent colocalisée avec d'autres services sur un
serveur unique. Le montage de l'ADR 0005 fait implicitement l'hypothèse d'une machine dédiée, qui
n'était nulle part énoncée.

C'est un défaut de **périmètre**, pas de choix d'outil : Caddy est précisément conçu pour servir
N domaines derrière un frontal unique.

## Options considérées

### A. Conserver le frontal dans le projet, en y ajoutant les autres services

Ajouter des blocs de site au `Caddyfile` du projet pour le blog, le monitoring, etc.

Rejetée : couple le cycle de vie de services sans rapport. Un `docker compose down` sur meds-api
coupe tout le serveur, et la configuration d'un service tiers vit dans le dépôt d'un autre.

### B. Frontal partagé, configuration par fichiers sur un réseau Docker externe

Le proxy devient une stack indépendante (`deploy/edge/`) qui détient seule 80/443. Chaque
application rejoint un réseau `edge` externe et ne publie aucun port. Un service = un fichier dans
`conf.d/`.

### C. Frontal partagé piloté par labels Docker (`caddy-docker-proxy`, ou Traefik)

Chaque conteneur déclare son domaine en labels ; le proxy découvre tout seul.

Séduisant, mais impose de monter `/var/run/docker.sock` dans le conteneur exposé sur Internet —
un accès au socket Docker équivaut à un accès root sur l'hôte. Le gain d'ergonomie ne se
matérialise qu'à partir de cinq ou six services ; en dessous, `conf.d/` reste plus lisible et
auditable.

## Décision

**Retenir l'option B.** Les profils `caddy` et `traefik` du projet sont conservés pour le
développement local et le serveur mono-service ; la stack `deploy/edge/` est le montage recommandé
dès qu'un second service est hébergé.

Le `Caddyfile` partagé ne déclare **aucun nom d'hôte** : il n'expose que des fragments réutilisables
(`hardened`, `ops-privees`) puis `import conf.d/*.caddy`. La configuration commune vit à un seul
endroit ; ajouter un service ne la modifie pas.

### Corollaire : `/metrics` refusé inconditionnellement

La vérification de ce montage a mis au jour un défaut de la règle documentée jusqu'ici, présente
dans les deux profils :

```caddyfile
@internal { path /metrics /debug/*
            not remote_ip private_ranges }
respond @internal 403
```

Elle suppose que l'adresse source vue par le proxy est celle du client. C'est vrai sur un serveur
Linux avec le bridge Docker par défaut, où le DNAT préserve l'adresse d'origine. Ça ne l'est pas
sous Docker Desktop, OrbStack, avec `userland-proxy`, ni derrière un CDN : l'adresse apparaît alors
privée, la condition `not private_ranges` est fausse, et `/metrics` **est servi à Internet par une
règle censée le refuser**. Mesuré le 15/08/2026 : `curl https://…/metrics` renvoyait `200`.

Le refus devient donc inconditionnel dans les deux profils et dans la stack partagée. Prometheus
n'a pas à passer par le frontal : il scrute `api:8080/metrics` en direct sur le réseau `internal`,
dépourvu de route entrante depuis Internet.

## Conséquences

### Positives

- **80/443 mutualisables** : N services, N domaines, N certificats, un seul frontal.
- **Cycles de vie découplés** : redéployer meds-api ne coupe plus les autres sites.
- **Aucun port hôte publié par les applications** : la surface exposée de la machine se décrit en
  une règle de pare-feu.
- **Ajout d'un service à chaud** : un fichier dans `conf.d/`, `caddy reload`, aucune coupure —
  vérifié avec deux services simultanés.
- **Fermé par défaut** : un domaine absent de `conf.d/` n'obtient pas de certificat et sa poignée
  de main TLS échoue.
- La correction de `/metrics` supprime une hypothèse implicite sur la topologie réseau, qui aurait
  échoué silencieusement en production derrière un CDN.

### Négatives — assumées

- **Une stack de plus à exploiter**, avec son `up`/`down` et sa sauvegarde de certificats propres.
- **Rayon d'action élargi** : une panne du frontal coupe tous les services, pas seulement l'API.
  Atténuation : `make reload` valide la configuration avant de l'appliquer, donc une configuration
  invalide ne peut pas être mise en service.
- **Deux montages documentés** (profil intégré, frontal partagé), donc un choix de plus à
  expliquer au lecteur — et un risque de dérive entre les deux Caddyfile.
- **Deux prérequis faciles à oublier** : `docker network create edge` et un `container_name`
  stable par service. Sans eux, Caddy répond `502`.
- **Le port 80 doit rester libre** pour le challenge ACME HTTP-01. Un service qui le monopolise
  casse le renouvellement de tous les domaines de la machine.

### Ce qui invaliderait cette décision

Au-delà de cinq ou six services, la maintenance manuelle de `conf.d/` deviendrait plus coûteuse que
la découverte automatique. Il faudrait alors reconsidérer l'option C — en isolant l'accès au socket
Docker derrière un mandataire en lecture seule de type `docker-socket-proxy`, jamais en montant le
socket brut dans le conteneur exposé.

À l'inverse, si meds-api devait être déployée sur une machine strictement dédiée, la stack
partagée serait un détour inutile et le profil `caddy` de l'ADR 0005 suffirait.
