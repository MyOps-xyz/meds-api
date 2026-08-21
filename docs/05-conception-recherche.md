# 05 — Conception de la recherche

## 1. Le problème à résoudre

La recherche est ce qui distingue l'API d'un simple téléchargement de fichiers. Elle doit
absorber l'écart entre **ce que tape un utilisateur** et **ce que contient la BDPM**.

Cet écart est important, car les dénominations de la source sont écrites pour l'administration,
pas pour la recherche :

| L'utilisateur tape | La BDPM contient |
|---|---|
| `doliprane` | `DOLIPRANE 1000 mg, comprimé` |
| `paracetamol` | `PARACÉTAMOL` (accentué, en capitales) |
| `paracetamole`, `paracétamol` | idem |
| `amlodipine` | `BÉSILATE D'AMLODIPINE` (le sel, pas la fraction) |
| `doliprane 1000` | ordre des mots et ponctuation variables |

Quatre exigences en découlent : insensibilité à la casse et aux accents, tolérance aux fautes de
frappe, recherche sur les substances autant que sur les noms commerciaux, et un classement qui
remonte le médicament attendu en tête.

Contrainte de conception : **tout doit tenir dans le processus**, sans moteur externe. Aucun
Elasticsearch, aucun Bleve — 15 857 documents ne le justifient pas, et un service supplémentaire
ruinerait la simplicité de déploiement.

---

## 2. Normalisation

Appliquée **identiquement à l'indexation et à la requête** — c'est la condition pour que les deux
se rencontrent. Une divergence entre les deux chaînes de normalisation est le bug classique des
moteurs de recherche maison ; d'où un test dédié (T-19) vérifiant qu'un seul et même code est
utilisé des deux côtés.

```
"BÉSILATE D'AMLODIPINE 5 mg, comprimé"
  ↓ 1. minuscules (unicode.ToLower, pas ASCII)
"bésilate d'amlodipine 5 mg, comprimé"
  ↓ 2. décomposition NFD  (é → e + ◌́)
"be ́silate d'amlodipine 5 mg, comprime ́"
  ↓ 3. suppression des marques diacritiques (catégorie Mn)
"besilate d'amlodipine 5 mg, comprime"
  ↓ 4. ponctuation et apostrophes → espaces
"besilate d amlodipine 5 mg  comprime"
  ↓ 5. découpage sur les espaces, tokens de 1 caractère écartés
["besilate", "amlodipine", "mg", "comprime"]
```

Réalisée en **une passe**, sur `golang.org/x/text/unicode/norm` seul :

```go
func Normalize(s string) string {
    decomposed := norm.NFD.String(s)   // valeur immuable, sûre en concurrence
    // puis une boucle : marques Mn écartées, lettres et chiffres minusculés,
    // tout le reste devient un séparateur unique.
}
```

**Un `transform.Chain` partagé serait une course de données.** C'était la rédaction initiale :

```go
var normalizer = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
```

`transform.String` appelle `Reset` puis `Transform` sur le transformeur, qui porte des tampons
internes : deux goroutines qui l'utilisent simultanément se corrompent mutuellement. Or `Normalize`
est appelée **à chaque requête de recherche** et **à chaque construction de Store** — donc en
parallèle en permanence.

Le détecteur de course l'a mis en évidence le 15/08/2026, entre une construction de Store et une
recherche concurrente. La défaillance n'aurait pas été un plantage visible mais des **résultats de
recherche silencieusement faux**, le pire mode de défaillance possible pour cette API. La
réécriture en une passe supprime tout état partagé — et se trouve être **40 % plus rapide** :
recherche exacte à 4,1 µs contre 7,0 µs, autocomplétion à 415 ns contre 968 ns.

`TestNormalize_SansCourseNiCorruption` et `TestStore_ConstructionPendantRecherche` verrouillent la
propriété : ils doivent rester exécutés sous `-race`.

Deux points d'attention :

- **`unicode.ToLower`, pas `strings.ToLower` sur des octets.** `É` fait deux octets en UTF-8 ;
  un abaissement de casse octet par octet le corromprait.
