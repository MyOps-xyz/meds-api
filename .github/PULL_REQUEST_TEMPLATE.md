## Objet

<!-- Que fait ce changement, et pourquoi. Décrivez le problème avant la solution. -->

Closes #

## Type de changement

- [ ] Correction de bogue (sans rupture de contrat)
- [ ] Nouvelle fonctionnalité (sans rupture de contrat)
- [ ] Changement cassant (le contrat HTTP, la configuration ou le déploiement change)
- [ ] Documentation ou spécification seule
- [ ] Outillage, CI, dépendances

## Contrôles

- [ ] `make fmt` — code formaté, `go vet` propre
- [ ] `make lint` — aucune remontée
- [ ] `make test` — suite verte avec `-race`
- [ ] `make cover-check` — seuils de couverture tenus
- [ ] Un test échoue sans ce changement et passe avec (correction de bogue)

Contrôles complémentaires, si le changement les concerne :

- [ ] `make openapi-lint` — contrat OpenAPI modifié
- [ ] `make fuzz` — parseur, décodeur ou validateur d'entrée modifié
- [ ] `make bench-budgets` — chemin chaud du `store` ou de la recherche modifié
- [ ] `make load-reload` — chemin de lecture HTTP ou bascule de snapshot modifiés
- [ ] `make alerts-lint` — règles d'alerte modifiées

## Répercussions

<!-- Cochez ce qui a été mis à jour ; barrez ou supprimez ce qui ne s'applique pas. -->

- [ ] `api/openapi.yaml` et `docs/04-specification-api.md` (contrat HTTP)
- [ ] `.env.example` et le tableau de configuration du README (variable d'environnement)
- [ ] Un ADR dans `docs/adr/` (décision structurante)
- [ ] `docs/10-observabilite.md` et les règles d'alerte (métrique ou journal)
- [ ] `docs/08-deploiement-docker.md` ou `deploy/edge/README.md` (déploiement)

## Invariants

- [ ] Le sens des dépendances reste `api → store → bdpm`, sans retour
- [ ] Aucun verrou introduit sur le chemin de lecture
- [ ] Aucun secret ne peut atteindre les journaux, les métriques ou une réponse
- [ ] Aucune dépendance directe ajoutée — ou son ajout est justifié ci-dessous

## Notes pour la relecture

<!-- Points d'attention, arbitrages, ce qui reste à faire dans une pull request ultérieure. -->
