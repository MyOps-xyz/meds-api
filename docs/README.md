# Documentation de conception — meds-api

API REST haute performance exposant la **Base de Données Publique des Médicaments** (BDPM),
publiée par l'ANSM avec le concours de la HAS et du ministère de la Santé.

> **Statut** : implémentation terminée, lots 0 à 9 livrés. Recette des objectifs O1–O7 en
> [00 §8](00-vision-et-perimetre.md#8-critères-dacceptation-du-projet) — six sur sept tenus, O4
> partiellement (budget de recherche à remesurer sur l'infrastructure cible).
> **Date de rédaction** : 14 août 2026, mise à jour le 15/08/2026 après vérification sur données
> et infrastructure réelles.
> **Analyse de la source** : effectuée sur les fichiers réellement téléchargés le 14/08/2026, et
> corrigée le 15/08/2026 sur trois points mesurés (voir [ADR 0007](adr/0007-hors-perimetre-distinct-du-rejet.md)).

---

## Guide de lecture

Les documents sont numérotés dans l'ordre de lecture recommandé. Chacun est autonome, mais
les suivants supposent le précédent acquis.

| # | Document | Pour qui | Répond à |
|---|---|---|---|
| [00](00-vision-et-perimetre.md) | Vision et périmètre | Tous | Pourquoi ce projet, pour qui, où s'arrête-t-il ? |
| [01](01-analyse-source-bdpm.md) | Analyse de la source BDPM | Backend, Data | Que contiennent vraiment les 10 fichiers ? Quels pièges ? |
| [02](02-architecture.md) | Architecture | Backend, DevOps | Comment les pièces s'assemblent-elles ? |
| [03](03-modele-de-donnees.md) | Modèle de données | Backend | Quelles entités, quelles relations, quelle représentation mémoire ? |
| [04](04-specification-api.md) | Spécification de l'API | Backend, Intégrateurs | Quel est le contrat exact de chaque endpoint ? |
| [05](05-conception-recherche.md) | Conception de la recherche | Backend | Comment la recherche plein-texte fonctionne-t-elle ? |
| [06](06-securite.md) | Sécurité | Backend, DevOps, Sécurité | Quelles menaces, quelles contre-mesures ? |
| [07](07-performance.md) | Performance | Backend, DevOps | Quels budgets, comment les mesurer ? |
| [08](08-deploiement-docker.md) | Déploiement Docker | DevOps | Comment construire, déployer, exploiter ? |
| [09](09-pipeline-mise-a-jour.md) | Pipeline de mise à jour | Backend, DevOps | Comment le jeu de données se rafraîchit-il sans coupure ? |
| [10](10-observabilite.md) | Observabilité | DevOps, SRE | Que mesure-t-on, comment diagnostique-t-on ? |
| [11](11-strategie-de-tests.md) | Stratégie de tests | Tous | Comment prouve-t-on que ça marche ? |
| [12](12-backlog-taches.md) | Backlog des tâches | Chef de projet, Devs | Qui fait quoi, dans quel ordre, avec quels critères ? |
| [13](13-plan-execution.md) | Plan d'exécution | Chef de projet, Devs | Comment les 56 tâches se répartissent-elles dans le temps, avec quels risques ? |

### Décisions d'architecture (ADR)

Chaque décision structurante est tracée au format Nygard, **conséquences négatives incluses**.

| ADR | Décision |
|---|---|
| [0001](adr/0001-go-plutot-que-python.md) | Go plutôt que Python |
| [0002](adr/0002-memoire-plutot-que-sqlite.md) | Index en mémoire plutôt que SQLite |
| [0003](adr/0003-snapshot-ndjson-et-hot-swap-atomique.md) | Snapshot NDJSON et hot-swap atomique |
| [0004](adr/0004-cles-api-sha256-plutot-quargon2id.md) | Clés API en SHA-256 plutôt qu'Argon2id |
| [0005](adr/0005-caddy-et-traefik-en-profils.md) | Caddy et Traefik en profils Compose |
| [0006](adr/0006-frontal-mutualise-sur-reseau-externe.md) | Frontal mutualisé sur réseau Docker externe |
| [0007](adr/0007-hors-perimetre-distinct-du-rejet.md) | Le « hors périmètre » est distinct du rejet |

---

## Résumé exécutif

**Le problème.** La BDPM est distribuée en 10 fichiers TSV bruts, sans en-tête, aux encodages
hétérogènes, sans API. Chaque intégrateur (éditeur de logiciel médical, pharmacie, application
santé) réécrit le même parseur fragile.

**La solution.** Une API REST en lecture seule qui absorbe cette complexité une fois pour toutes,
maintient le jeu de données à jour automatiquement, et répond en moins d'une milliseconde.

**Le fait qui structure tout.** Le jeu de données complet pèse **22 Mo pour 15 857 médicaments**.
Il tient intégralement en RAM. Aucune base de données n'est nécessaire — ni SQLite, ni autre.
L'API charge un index immuable au démarrage et le remplace atomiquement à chaque mise à jour.

**La pile.** Go 1.26.6, zéro dépendance de routage, image Docker distroless < 25 Mo,
Caddy 2.11.4 ou Traefik 3.7.10 en frontal avec TLS automatique.

### Budgets à tenir

| Indicateur | Cible |
|---|---:|
| Lookup par CIS/CIP (p99) | < 200 µs |
| Recherche plein-texte (p99) | < 3 ms |
| Autocomplétion (p99) | < 1 ms |
| Débit sur 2 vCPU | > 20 000 req/s |
| Démarrage à froid | < 1 s |
| RSS en régime établi | < 150 Mo |
| Taille de l'image Docker | < 25 Mo |

---

## Démarrage rapide (cible, après implémentation)

```bash
cp .env.example .env          # renseigner MEDS_API_KEYS
make sync                     # première synchronisation depuis l'ANSM
docker compose --profile caddy up -d
curl -H "Authorization: Bearer $KEY" https://localhost/v1/medicaments?q=doliprane
```

---

## Conventions de la documentation

- **Toute affirmation chiffrée sur la donnée est mesurée**, pas estimée. La méthode de mesure est
  donnée dans [01](01-analyse-source-bdpm.md#7-méthode-de-mesure) et rejouable.
- Les termes métier (CIS, CIP, SMR, ASMR, AMM, princeps) sont définis dans le
  [glossaire](00-vision-et-perimetre.md#glossaire).
- Les identifiants de tâches `T-NN` référencent [12-backlog-taches.md](12-backlog-taches.md).
- Le français est la langue de la documentation ; le code, les noms de champs JSON et les
  messages de log sont en anglais, **sauf les valeurs issues de la BDPM** qui restent telles
  quelles (`Autorisation active`, `Commercialisée`).

## Sources officielles

- Portail BDPM : <https://base-donnees-publique.medicaments.gouv.fr/>
- Page de téléchargement : <https://base-donnees-publique.medicaments.gouv.fr/telechargement>
- Spécification officielle des fichiers (PDF v4, mise à jour du 27/08/2025) :
  <https://base-donnees-publique.medicaments.gouv.fr/download/file/Contenu_et_format_des_fichiers_telechargeables_dans_la_BDM_v4.pdf>