- **L'apostrophe devient un séparateur.** `D'AMLODIPINE` doit produire le token `amlodipine`,
  sans quoi une recherche sur `amlodipine` manquerait les 157 spécialités indexées sous le nom
  du sel. C'est le cas typique où une normalisation trop timide dégrade fortement le rappel.

Les mots vides (`de`, `à`, `pour`, `en`) **ne sont pas supprimés**. Sur des dénominations courtes,
le gain de place est négligeable et le risque de perte de sens réel (`A 313`, `POUR CENT`).

---

## 3. Index inversé

```go
type InvertedIndex struct {
    tokens   []string           // tokens distincts, triés
    postings [][]SpecID         // postings[i] = spécialités contenant tokens[i], trié
    df       []int32            // fréquence documentaire, pour le scoring
}
```

Trois champs sont indexés, avec des poids différents :

| Champ indexé | Poids | Source |
|---|---:|---|
| Dénomination | 3,0 | `CIS_bdpm.txt` col. 2 |
| Dénomination de substance | 2,0 | `CIS_COMPO_bdpm.txt` col. 4 |
| Titulaire | 1,0 | `CIS_bdpm.txt` col. 11 |

Le tableau `tokens` est **trié**, ce qui permet trois choses avec une seule structure : la
recherche exacte par dichotomie en O(log n), la recherche par préfixe (borne inférieure puis
parcours tant que le préfixe tient — c'est l'autocomplétion), et un parcours ordonné pour le
débogage.

Les postings sont des `[]SpecID` triés : leur intersection est un balayage linéaire simultané,
sans allocation ni table de hachage intermédiaire.

**Volumétrie mesurée** (15/08/2026) : **8 959 tokens distincts**, et non les ~25 000 estimés
initialement. Un décompte indépendant en shell sur les trois champs indexés donne 9 410 tokens
avant repli des accents, que le repli ramène sous 9 000 : les deux mesures concordent. Les
dénominations de la BDPM sont bien plus répétitives que l'estimation ne le supposait — quelques
milliers de noms commerciaux, un vocabulaire de formes et de dosages très restreint.

Conséquence favorable : l'index inversé **et** l'index trigramme coûtent environ trois fois moins
que prévu, et le budget mémoire de [03](03-modele-de-donnees.md#45-budget-mémoire) est tenu avec
une large marge (**35,4 Mio de heap mesurés** après construction complète, budget 100 Mio).

### Recherche conjonctive

Tous les tokens de la requête doivent être présents (**ET**), ce qui correspond à l'intuition :
`doliprane 1000` ne doit pas remonter tous les DOLIPRANE.

Optimisation : les postings sont intersectés **du plus court au plus long**. La taille du résultat
étant bornée par la plus petite liste, commencer par elle réduit le travail d'un ordre de grandeur
sur les requêtes contenant un mot fréquent (`comprime` apparaît dans des milliers de dénominations).

```go
sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
result := lists[0]
for _, l := range lists[1:] {
    result = intersect(result, l)
    if len(result) == 0 { break }
}
```

Le **dernier token est traité comme un préfixe** lorsque la requête ne se termine pas par un
espace : cela rend la recherche réactive pendant la frappe (`dolipr` trouve `DOLIPRANE`) sans
endpoint distinct.

---

## 4. Classement

BM25 allégé, augmenté de bonus métier. La formule complète de BM25 (avec normalisation par
longueur de document) n'a pas de sens ici : les dénominations font toutes quelques mots, la
variance de longueur est faible. On garde la partie qui compte — la **fréquence documentaire
inverse**, qui fait qu'un token rare pèse plus qu'un token banal.

```
score = Σ_tokens ( idf(t) × poids_champ(t) ) + bonus

