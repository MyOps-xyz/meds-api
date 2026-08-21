# 13 — Plan d'exécution

## 1. Cadrage de l'exécution

### 1.1 Capacité retenue

Le backlog ([12](12-backlog-taches.md)) est chiffré pour deux scénarios : environ 13 semaines
pour un développeur seul, ou 6 à 7 semaines pour une équipe de trois exploitant le parallélisme
décrit en son §11. Ce plan retient l'hypothèse **un développeur Go à temps plein, aidé d'un
agent** (assistance à la génération de code, à la revue et à la rédaction de tests, sans
parallélisation humaine réelle).

Conséquences directes de cette hypothèse :

- Les 127 points sont exécutés **en série**, lot par lot, dans l'ordre du backlog. Le tableau de
  parallélisation à trois développeurs (§11 du backlog) ne s'applique pas ; il n'est cité que
  comme référence de comparaison.
- La vitesse théorique d'un développeur confirmé (1 pt ≈ une demi-journée) est ajustée à la
  hausse grâce à l'agent sur les tâches mécaniques (parsers répétitifs, tests golden, squelette
  d'outillage, boilerplate de handlers), mais **pas** sur les tâches qui exigent un jugement
  humain difficile à déléguer : arbitrages de conception, lecture fine de la source ANSM,
  diagnostic de dérive mémoire, revue de sécurité. Le facteur d'accélération retenu est prudent :
  **1 pt = une demi-journée reste l'unité de référence**, l'agent servant à absorber les pointes
  de charge plutôt qu'à comprimer le chemin critique.
- Le calendrier cible est donc **13 semaines à temps plein** (127 pts ÷ ~10 pts/semaine),
  réparties en **13 sprints d'une semaine**, alignés strictement sur les 10 lots et les 5 jalons
  J1–J5. Aucune tâche n'est découpée entre deux personnes ; le risque de blocage n'est donc pas un
  risque de coordination mais un risque de **sous-estimation individuelle**, traité au §4.
- Le chemin critique du backlog (T-01 → T-04 → T-05 → T-06 → T-07 → T-08 → T-09 → T-11 → T-17 →
  T-18 → T-30 → T-53 → T-56) devient, dans ce plan à un développeur, **le calendrier lui-même** :
  il n'existe pas de tâche « en parallèle » qui masquerait un retard sur cette chaîne. Tout retard
  pris sur le lot 1 se répercute donc mécaniquement, jour pour jour, sur la date de livraison.
- La revue par un pair (exigée par la définition de terminé, §0 du backlog) est assurée par une
  revue différée : l'agent produit une proposition, le développeur la valide avant fusion. Ce
  n'est pas une revue à deux personnes indépendantes ; le §6 de ce document précise dans quels cas
  ce mode de revue ne suffit pas et où un second avis humain doit être sollicité.

### 1.2 Ce que ce plan ne fait pas

Il ne compresse pas le nombre de points ni ne retire de tâche du backlog approuvé. Il ne
propose pas non plus de parallélisation artificielle (par exemple traiter le lot 2 avant que le
lot 1 ait produit un snapshot réel) : le backlog l'exclut explicitement (« à attaquer en premier,
sans le paralléliser à l'excès »), et cette contrainte est renforcée dans un contexte mono-
développeur, où toute tentative de parallélisation reviendrait à interrompre le lot 1 avant sa
fin — ce que la définition de terminé interdit.

---

## 2. Découpage en sprints

Treize sprints d'une semaine. Chaque sprint reprend un ou plusieurs lots dans l'ordre du backlog,
sans les fragmenter au-delà de ce que permet la dépendance entre tâches. Le critère de sortie est
unique et vérifiable par lecture directe (commande exécutée, test passé, fichier produit) — pas
d'appréciation qualitative.

