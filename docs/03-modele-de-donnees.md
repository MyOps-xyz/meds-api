# 03 — Modèle de données

## 1. Trois représentations distinctes

| Représentation | Rôle | Format |
|---|---|---|
| **Source** | ce que publie l'ANSM | 10 fichiers TSV, sales, hétérogènes |
| **Snapshot** | donnée normalisée, persistée, inspectable | NDJSON + `manifest.json` |
| **Store** | index en mémoire optimisé pour la lecture | structures Go compactes |

Les confondre serait une erreur : le snapshot privilégie la **lisibilité et la durabilité**,
le Store privilégie la **densité et la vitesse d'accès**. Le premier est le contrat de
persistance, le second un détail d'implémentation qui peut évoluer sans migration.

---

## 2. Modèle conceptuel

```
                          ┌──────────────────┐
                          │   Specialite     │  15 857
                          │   PK: cis        │
                          └────────┬─────────┘
        ┌──────────┬───────────┬───┴────┬───────────┬──────────┬─────────┐
        │ 1-N      │ 1-N       │ 1-N    │ 1-N       │ 1-N      │ 0-1     │
        ▼          ▼           ▼        ▼           ▼          ▼         ▼
 Presentation  Composant  Condition  AvisSMR   AvisASMR   Rupture   InfoMITM
    20 903      32 420     28 379     15 432    10 027      640       7 711
        │                              │           │
        │ N-1                          │ N-1       │ N-1
        ▼                              ▼           ▼
   Substance                     ┌──────────────────────┐
     3 895                       │  LienAvisCT (10 419) │
                                 │  PK: codeDossierHAS  │
                                 └──────────────────────┘

   Specialite ──N-M(via GroupeGenerique)── Specialite
                    1 671 groupes, 10 719 appartenances
```

**Le code CIS est la clé pivot** : il relie tout, sauf `LienAvisCT` qui se joint par le code de
dossier HAS.

---

## 3. Entités du snapshot

Un fichier NDJSON par entité. Noms de champs en `snake_case`, identiques à ceux exposés par l'API :
la sérialisation du snapshot et celle de la réponse HTTP partagent les mêmes structures Go, ce qui
supprime une couche de mapping et une source d'écart.

### 3.1 `specialites.ndjson`

```json
{
  "cis": "61266250",
  "denomination": "A 313 200 000 UI POUR CENT, pommade",
  "forme_pharmaceutique": "pommade",
  "voies_administration": ["cutanée"],
  "statut_amm": "Autorisation active",
  "procedure_amm": "Procédure nationale",
  "etat_commercialisation": "Commercialisée",
  "date_amm": "1998-03-12",
  "statut_bdm": null,
  "numero_autorisation_europeenne": null,
  "titulaires": ["PHARMA DEVELOPPEMENT"],
  "surveillance_renforcee": false
}
```

Normalisations appliquées : voies et titulaires **découpés sur `;`** et détrimés (P4) ;
date convertie de `JJ/MM/AAAA` en **ISO 8601** ; `surveillance_renforcee` converti en booléen ;
champs vides convertis en `null` plutôt qu'en chaîne vide, pour distinguer *absent* de *vide*.

### 3.2 `presentations.ndjson`

```json
{
  "cip13": "3400949497294",
  "cip7": "4949729",
  "cis": "60002283",
  "libelle": "plaquette(s) PVC PVDC aluminium de 30 comprimé(s)",
  "statut_administratif": "Présentation active",
  "etat_commercialisation": "Déclaration de commercialisation",
  "date_declaration": "2011-03-16",
  "agrement_collectivites": true,
  "taux_remboursement": [65],
  "prix_medicament_cents": 2434,
  "prix_public_cents": 2536,
  "honoraires_dispensation_cents": 102,
  "indications_remboursement": null
}
```

Décisions notables :

- **Les prix sont stockés en centimes, dans un entier.** Un prix est une valeur monétaire exacte ;
  le représenter en `float64` introduirait des erreurs de représentation (`24,34 €` n'est pas
  exactement représentable en binaire) et exposerait des `24.339999999999996` en JSON. La virgule
  décimale française est convertie à l'ingestion (P4).
- Un prix absent devient `null`, jamais `0` : **7 276 présentations sur 20 903 n'ont pas de prix**
  car non remboursables. Confondre les deux fausserait toute statistique.
- `taux_remboursement` est un **tableau d'entiers**, ce qui absorbe l'instabilité `65%` / `65 %`
  (P4) et le caractère multi-valué du champ.
