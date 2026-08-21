# 11 — Stratégie de tests

## 1. Ce qu'il faut réellement prouver

Sur ce projet, le risque n'est pas de mal implémenter un algorithme connu. Il est ailleurs, dans
trois zones précises :

1. **L'ingestion** — la source est sale, hétérogène et **change sans préavis**
   ([01](01-analyse-source-bdpm.md)). C'est là que les erreurs seront silencieuses et graves.
2. **La concurrence** — la lecture sans verrou n'est correcte que si le `Store` est réellement
   immuable. Une violation ne se manifesterait qu'en production, sous charge.
3. **La pertinence de la recherche** — un ajustement de bonus peut dégrader les résultats sans
   qu'aucun test fonctionnel ne le remarque.

L'effort de test est donc concentré là, plutôt que réparti uniformément. La couverture n'est pas
un objectif en soi : **80 % sur `internal/bdpm` et `internal/store/search` valent mieux que 90 %
en moyenne** obtenus en testant des accesseurs triviaux.

| Zone | Couverture visée | Justification |
|---|---:|---|
| `internal/bdpm` (ingestion) | ≥ 85 % | Zone à risque n° 1 |
| `internal/store` (dont `search.go`) | ≥ 80 % | Zone à risque n° 3, structures et invariants |
| `internal/api` (dont `middleware.go`) | ≥ 75 % | Sécurité ; couvert aussi en intégration |
| **Global** | **≥ 80 %** | |

La recherche et les intergiciels avaient été anticipés comme des packages distincts
(`internal/store/search`, `internal/middleware`) ; ils vivent en réalité dans `internal/store` et
`internal/api`. Les seuils portent donc sur les packages réels — un seuil visant un package
inexistant est ignoré en silence par `check-coverage.sh`, ce qui revient à n'avoir aucun seuil.

---

## 2. Pyramide de tests

```
        ┌──────────────────────────┐
        │  Charge (k6)             │  budgets de perf, rechargement sous charge
        ├──────────────────────────┤
        │  Fumée (compose)         │  le déploiement réel fonctionne
        ├──────────────────────────┤
        │  Intégration (httptest)  │  contrat d'API de bout en bout
        ├──────────────────────────┤
        │  Golden files            │  ingestion déterministe et fidèle
        ├──────────────────────────┤
        │  Unitaires + fuzzing     │  parsers, normalisation, recherche
        └──────────────────────────┘
```

---

## 3. Tests unitaires

### 3.1 Ingestion — le cœur du dispositif