| Sprint | Tâches | Points | Objectif démontrable | Critère de sortie |
|---|---|---:|---|---|
| S1 | T-01, T-02, T-03, T-04, T-05, T-06, T-07 | 13 | Dépôt outillé, configuration validée, et amorce fiable de l'ingestion (téléchargement, encodage, lecture TSV) | `go build ./...`, `make lint`, `make test` et la CI passent sur le squelette ; `TestReadCIS_MITM` restitue 7 711 lignes |
| S2 | T-08, T-09 | 8 | Les 10 fichiers réels sont parsés, normalisés et validés avec quarantaine | Parsing intégral des 10 fichiers réels sans panique ; 4 CIS orphelins simulés vont en quarantaine sans faire échouer l'ingestion |
| S3 | T-10, T-11, T-12 | 6 | Snapshot NDJSON atomique reproductible, golden files en place — **J1 atteint** | Deux exécutions sur la même entrée produisent des fichiers identiques octet pour octet ; les 8 pièges ont chacun un test golden nommé et vert |
| S4 | T-13, T-14, T-15, T-16 | 7 | Structures mémoire, internement et index construits à partir du snapshot | `unsafe.Sizeof(Spec{}) ≤ 128` octets ; les 15 857 CIS et 20 903 CIP13 sont résolus par `byCIS`/`byCIP13` |
| S5 | T-17, T-18 | 7 | `Store` complet, bascule atomique sans interruption — **J2 amorcé** | Construction du `Store` < 500 ms sur le dataset réel ; `TestConcurrentReadDuringSwap` vert sous `-race` |
| S6 | T-19, T-20, T-21 | 8 | Recherche exacte et tolérante aux fautes opérationnelle | Recherche exacte < 500 µs au p99 ; `paracetamo` retrouve `PARACÉTAMOL` par repli trigramme |
| S7 | T-22, T-23 | 5 | Classement pertinent et autocomplétion — **J2 atteint** | `doliprane` place `DOLIPRANE` en première position ; les 13 cas de `search_golden.json` passent avec precision@5 ≥ 0,8 |
| S8 | T-24, T-25, T-26, T-27, T-28, T-29 | 10 | Serveur HTTP complet : routage, rendu, erreurs, pagination, cache conditionnel | Toutes les routes de [04](04-specification-api.md#2-endpoints) déclarées ; parcours de 231 résultats paginés sans doublon ni omission |
| S9 | T-30, T-31, T-32, T-33 | 12 | Handlers médicaments et référentiels, validation, OpenAPI — **J3 atteint** | `include=all` renvoie toutes les sections ; `spectral lint` sans erreur ; `/docs` fonctionne hors ligne |
| S10 | T-34, T-35, T-36, T-37, T-38, T-39 | 13 | Authentification, limitation de débit, durcissement, CORS, logs, tests d'abus — **J4 amorcé** | Les 10 scénarios d'abus de [06](06-securite.md#11-vérification) passent et sont automatisés en CI |
| S11 | T-40, T-41, T-42, T-43, T-44, T-45 | 12 | Scheduler, endpoint d'administration, CLI, métriques, sondes, alertes — **J4 atteint** | `promtool check rules` sans erreur ; `/readyz` bascule `503` → `200` à la publication du premier `Store` |
| S12 | T-46, T-47, T-48, T-49, T-50, T-51 | 12 | Image distroless durcie, Compose avec profils Caddy et Traefik, documentation d'exploitation | Image < 25 Mio ; `docker exec sh` échoue sur les deux profils ; un tiers déploie en suivant uniquement le README |
| S13 | T-52, T-53, T-54, T-55, T-56 | 13 | Couverture, tests d'intégration, fuzzing, charge, recette finale — **J5 atteint** | Les 7 objectifs O1–O7 sont vérifiés ; `soak.js` montre un RSS stable sur 1 h ; fumée verte sur `caddy` et sur `traefik` |

Total vérifié : 13 + 8 + 6 + 7 + 7 + 8 + 5 + 10 + 12 + 13 + 12 + 12 + 13 = **127 points**, 56
tâches, conforme au backlog.

---

## 3. Sprint 1 en détail

Sprint 1 = Lot 0 complet (T-01 à T-04) + amorce du lot 1 (T-05 à T-07). Objectif : poser les
garde-fous du dépôt puis démarrer le chemin critique dans l'ordre imposé par ses dépendances
(T-01 → T-04 → T-05 → T-06 → T-07). Aucune tâche de ce sprint ne peut commencer avant que sa
dépendance directe soit terminée et acceptée.

### Ordre d'exécution

1. T-01 — Squelette du projet
2. T-02 — Outillage de développement (dépend de T-01)
3. T-04 — Configuration (dépend de T-01 ; traitée avant T-03 car elle est sur le chemin critique)
4. T-03 — Intégration continue (dépend de T-02 ; peut s'exécuter en fin de sprint, en parallèle
   logique de T-05, car elle ne bloque aucune tâche suivante du sprint)
5. T-05 — Client HTTP ANSM (dépend de T-04)
6. T-06 — Détection d'encodage (dépend de T-05)
7. T-07 — Lecteur TSV robuste (dépend de T-06)

### T-01 — Squelette du projet

Livrables, fichier par fichier, conformes à l'arborescence de
[02-architecture.md §3.2](02-architecture.md#32-arborescence-du-code) :

- `go.mod` — module en Go 1.26.6.
- `.gitignore`
- `.gitattributes` — règle `testdata/golden/input/** binary` pour empêcher toute conversion
  d'encodage par Git sur les échantillons figés en Latin-1/Windows-1252.
- `LICENSE`
- `README.md`
- `cmd/meds-api/main.go` — point d'entrée vide à ce stade (câblage réel en T-24).
- `cmd/bdpm-sync/main.go` — point d'entrée vide à ce stade (CLI réelle en T-42).
- `internal/bdpm/` — répertoire créé, fichiers `client.go`, `encoding.go`, `scanner.go` créés
  en tâches suivantes du sprint ; `parse_*.go`, `normalize.go`, `validate.go`, `snapshot.go`,
  `report.go` en squelette (déclarations de paquet uniquement, implémentés au lot 1 §2).
- `internal/store/`, `internal/store/search/` — répertoires créés, vides à ce stade (lot 2 et 3).
- `internal/api/`, `internal/middleware/` — répertoires créés, vides à ce stade (lot 4 et 5).
- `internal/config/` — répertoire créé ; contenu réel livré par T-04.
- `internal/observability/`, `internal/scheduler/` — répertoires créés, vides à ce stade (lots 6
  et 7).
- `api/openapi.yaml` — fichier vide ou squelette minimal (contenu réel en T-33).
- `testdata/golden/` — répertoire créé, vide (peuplé en T-12).
- `deploy/` — répertoire créé, vide (peuplé au lot 8).

**Critères d'acceptation** (recopiés du backlog) :

- [ ] `go build ./...` réussit.
- [ ] L'arborescence correspond à celle documentée en [02](02-architecture.md#32-arborescence-du-code).
- [ ] `.gitattributes` empêche toute conversion d'encodage sur les golden files.

### T-02 — Outillage de développement

Livrables :

- `Makefile` — cibles `build`, `test`, `lint`, `run`, `sync`, `bench`, `docker`, et une cible
  `help` listant les cibles disponibles.
- `.golangci.yml` — active au minimum `gosec`, `errcheck`, `revive`, `staticcheck`, `ineffassign`,
  `misspell`.

**Critères d'acceptation** :

- [ ] `make lint` fonctionne sur un dépôt propre.
- [ ] `make test` fonctionne sur un dépôt propre.
- [ ] `make help` liste les cibles.

### T-04 — Configuration

Traitée avant T-03 dans ce sprint car elle se trouve sur le chemin critique (T-05 en dépend
directement), alors que T-03 (CI) ne bloque aucune autre tâche du sprint.

Livrables :

- `internal/config/config.go` — structure de configuration, chargement des 13 variables `MEDS_*`
  documentées en [02-architecture.md §7](02-architecture.md#7-configuration) (`MEDS_ADDR`,
  `MEDS_DATA_DIR`, `MEDS_API_KEYS`, `MEDS_ADMIN_KEY`, `MEDS_SYNC_CRON`, `MEDS_SYNC_JITTER`,
  `MEDS_SYNC_ON_START`, `MEDS_RATE_LIMIT`, `MEDS_RATE_BURST`, `MEDS_CORS_ORIGINS`,
  `MEDS_MAX_REJECT_RATIO`, `MEDS_SNAPSHOT_KEEP`, `MEDS_LOG_LEVEL`), valeurs par défaut sûres.
- `internal/config/config_test.go` — validation stricte au démarrage, y compris l'échec immédiat
  en l'absence de `MEDS_API_KEYS`.

**Critères d'acceptation** :

- [ ] Démarrage refusé si `MEDS_API_KEYS` est absent, avec un message explicite.
- [ ] Une valeur invalide (expression cron mal formée, limite négative) empêche le démarrage.
- [ ] Tous les paramètres documentés sont pris en compte.

### T-03 — Intégration continue

Livrables :

- `.github/workflows/ci.yml` — lint, `go test -race`, seuils de couverture (référence
  [11-strategie-de-tests.md](11-strategie-de-tests.md#9-intégration-continue)), `govulncheck`,
  build multi-arch, `trivy`, `spectral`.

**Critères d'acceptation** :

- [ ] Le pipeline passe au vert sur le squelette.
- [ ] Une régression volontaire (test cassé, vulnérabilité connue introduite) fait échouer le
      pipeline.

### T-05 — Client HTTP ANSM

Livrables :

- `internal/bdpm/client.go` — téléchargement parallèle des 10 fichiers
  ([09-pipeline-mise-a-jour.md §3.1](09-pipeline-mise-a-jour.md#31-téléchargement)) : `errgroup`
  limité à 4, 3 tentatives, backoff exponentiel avec aléa, délais explicites, `User-Agent`
  identifiable, `io.LimitReader` à 100 Mio, TLS strict.
- `internal/bdpm/client_test.go` — source ANSM simulée (serveur de test HTTP) couvrant les cas
  d'échec et de dépassement de délai.

**Critères d'acceptation** :

- [ ] Les 10 fichiers sont récupérés en moins de 60 s.
- [ ] Une source simulée renvoyant `500` déclenche 3 tentatives puis un abandon propre.
- [ ] Un fichier en échec annule la totalité du téléchargement.
- [ ] Aucune requête ne dépasse le délai imparti.

### T-06 — Détection d'encodage

Livrables :

- `internal/bdpm/encoding.go` — détection par fichier, à chaque synchronisation (piège P1) :
  UTF-8 valide → tel quel ; sinon Windows-1252 (via `golang.org/x/text`). Encodage retenu inscrit
  au manifest.
- `internal/bdpm/encoding_test.go` — cas `CIS_CIP_bdpm.txt` (UTF-8) et `CIS_bdpm.txt`
  (Windows-1252), y compris le mot « comprimé » comme marqueur de bon décodage.

**Critères d'acceptation** :

- [ ] `CIS_CIP_bdpm.txt` détecté UTF-8.
- [ ] `CIS_bdpm.txt` détecté Windows-1252.
- [ ] « comprimé » correctement décodé dans les deux cas.
- [ ] Un fichier basculant d'un encodage à l'autre entre deux synchronisations est traité sans
      intervention manuelle.

### T-07 — Lecteur TSV robuste

Livrables :

- `internal/bdpm/scanner.go` — `bufio.Scanner` avec buffer de 1 Mio, `TrimRight("\r")`, saut des
  lignes vides, découpage sur tabulation. Explicitement **sans** `encoding/csv` (pièges P7, P8).
- `internal/bdpm/scanner_test.go` — les 8 pièges de terminaison et de guillemets couverts
  individuellement, plus le cas `CIS_MITM.txt` (7 711 enregistrements sans saut de ligne final).

**Critères d'acceptation** :

- [ ] Les 8 pièges de terminaison et de guillemets sont couverts par un test.
- [ ] `CIS_MITM.txt` restitue bien 7 711 enregistrements malgré l'absence de saut de ligne final.
- [ ] Un guillemet nu est conservé littéralement.
- [ ] Aucune panique sur entrée malformée.

---

## 4. Registre des risques

| # | Risque | Probabilité | Impact | Signal d'alerte observable | Parade |
|---|---|---|---|---|---|
| R1 | Changement de format amont ANSM (colonne ajoutée/renommée, nouvel encodage) | Moyenne | Élevé — casse T-08/T-09 en silence si non détecté | Un parser lève une erreur de colonne manquante, ou le taux de rejet de T-09 dépasse `MEDS_MAX_REJECT_RATIO` sur une synchronisation qui passait avant | Validation de structure en première ligne de T-09 (avant toute logique métier) ; golden files (T-12) rejouables sur toute nouvelle source avant mise en production ; échec bloquant plutôt que dégradation silencieuse (cf. ADR implicite : un snapshot partiel est pire qu'un snapshot périmé) |
| R2 | Encodage instable ou mixte au sein d'un même fichier | Faible | Moyen — corruption de libellés (accents), non détectée sans test dédié | Le mot « comprimé » ou tout libellé accentué apparaît mal formé dans le snapshot NDJSON | T-06 détecte l'encodage par fichier à chaque synchronisation, jamais une valeur figée en dur ; test de non-régression sur les deux encodages connus (T-12) |
| R3 | Dérive du budget mémoire (RSS > 150 Mio en régime établi, > 250 Mio pendant rechargement) | Moyenne | Élevé — remet en cause le choix « mémoire plutôt que SQLite » (ADR 0002) | `go tool pprof` sur `/debug/pprof/heap` montre une croissance non expliquée par le volume de données ; RSS ne redescend pas 30 s après une bascule | Mesure systématique du RSS dès T-17/T-18 (critères d'acceptation chiffrés) ; SoA et internement (T-13/T-14) valident leur gain mémoire avant d'avancer sur le lot 3 ; profilage mémoire intégré aux tests de charge (T-55, `soak.js`) |
| R4 | Complexité du moteur de recherche sous-estimée (index inversé, trigrammes, BM25) | Moyenne | Moyen — 13 pts concentrés sur 5 tâches interdépendantes, seul lot sans golden files amont | precision@5 < 0,8 ou MRR < 0,85 sur `search_golden.json` (T-23) après implémentation complète de T-19 à T-22 | Jeu de requêtes de référence figé avant l'implémentation ([05](05-conception-recherche.md#7-jeu-de-requêtes-de-référence)) ; `RankParams` regroupés pour permettre un ajustement sans réécrire l'algorithme (T-22) ; sprint dédié par sous-étape (S6/S7) plutôt qu'un bloc unique |
| R5 | Indisponibilité de la source ANSM pendant le développement | Faible | Moyen — bloque le travail sur données réelles, pas sur le code lui-même | `make sync` échoue de façon répétée alors que le code de T-05 est correct (vérifiable par le serveur de test simulé de T-05) | Golden files (T-12) et jeux d'échantillons figés dès la fin du lot 1 permettent de continuer le développement des lots 2 à 9 sans dépendre de la disponibilité live de l'ANSM ; le client (T-05) est testé contre un serveur simulé, pas contre la source réelle, pour l'essentiel de la suite |
| R6 | Sous-estimation du lot 1 (chemin critique, 21 pts, 8 tâches) | Élevée | Élevé — tout retard ici décale mécaniquement les 12 sprints suivants dans un plan à un développeur | Le sprint S1, S2 ou S3 dépasse sa semaine allouée sans que le critère de sortie soit atteint | Le plan alloue déjà 3 sprints pleins (S1–S3, 27 pts) au lot 1 plutôt que la répartition à 3 développeurs (qui l'aurait étalé sur 3 semaines en parallèle d'autres lots) ; en cas de dépassement, appliquer la règle d'arbitrage du §5 (le lot 1 n'est jamais compressé, c'est le calendrier global qui glisse) |
| R7 | Tenue des budgets de performance (§2 de [07](07-performance.md)) découverte tardivement | Moyenne | Élevé — un dépassement détecté en S13 (T-55) oblige à revenir sur des choix de conception du lot 2 ou 3 | Un budget de latence (p99), de RSS ou de construction du `Store` (< 500 ms) est dépassé lors des tests de charge finaux | Les critères d'acceptation chiffrés de T-17, T-18, T-20, T-21, T-23 font mesurer les budgets dès leur sprint d'origine (S5 à S7), pas seulement en recette finale (S13) ; `make bench` disponible dès T-02 pour un usage continu |
| R8 | Revue par un pair affaiblie par l'absence d'un second développeur humain | Moyenne | Moyen — risque de biais de confirmation entre le développeur et l'agent sur les tâches de sécurité et de conception | Une tâche du lot 5 (sécurité) ou une décision d'architecture est fusionnée sans qu'aucun avis extérieur au binôme développeur/agent n'ait été sollicité | Sur les tâches de sécurité (lot 5) et les décisions structurantes, solliciter une revue humaine externe ponctuelle avant fusion ; documenter les décisions sous forme d'ADR (§6) pour rendre le raisonnement auditable a posteriori, même sans second réviseur immédiat |

---

## 5. Rituels et pilotage

### Indicateurs suivis

- **Points brûlés par sprint** — comparés aux points planifiés du sprint (§2) ; un écart supérieur
  à 20 % sur deux sprints consécutifs déclenche une revue du plan.
- **Tâches par jalon** — J1 (12 tâches), J2 (11 tâches), J3 (10 tâches), J4 (12 tâches), J5
  (11 tâches) ; suivi binaire terminé/non terminé, pas de pourcentage d'avancement partiel sur
  une tâche (cohérent avec la définition de terminé du backlog : « une tâche est terminée ou ne
  l'est pas »).
- **Couverture de tests** — suivie en continu dès que la CI (T-03) est en place ; seuils cibles
  définis en [11](11-strategie-de-tests.md#1-ce-quil-faut-réellement-prouver) et vérifiés
  formellement à T-52 (≥ 85 % sur `internal/bdpm` et `internal/store/search`, ≥ 80 % global).
- **Budgets de performance** (RSS, latence p99, temps de construction du `Store`) — mesurés dès
  qu'un critère d'acceptation chiffré le permet (à partir de S5), pas seulement à T-55.

### Rythme de revue

- **Revue de fin de sprint (hebdomadaire)** — vérification du critère de sortie unique du sprint
  (§2), mise à jour des indicateurs ci-dessus, décision de passage au sprint suivant ou de
  prolongation.
- **Point de jalon (à chaque J1–J5)** — vérification croisée avec les documents de référence
  concernés (par exemple les 7 objectifs O1–O7 de
  [00](00-vision-et-perimetre.md#8-critères-dacceptation-du-projet) à J5) avant de considérer le
  jalon acquis.
- **Revue continue du code** — chaque tâche suit la définition de terminé commune du backlog
  (tests, lint, `-race`, critères d'acceptation, documentation, revue) avant d'être considérée
  close ; pas d'attente de fin de sprint pour la revue individuelle d'une tâche.

### Règle d'arbitrage en cas de retard

1. **Le lot 1 (ingestion) n'est jamais compressé.** C'est le fondement documenté du projet
   (« la qualité y prime sur la vitesse ») et le seul lot sur lequel toute la suite dépend de
   données réelles. Un retard ici se traduit par un décalage du calendrier global, jamais par une
   réduction des critères d'acceptation de T-05 à T-12.
2. **Le lot compressible en priorité est le lot 8 (conteneurisation)**, en particulier les
   tâches non structurantes T-49/T-50 (un seul profil, Caddy ou Traefik, peut être livré en
   premier et l'autre différé sans remettre en cause l'architecture, cf. ADR 0005).
3. **Ordre de compression, du plus sûr au plus risqué** : réduction du périmètre documentaire non
   bloquant (partie de T-51) → report d'un des deux profils de déploiement (T-50 ou T-49) → report
   des tableaux de bord (T-45) sans toucher aux métriques et alertes elles-mêmes (T-43, T-44) →
   en dernier recours, report de tests de charge non critiques (scénarios secondaires de T-55, en
   conservant `reload.js` et `soak.js`).
4. **Ne sont jamais compressés** : le lot 1 dans son ensemble, T-18 (bascule atomique, cœur de la
   promesse « zéro coupure » de l'ADR 0003), le lot 5 (sécurité — un déploiement sans
   authentification ni durcissement n'est pas un déploiement partiel, c'est un déploiement
   dangereux), et T-56 (recette finale).

---

## 6. Gouvernance des décisions

Un nouvel ADR (`docs/adr/000N-*.md`, numérotation continue à partir de 0006) est ouvert — plutôt
que tranché directement dans le code — dès qu'une décision rencontrée pendant l'implémentation
présente au moins une des caractéristiques suivantes :

- Elle **contredit ou nuance** une décision déjà actée par un ADR existant (0001 Go, 0002 mémoire
  plutôt que SQLite, 0003 snapshot NDJSON et hot-swap, 0004 clés API SHA-256, 0005 Caddy et
  Traefik en profils) — par exemple si un budget mémoire s'avère intenable et remet en question
  l'ADR 0002.
- Elle a un **impact sur plus d'un lot** ou sur l'architecture décrite en
  [02](02-architecture.md) (par exemple un changement dans la manière dont `api`, `store` et
  `bdpm` communiquent).
- Elle introduit une **nouvelle dépendance externe** non prévue en
  [02-architecture.md §3.3](02-architecture.md#33-dépendances-externes), où la liste est
  volontairement réduite au strict nécessaire.
- Elle modifie un **budget chiffré** documenté ailleurs dans `docs/` (performance, sécurité,
  couverture de tests) plutôt que de simplement l'atteindre.
- Elle est **irréversible ou coûteuse à revenir en arrière** une fois du code écrit dessus (choix
  de format de snapshot, de schéma d'authentification, de stratégie de bascule).

À l'inverse, une décision **locale à une tâche**, sans impact sur les documents de conception
existants et réversible à faible coût (choix de nom de fonction, découpage interne d'un fichier,
ordre d'exécution de sous-étapes à l'intérieur d'une tâche) se tranche directement dans le code et
sa revue, sans ADR. Le doute se résout en faveur de l'ADR : un ADR inutile coûte peu, une décision
structurante non tracée coûte cher à reconstituer a posteriori — ce risque est explicitement
couvert par R8 du registre ci-dessus.

Un ADR est rédigé **avant** que l'implémentation qui en découle soit fusionnée, jamais après
coup en simple justification rétroactive, et il indique explicitement s'il remplace, précise ou
laisse inchangé un ADR antérieur.