- `agrement_collectivites` : booléen, `null` si `inconnu`.

### 3.3 `composants.ndjson`

```json
{
  "cis": "60002283",
  "element_pharmaceutique": "comprimé",
  "code_substance": "42215",
  "denomination_substance": "ANASTROZOLE",
  "dosage": "1,00 mg",
  "reference_dosage": "un comprimé",
  "nature": "SA",
  "numero_liaison": 1
}
```

`nature` vaut `SA` (substance active) ou `FT` (fraction thérapeutique). La valeur `ST` annoncée
par la documentation officielle **n'existe pas dans la donnée** (P3) ; le parseur l'accepte
néanmoins et la convertit en `FT`, pour le cas où l'ANSM alignerait la donnée sur sa doc.

Le `dosage` reste **du texte brut, non parsé** : il mêle notations métriques (`1,00 mg`) et
homéopathiques (`2CH à 30CH et 4DH à 60DH`). Toute normalisation serait une interprétation, donc
un risque d'erreur sur une donnée de santé.

### 3.4 `substances.ndjson` — entité dérivée

N'existe pas dans la source ; construite par agrégation de `CIS_COMPO_bdpm.txt`.

```json
{ "code": "42215", "denomination": "ANASTROZOLE", "nb_specialites": 12 }
```

Elle rend possible le référentiel `/v1/substances` et la recherche par principe actif.

### 3.5 `groupes_generiques.ndjson`

```json
{
  "id": "1",
  "libelle": "CIMETIDINE 200 mg - TAGAMET 200 mg, comprimé pelliculé",
  "membres": [
    { "cis": "65383183", "type": 0, "type_libelle": "princeps",   "ordre": 1 },
    { "cis": "67535309", "type": 1, "type_libelle": "générique",  "ordre": 2 }
  ]
}
```

Le groupe est **dénormalisé avec ses membres** : c'est l'unité de consultation naturelle, et cela
évite une jointure au moment de la requête. Les types admis sont `0`, `1`, `2`, `4` — la valeur
`3` n'existe pas, l'énumération est discontinue (§3.7 de [01](01-analyse-source-bdpm.md)).

### 3.6 `avis_smr.ndjson` / `avis_asmr.ndjson`

```json
{
  "cis": "60002283",
  "code_dossier_has": "CT-12345",
  "motif_evaluation": "Inscription (CT)",
  "date_avis": "2011-05-18",
  "valeur": "Important",
  "libelle": "Le service médical rendu par …",
  "lien_avis_ct": "https://www.has-sante.fr/…"
}
```

Le lien vers l'avis de la commission de la transparence est **résolu à l'ingestion** depuis
`HAS_LiensPageCT_bdpm.txt` : le consommateur n'a pas à connaître l'existence de ce fichier ni à
faire la jointure lui-même.

Pour l'ASMR, un champ `niveau` (entier 1–5) complète `valeur` **uniquement** quand celle-ci est un
chiffre romain pur. Les trois valeurs textuelles (`Commentaires sans chiffrage de l'ASMR`,
`V dans l'attente de données`…) laissent `niveau` à `null`.

> La date est convertie depuis `AAAAMMJJ` ici, alors que les autres fichiers utilisent
> `JJ/MM/AAAA` : deux parseurs distincts sont nécessaires.

### 3.7 `conditions.ndjson`, `ruptures.ndjson`, `mitm.ndjson`

```json
{ "cis": "63852237", "condition": "réservé à l'usage professionnel DENTAIRE" }
```

```json
{
  "cis": "62000612",
  "cip13": null,
  "code_statut": 2,
  "statut": "tension_approvisionnement",
  "libelle": "Tension d'approvisionnement",
  "libelle_source": "Tension dapprovisionnement",
  "date_debut": "2026-07-30",
  "date_debut_approximative": false,
  "date_mise_a_jour": "2026-07-31",
  "date_remise_disposition": null,
  "lien_ansm": "https://ansm.sante.fr/…"
}
```

Trois précautions sur les ruptures :

- `statut` et `libelle` sont **dérivés du code numérique**, jamais du libellé source, qui est
  incohérent (`Remise à disposition` vs `REmise à disposition`, apostrophes perdues — P5).
  `libelle_source` conserve la valeur brute pour la traçabilité et l'audit.
- `cip13` à `null` signifie que **toute la spécialité** est concernée, pas qu'on ignore laquelle.
- `date_debut_approximative` vaut `true` pour les fiches antérieures au 06/10/2023, où l'ANSM
  documente que la date de début est en réalité la date de mise à jour.