idf(t) = log( 1 + (N - df(t) + 0.5) / (df(t) + 0.5) )
```

| Bonus | Valeur | Justification |
|---|---:|---|
| Dénomination exactement égale à la requête | +50 | `doliprane` doit remonter `DOLIPRANE` avant `DOLIPRANE CODEINE` |
| Dénomination commençant par la requête | +20 | Intention de recherche par nom commercial |
| Token en début de dénomination | +10 | Le nom commercial est en tête |
| Spécialité `Commercialisée` | +15 | Un médicament indisponible intéresse rarement en premier |
| AMM `Autorisation active` | +5 | Écarte les autorisations abrogées |
| Correspondance sur substance uniquement | −5 | Une correspondance sur le nom est plus directe |

Le bonus de commercialisation est le plus structurant : **2 256 spécialités sur 15 857 ne sont pas
commercialisées** et pollueraient systématiquement les premiers résultats sans lui.

Les valeurs ci-dessus sont un point de départ, à ajuster sur le jeu de requêtes de référence (§7).
Elles sont regroupées dans une seule structure `RankParams` pour être réglées sans toucher à
l'algorithme.

---

## 5. Tolérance aux fautes de frappe

Déclenchée **uniquement en repli**, quand la recherche exacte donne moins de 5 résultats. Une
recherche fructueuse n'en paie donc jamais le coût.

### Index trigramme

Chaque token est décomposé en séquences de 3 caractères, avec marqueurs de bord :

```
"doliprane" → "  d", " do", "dol", "oli", "lip", "ipr", "pra", "ran", "ane", "ne "
```

```go
type TrigramIndex struct {
    grams map[[3]byte][]int32   // trigramme → indices de tokens
}
```

Similarité de Jaccard entre les trigrammes de la requête et ceux du token candidat :

```
J(a,b) = |T(a) ∩ T(b)| / |T(a) ∪ T(b)|
```

Seuil retenu : **0,4**. Au-dessus, les fautes courantes (lettre en trop, lettre manquante,
inversion) restent détectées ; en dessous, du bruit apparaît. `paracetamo` / `paracetamol` donne
J ≈ 0,80 ; `paracetamol` / `paroxetine` donne J ≈ 0,10.

Les candidats retenus sont **injectés comme tokens alternatifs** dans la recherche, avec un
score minoré (× 0,6) pour qu'une correspondance exacte reste toujours devant.

Le choix des trigrammes plutôt que de la distance de Levenshtein est délibéré : Levenshtein
imposerait de comparer la requête à chacun des 25 000 tokens (O(n·m) par comparaison), alors que
l'index trigramme ne remonte que quelques dizaines de candidats plausibles.

Coût mémoire : ~20 Mo, le poste le plus lourd du `Store`. Il est assumé — c'est la fonctionnalité
que les utilisateurs remarquent.

---

## 6. Autocomplétion

`/v1/suggest` réutilise le tableau `tokens` trié, sans structure supplémentaire :

```go
i := sort.SearchStrings(idx.tokens, prefix)          // borne inférieure, O(log n)
for ; i < len(idx.tokens) && strings.HasPrefix(idx.tokens[i], prefix); i++ {
    // candidat
}
```

Un trie (arbre préfixe) serait plus élégant sur le papier, mais un tableau trié est plus rapide en
pratique à cette échelle : les données sont contiguës, la dichotomie tient dans quelques lignes de
cache, là où un trie multiplie les sauts de pointeurs. Et il ne coûte rien de plus, puisque le
tableau existe déjà pour l'index inversé.

Les suggestions mêlent médicaments et substances, classées par fréquence documentaire décroissante,
plafonnées à 200. Budget : **< 1 ms au p99**.

---

## 7. Jeu de requêtes de référence

Figé dans `testdata/search_golden.json` et vérifié en intégration continue (T-23). C'est le
garde-fou contre les régressions de pertinence : sans lui, tout ajustement de bonus se fait à
l'aveugle.

| Requête | Attendu | Vérifie |
|---|---|---|
| `doliprane` | `DOLIPRANE` en tête | Correspondance exacte |
| `DOLIPRANE` | idem | Insensibilité à la casse |
| `paracetamol` | ≥ 200 résultats | Insensibilité aux accents |
| `paracétamol` | même tête de liste que ci-dessus | Équivalence accentuée / non accentuée |
| `paracetamo` | même tête de liste | Tolérance : lettre manquante |
| `paracetamoll` | même tête de liste | Tolérance : lettre en trop |
| `amlodipine` | ≥ 157 résultats | Découpage sur l'apostrophe (`D'AMLODIPINE`) |
| `doliprane 1000` | uniquement les dosages 1000 mg | Conjonction |
| `dolip` | `DOLIPRANE` présent | Recherche par préfixe |
| `sanofi` | spécialités du titulaire | Indexation du titulaire |
| `xyzzyqwerty` | 0 résultat, pas d'erreur | Absence de faux positifs |
| `` (vide) | `400 invalid_parameter` | Validation |
| `<script>` | 0 résultat, réponse échappée | Robustesse aux entrées hostiles |

Deux mesures agrégées complètent ces cas : **precision@5 ≥ 0,8** et **MRR ≥ 0,85** sur l'ensemble
du jeu.

---

## 8. Budget de performance

| Opération | Cible p99 | Justification |
|---|---:|---|
| Normalisation de la requête | < 10 µs | Quelques dizaines de caractères |
| Recherche exacte (2 tokens) | < 500 µs | Deux dichotomies + une intersection |
| Recherche avec repli trigramme | < 3 ms | Chemin dégradé, rarement emprunté |
| Autocomplétion | < 1 ms | Une dichotomie + un balayage borné |
| Construction de l'index au démarrage | < 300 ms | Inclus dans le budget de 500 ms du `Store` |

Aucune allocation sur le chemin de recherche en dehors du tableau de résultats : les tampons
intermédiaires d'intersection sont repris dans un `sync.Pool`.

---

## 9. Ce qui est écarté, et pourquoi

| Écarté | Raison |
|---|---|
| Elasticsearch / OpenSearch | Un service, une JVM et des gigaoctets de RAM pour 15 857 documents |
| Bleve, une bibliothèque de recherche Go | Une dépendance lourde et un index sur disque, pour des besoins que 400 lignes couvrent |
| SQLite FTS5 | Imposerait la base écartée par l'[ADR 0002](adr/0002-memoire-plutot-que-sqlite.md), et resterait plus lent qu'un index en mémoire |
| Recherche vectorielle / embeddings | Sur des noms de médicaments, la correspondance lexicale est plus fiable et explicable. Un modèle introduirait une opacité inacceptable sur de la donnée de santé. |
| Racinisation (stemming) française | Sur des noms propres et des termes chimiques, elle produit plus de faux positifs qu'elle n'améliore le rappel (`amlodipine` → `amlodipin`) |
| Distance de Levenshtein | O(n·m) contre tout le vocabulaire ; les trigrammes filtrent d'abord (§5) |
| Recherche phonétique (Soundex, Metaphone) | Calibrée pour l'anglais ; sur des noms de molécules, elle regroupe des substances sans rapport — dangereux ici |