Chaque piège de [01](01-analyse-source-bdpm.md#5-pièges-de-la-source) a **son test dédié**. Ce
tableau est le contrat de la tâche T-12 : aucun piège ne doit rester sans test.

| Piège | Test | Vérifie |
|---|---|---|
| P1 encodages | `TestDetectEncoding` | UTF-8 valide → tel quel ; sinon Windows-1252 ; `é` correct dans les deux cas |
| P2 pas de cache HTTP | `TestChangeDetection` | Contenu identique → aucune reconstruction |
| P3 `FT` vs `ST` | `TestParseComposition_NatureFT` | `SA`, `FT` et `ST` acceptés ; `ST` converti en `FT` |
| P4 valeurs sales | `TestNormalize_TauxPrixTitulaire` | `65%` et `65 %` → `65` ; `24,34` → `2434` ; titulaire détrimé |
| P5 libellés incohérents | `TestRupture_LibelleDeriveDuCode` | `REmise à disposition` → libellé canonique ; source conservée |
| P6 orphelins | `TestValidate_CISOrphelin` | 4 orphelins mis en quarantaine, ingestion réussie |
| P7 fins de ligne | `TestScanner_CRLF_NoTrailingNewline` | Dernière ligne sans `\n` lue ; `\r` retiré ; lignes vides ignorées |
| P8 guillemets nus | `TestScanner_BareQuotes` | Guillemet conservé littéralement, aucun champ tronqué |

Le test P4 sur les prix mérite une attention particulière, car c'est l'erreur la plus discrète :

```go
func TestParsePriceCents(t *testing.T) {
    cases := []struct{ in string; want *int32 }{
        {"24,34", ptr(2434)},
        {"1,02",  ptr(102)},
        {"430,37", ptr(43037)},
        {"",      nil},          // absent ≠ zéro — 7 276 présentations concernées
        {"0,00",  ptr(0)},       // zéro réel
    }
    // …
}
```

Le cas `{"24,34" → 2434}` échoue si l'implémentation tronque au lieu d'arrondir
(`24.34 * 100 == 2433.9999…`). C'est précisément ce qu'on veut attraper.

### 3.2 Recherche

| Test | Vérifie |
|---|---|
| `TestNormalizeToken` | Casse, NFD, diacritiques, apostrophe séparatrice |
| `TestNormalizeSymmetry` | **La requête et l'index passent par le même code** |
| `TestInvertedIndex_Intersect` | Conjonction correcte, ordre par taille croissante |
| `TestTrigramJaccard` | `paracetamo`/`paracetamol` > 0,4 ; `paracetamol`/`paroxetine` < 0,4 |
| `TestSearchGolden` | Jeu de requêtes de référence ([05](05-conception-recherche.md#7-jeu-de-requêtes-de-référence)) |

`TestNormalizeSymmetry` est le plus important : il vérifie qu'indexation et requête ne peuvent pas
diverger, en s'assurant qu'un même corpus indexé est retrouvé par chacun de ses propres tokens.

### 3.3 Invariants du `Store`

| Test | Vérifie |
|---|---|
| `TestCSR_Boundaries` | `Get` renvoie exactement les éléments du bon parent, y compris aux bornes |
| `TestCSR_EmptyRelation` | Un CIS sans présentation renvoie une slice vide, pas `nil` ni une panique |
| `TestIntern_Roundtrip` | `Resolve(Intern(s)) == s` pour tout `s` |
| `TestStore_Immutable` | **Une campagne de requêtes ne modifie pas le `Store`** |

`TestStore_Immutable` compare un hachage de l'état interne avant et après 10 000 requêtes variées.
C'est la garantie sur laquelle repose toute la concurrence
([02](02-architecture.md#5-concurrence)) ; sans ce test, l'invariant n'est qu'une convention.

### 3.4 Concurrence

**`-race` est obligatoire sur l'ensemble de la suite**, en local comme en CI. C'est le seul moyen
fiable de détecter une lecture concurrente incorrecte.

```go
func TestConcurrentReadDuringSwap(t *testing.T) {
    // 100 lecteurs en boucle + 10 bascules de Store en parallèle
    // Attendu : aucune course détectée, aucune réponse incohérente,
    //           chaque lecteur voit un état cohérent de bout en bout
}
```

---

## 4. Golden files

Le mécanisme central de non-régression de l'ingestion (T-12).

```
testdata/golden/
  input/     échantillon figé des 10 fichiers (~200 lignes chacun, encodage d'origine préservé)
  expected/  snapshot NDJSON + manifest attendus
```

L'échantillon est choisi pour **contenir tous les cas difficiles**, pas pour être représentatif :
lignes à guillemets nus, CIS orphelins, prix absents, taux `65 %` et `65%`, libellé de rupture
incohérent, dernière ligne sans saut de ligne, ligne vide, valeurs `SA` et `FT`.

```bash
go test ./internal/bdpm -run TestGolden            # vérifie
go test ./internal/bdpm -run TestGolden -update    # régénère après changement volontaire
```

L'ingestion doit être **déterministe** : deux exécutions sur la même entrée produisent un snapshot
identique octet pour octet. Cela impose un tri stable partout où l'ordre n'est pas naturellement
défini (notamment les parcours de `map`, non déterministes en Go).

> Les fichiers d'entrée sont commités **dans leur encodage d'origine** — Latin-1 pour neuf d'entre
> eux. Une conversion accidentelle en UTF-8 par un éditeur ou par `.gitattributes` viderait le
> test P1 de son sens. Un `.gitattributes` marquant `testdata/golden/input/** binary` prévient ce
> risque.

---

## 5. Tests d'intégration

Serveur complet monté avec `httptest`, sur un snapshot figé.

| Famille | Cas |
|---|---|
| Contrat | Chaque endpoint de [04](04-specification-api.md) : statut, forme, champs obligatoires |
| Erreurs | `400`, `401`, `404`, `406`, `429`, `503` au format RFC 9457 avec `request_id` |
| Pagination | Parcours complet sans doublon ni omission ; curseur périmé → `400 cursor_stale` |
| Cache | `ETag` stable ; `If-None-Match` → `304` ; `ETag` change après rechargement |
| `include` | Chaque valeur, combinaisons, valeur inconnue |
| Vide vs absent | CIS existant sans présentation → `200` + tableau vide ; CIS inconnu → `404` |
| Sécurité | Scénarios de [06](06-securite.md#11-vérification) |

Une vérification transversale, dérivée de l'objectif **O1** : un test parcourt le snapshot complet
et s'assure que **chaque champ de chaque entité est atteignable par au moins un endpoint**. C'est
ce qui garantit qu'aucune donnée de la BDPM n'est ingérée puis oubliée.

Le contrat OpenAPI est validé dans les deux sens : `spectral lint` sur le fichier, et vérification
que les réponses réelles respectent le schéma déclaré — un OpenAPI qui ment est pire qu'absent.

---

## 6. Fuzzing

Les parsers reçoivent des données externes non maîtrisées : le fuzzing y est particulièrement
adapté (T-54).

```go
func FuzzParseSpecialites(f *testing.F) {
    f.Add([]byte("61266250\tA 313\tpommade\tcutanée\t…"))
    f.Fuzz(func(t *testing.T, data []byte) {
        _, _ = ParseSpecialites(data)   // exigence : ne jamais paniquer
    })
}
```

Objectif unique et clair : **aucune panique, aucun blocage, aucune consommation mémoire
déraisonnable**, quelle que soit l'entrée. Un parser qui panique sur une donnée malformée
transformerait un changement de format ANSM en indisponibilité totale.

Cibles : les dix parsers, la normalisation, le décodeur de curseur de pagination, le normaliseur
de requête de recherche.

Exécution : 1 minute par cible en CI, campagnes longues en nocturne. Toute entrée produisant un
échec est ajoutée au corpus `testdata/fuzz/`.

---

## 7. Tests de charge

Détail du protocole en [07](07-performance.md#5-protocole-de-mesure).

| Scénario | Objectif |
|---|---|
| `mixed.js` | Charge nominale représentative, 200 VU / 5 min → budgets tenus |
| `spike.js` | Montée brutale à 1 000 VU → dégradation propre, `429` plutôt qu'effondrement |
| `reload.js` | **Rechargement sous charge → zéro erreur** |
| `soak.js` | 50 VU pendant 1 h → RSS stable, absence de fuite |

`reload.js` est le test décisif : il valide la promesse centrale de l'architecture (E2 de
[09](09-pipeline-mise-a-jour.md)). Son critère est absolu — **une seule requête en erreur invalide
la conception**, il ne s'agit pas d'une moyenne.

---

## 8. Tests de fumée

Exécutés sur le `docker compose` réel, en CI et après chaque déploiement (T-56) :

1. `docker compose --profile caddy up -d`
2. Attendre `/readyz` → `200` (délai maximum : 90 s, synchronisation initiale comprise)
3. Appeler `/v1/dataset`, `/v1/medicaments?q=doliprane`, `/v1/medicaments/{cis}`,
   `/v1/presentations/{cip13}`
4. Vérifier le certificat TLS et la redirection HTTP → HTTPS
5. Vérifier que `/metrics` est **refusé** depuis l'extérieur
6. Recommencer avec `--profile traefik`

Le point 6 n'est pas facultatif : les deux profils étant livrés, les deux doivent être vérifiés,
sans quoi le second se dégrade silencieusement.

---

## 9. Intégration continue

| Étape | Bloquant | Durée visée |
|---|---|---|
| `golangci-lint run` | oui | < 1 min |
| `go test -race ./...` | oui | < 3 min |
| Seuils de couverture | oui | — |
| `go test -fuzz` (1 min/cible) | oui | < 5 min |
| `govulncheck ./...` | oui | < 1 min |
| `spectral lint api/openapi.yaml` | oui | < 10 s |
| `trivy fs` (HIGH, CRITICAL) | oui | < 2 min |
| Titre de PR conforme à Conventional Commits | oui | < 10 s |
| Fumée sur compose | oui | < 3 min |
| `benchstat` vs référence | **avertissement** | < 5 min |
| Charge k6 | nocturne | 30 min |

La construction multi-arch (`docker buildx build`) et la publication de l'image ne figurent pas
dans ce tableau : elles n'ont lieu qu'à la release, sur `main`, jamais sur une pull request.
L'image n'est pas scannée par `trivy image` — bâtie sur `distroless/static` avec un binaire
statique, sa surface se réduit aux dépendances Go, que `trivy fs` et `govulncheck` couvrent déjà.

Le benchmark est en avertissement et non bloquant : les exécuteurs d'intégration continue sont
partagés et bruités, une variation de 15 % y est courante sans régression réelle. Une régression
signalée doit être rejouée sur machine dédiée avant d'être tenue pour établie
([07](07-performance.md#51-micro-benchmarks-go)).

---

## 10. Conventions

- **Tests tabulaires** avec `t.Run` par cas nommé, `t.Parallel()` quand c'est sûr.
- Aucun accès réseau en test unitaire : la source ANSM est simulée par `httptest.Server`.
- Aucune dépendance temporelle : l'horloge est injectée, jamais `time.Now()` en dur.
- Une assertion vérifie **un comportement**, pas une implémentation : un test qui casse au moindre
  remaniement interne coûte plus qu'il ne protège.
- Les messages d'échec indiquent l'attendu **et** l'obtenu.

## 11. Matrice de couverture des objectifs

| Objectif ([00](00-vision-et-perimetre.md#3-objectifs)) | Preuve |
|---|---|
| O1 — 100 % des données exposées | Test transversal d'atteignabilité des champs (§5) |
| O2 — Recherche tolérante | `TestSearchGolden` (§3.2) |
| O3 — Mise à jour sans coupure | `reload.js` (§7) |
| O4 — Budgets de performance | `mixed.js` + `benchstat` (§7, §9) |
| O5 — Déploiement en une commande | Fumée sur les deux profils (§8) |
| O6 — Sécurité par défaut | Scénarios de [06](06-securite.md#11-vérification) (§5) |
| O7 — Contrat explicite | `spectral lint` + conformité des réponses (§5) |