```json
{ "cis": "60003620", "code_atc": "R03BA01",
  "denomination": "BECLOSPIN 800 microgrammes/2ml suspension pour inhalation par nébuliseur",
  "lien_bdpm": "https://base-donnees-publique.medicaments.gouv.fr/extrait.php?specid=60003620" }
```

Les liens MITM sont **réécrits en HTTPS** à l'ingestion (la source fournit du `http://`).

### 3.8 `manifest.json`

Métadonnées du snapshot — clé de voûte de la détection de changement et de l'observabilité.

```json
{
  "version": "2026-08-14T04:12:33Z",
  "hash": "3f2a…",
  "generated_at": "2026-08-14T04:12:33Z",
  "generator": "bdpm-sync/1.0.0",
  "source_files": [
    { "name": "CIS_bdpm.txt", "sha256": "a1b2…", "bytes": 3168771,
      "lines": 15857, "encoding": "windows-1252", "line_ending": "crlf" }
  ],
  "counts": { "specialites": 15857, "presentations": 20903, "composants": 32420 },
  "quarantine": {
    "total": 4,
    "by_file": { "CIS_CIP_bdpm.txt": 4 },
    "by_reason": { "cis_orphelin": 4 },
    "reject_ratio": 0.000026
  },
  "warnings": ["4 lignes de CIS_CIP_bdpm.txt référencent un CIS absent de CIS_bdpm.txt"]
}
```

Le `hash` global est le SHA-256 des SHA-256 des dix fichiers sources, triés par nom. Il identifie
le snapshot, alimente les `ETag` HTTP et sert de graine aux curseurs de pagination.

---

## 4. Représentation en mémoire (le `Store`)

Le snapshot est optimisé pour l'humain, le `Store` pour le processeur. La conversion a lieu une
fois, au chargement, en ~300 ms.

### 4.1 Structure de tableaux plutôt que tableau de structures

Les spécialités sont stockées dans une **slice contiguë**, indexée par un entier `SpecID`
(`int32`), et non dans une `map[string]*Specialite`.

```go
type SpecID int32

type Store struct {
    specs   []Spec              // 15 857 entrées contiguës
    byCIS   map[uint32]SpecID   // CIS (entier) → position
    // ...
}

type Spec struct {
    CIS          uint32   // le CIS est numérique : 4 octets au lieu de 8 chiffres + en-tête
    Denom        string
    DenomNorm    string   // forme normalisée, pré-calculée pour la recherche
    Forme        uint16   // index dans la table d'internement (367 valeurs)
    Voies        []uint16
    StatutAMM    uint8    // énumération : 5 valeurs
    Procedure    uint8    // énumération : 8 valeurs
    EtatCommerc  uint8    // énumération : 2 valeurs
    StatutBdm    uint8    // énumération : 3 valeurs
    DateAMM      int32    // jours depuis l'epoch
    NumEuro      string
    Titulaires   []uint16 // 666 valeurs internées
    Surveillance bool
}
```

**Trois choix, trois gains :**

1. **Un CIS est un `uint32`, pas une `string`.** Les codes CIS sont des entiers à 8 chiffres, donc
   inférieurs à 2³². Économie : 8 octets de chaîne + 16 octets d'en-tête → 4 octets. Et la
   comparaison de clés devient une comparaison d'entiers.
2. **Interning des champs à faible cardinalité.** 367 formes pour 15 857 spécialités : stocker la
   chaîne 15 857 fois est un gaspillage. On stocke un `uint16` (2 octets) et une table de
   correspondance unique. Idem pour 666 titulaires et 68 voies. Économie mesurable : ~1,5 Mo.
3. **Énumérations en `uint8`.** `Autorisation active` fait 20 octets en chaîne, 1 en énumération,
   avec conversion en libellé au moment de la sérialisation.

### 4.2 Relations 1-N en format CSR

Les relations (présentations, composants, conditions, avis…) sont stockées en **Compressed Sparse
Row**, emprunté aux matrices creuses :

```go
type CSR[T any] struct {
    offsets []int32  // len = nbSpecialites + 1
    values  []T      // trié par SpecID
}

func (c *CSR[T]) Get(id SpecID) []T {
    return c.values[c.offsets[id]:c.offsets[id+1]]
}
```

Une `map[SpecID][]Presentation` coûterait 15 857 entrées de map plus 15 857 slices allouées
séparément — soit ~15 857 allocations éparpillées en mémoire, et un défaut de cache par accès.
Le CSR n'alloue **que deux slices contiguës** pour toute la relation, et `Get` est une simple
soustraction de bornes : aucune allocation, aucune recherche, données voisines en cache.

Les présentations d'un même médicament sont physiquement adjacentes en mémoire : les lire toutes
coûte une seule ligne de cache dans la plupart des cas.

### 4.3 Index d'accès direct

| Index | Type | Taille | Usage |
|---|---|---|---|
| `byCIS` | `map[uint32]SpecID` | 15 857 | `/v1/medicaments/{cis}` |
| `byCIP13` | `map[uint64]PresID` | 20 903 | `/v1/presentations/{cip}` |
| `byCIP7` | `map[uint32]PresID` | 20 903 | idem, forme courte |
| `bySubstance` | `map[uint32]SubID` | 3 895 | `/v1/substances/{code}` |
| `byGroupe` | `map[uint32]GrpID` | 1 671 | `/v1/groupes-generiques/{id}` |
| `byDossierHAS` | `map[string]int32` | 10 419 | jointure des liens CT |

Les codes CIP13 dépassent 2³² (13 chiffres) : `uint64`. Les CIP7 tiennent dans un `uint32`.

### 4.4 Index de recherche

Détaillés dans [05](05-conception-recherche.md) : index inversé `token → []SpecID`, index
trigramme pour la tolérance aux fautes, tableau trié de tokens pour l'autocomplétion.

### 4.5 Budget mémoire

| Poste | Estimation |
|---|---:|
| Chaînes (dénominations, libellés SMR/ASMR) | ~22 Mo |
| Structures `Spec`, `Presentation`, `Composant`… | ~8 Mo |
| Index CSR (offsets + valeurs) | ~3 Mo |
| Maps d'accès direct | ~4 Mo |
| Index inversé + trigrammes | ~25 Mo |
| Tables d'internement | < 1 Mo |
| **Total heap** | **~63 Mo** |
| RSS avec runtime Go et pointe de rechargement | **< 150 Mo** |

Le poste dominant est l'index de recherche, non la donnée. C'est assumé : il porte la
fonctionnalité qui différencie l'API d'un simple téléchargement de fichiers.

### 4.6 Immuabilité

Une fois `Build()` terminé, **aucun champ du `Store` n'est modifié**. Cette propriété n'est pas
une convention mais la condition de correction de la lecture sans verrou
([02](02-architecture.md#5-concurrence)).

Deux règles de codage la garantissent :

- Aucune méthode à récepteur pointeur ne modifie le `Store` ; toutes ont un récepteur valeur ou
  un pointeur en lecture seule.
- Les slices renvoyées par `Get` sont potentiellement partagées : les handlers ne doivent **jamais
  les modifier ni les trier en place**. Un test dédié (T-53) vérifie qu'une réponse ne mute pas le
  `Store`, en comparant un hachage de l'état avant et après une campagne de requêtes.

---

## 5. Correspondance source → API

| Fichier source | Entité snapshot | Endpoints |
|---|---|---|
| `CIS_bdpm.txt` | `specialites` | `/v1/medicaments`, `/v1/medicaments/{cis}` |
| `CIS_CIP_bdpm.txt` | `presentations` | `/v1/medicaments/{cis}/presentations`, `/v1/presentations/{cip}` |
| `CIS_COMPO_bdpm.txt` | `composants`, `substances` | `/v1/medicaments/{cis}/composition`, `/v1/substances` |
| `CIS_GENER_bdpm.txt` | `groupes_generiques` | `/v1/medicaments/{cis}/generiques`, `/v1/groupes-generiques` |
| `CIS_HAS_SMR_bdpm.txt` | `avis_smr` | `/v1/medicaments/{cis}/avis` |
| `CIS_HAS_ASMR_bdpm.txt` | `avis_asmr` | `/v1/medicaments/{cis}/avis` |
| `HAS_LiensPageCT_bdpm.txt` | *(fusionné dans les avis)* | `/v1/medicaments/{cis}/avis` |
| `CIS_CPD_bdpm.txt` | `conditions` | `/v1/medicaments/{cis}/conditions` |
| `CIS_CIP_Dispo_Spec.txt` | `ruptures` | `/v1/ruptures` |
| `CIS_MITM.txt` | `mitm` | `/v1/mitm`, filtre `?mitm=true` |

**Objectif O1 vérifié** : chaque fichier source est exposé par au moins un endpoint, et chacun de
leurs champs est atteignable.
