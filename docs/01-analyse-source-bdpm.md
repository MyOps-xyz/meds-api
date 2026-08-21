# 01 — Analyse de la source BDPM

> **Toutes les valeurs de ce document ont été mesurées** sur les fichiers réellement téléchargés
> le **14 août 2026**, et non reprises de la documentation officielle. Là où les deux divergent,
> l'écart est signalé explicitement. La méthode de mesure est donnée au §7 et rejouable.

---

## 1. Point d'accès

Base : `https://base-donnees-publique.medicaments.gouv.fr/download/file/<NOM_FICHIER>`

Un seul fichier fait exception : les informations importantes, servies sur
`/download/CIS_InfoImportantes.txt` et **générées à la volée** (leur nom canonique documenté est
`CIS_InfoImportantes_AAAAMMJJhhmiss_bdpm.txt`).

### En-têtes de réponse observés

```http
HTTP/1.1 200 OK
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="CIS_bdpm.txt"
Cache-Control: private, must-revalidate
Pragma: no-cache
Expires: 0
```

**Absents** : `ETag`, `Last-Modified`, `Content-Length` (sur HEAD), `Content-Encoding: gzip`.

Conséquences directes sur la conception :

- Un `GET` conditionnel (`If-Modified-Since`) est **inopérant** : testé, le serveur renvoie
  systématiquement `200` avec le corps complet.
- La compression n'est pas proposée : les 22 Mo transitent en clair à chaque synchronisation.
- **La seule détection de changement fiable est un hachage du contenu téléchargé** (SHA-256).

## 2. Inventaire des fichiers

| Fichier | Lignes | Taille | Col. | Encodage | Fins de ligne | Contenu |
|---|---:|---:|---:|---|---|---|
| `CIS_bdpm.txt` | 15 857 | 3,17 Mo | 12 | **Latin-1** | CRLF | Spécialités (médicaments) |
| `CIS_CIP_bdpm.txt` | 20 903 | 4,15 Mo | 13 | **UTF-8** | **LF** | Présentations (boîtes), prix |
| `CIS_COMPO_bdpm.txt` | 32 420 | 2,73 Mo | 8 | Latin-1 | CRLF | Composition (substances) |
| `CIS_HAS_SMR_bdpm.txt` | 15 432 | 4,56 Mo | 6 | Latin-1 | CRLF | Avis SMR de la HAS |
| `CIS_HAS_ASMR_bdpm.txt` | 10 027 | 4,53 Mo | 6 | Latin-1 | CRLF | Avis ASMR de la HAS |
| `HAS_LiensPageCT_bdpm.txt` | 10 419 | 0,51 Mo | 2 | ASCII | CRLF | Liens vers les avis CT |
| `CIS_GENER_bdpm.txt` | 10 719 | 1,22 Mo | 5 | Latin-1 | CRLF | Groupes génériques |
| `CIS_CPD_bdpm.txt` | 28 379 | 1,33 Mo | 2 | Latin-1 | CRLF | Conditions de prescription |
| `CIS_CIP_Dispo_Spec.txt` | 640 | 0,14 Mo | 8 | Latin-1 | CRLF | Ruptures et tensions |
| `CIS_MITM.txt` | 7 711 | 1,14 Mo | 4 | Latin-1 | CRLF* | Intérêt thérapeutique majeur |
| **Total** | **~152 500** | **~22,3 Mo** | | | | |

\* sans terminateur sur la dernière ligne — voir piège P7.

### Volumétrie exploitable

| Grandeur | Valeur |
|---|---:|
| Spécialités (CIS distincts) | 15 857 |
| Présentations (CIP13 distincts) | 20 903 |
| Substances distinctes | 3 895 |
| Titulaires d'AMM distincts | 666 |
| Groupes génériques | 1 671 |
| Formes pharmaceutiques distinctes | 367 |
| Voies d'administration atomiques | 68 |
| Combinaisons de voies observées | 156 |
| Codes ATC distincts (MITM) | 1 255 |
| CIS possédant au moins une présentation | 14 609 |
| Présentations par CIS (moyenne / max) | 1,43 / 37 |
| Amplitude des dates d'AMM | 11/03/1974 → 02/04/2026 |

**C'est le chiffre déterminant du projet : 22 Mo, 15 857 médicaments.** Le jeu de données complet
tient dans le cache L3 d'un serveur moderne. Voir
[ADR 0002](adr/0002-memoire-plutot-que-sqlite.md).

## 3. Schéma des fichiers

Format commun, confirmé par la documentation officielle et par la mesure :
**valeurs séparées par des tabulations, aucun délimiteur de champ, aucune ligne d'en-tête**.
La première ligne est déjà une ligne de données.

### 3.1 `CIS_bdpm.txt` — Spécialités (12 colonnes)

| # | Champ | Type | Exemple / valeurs |
|---:|---|---|---|
| 1 | Code CIS | 8 chiffres | `61266250` |
| 2 | Dénomination | texte (≤ 255) | `A 313 200 000 UI POUR CENT, pommade` |
| 3 | Forme pharmaceutique | texte | `pommade` — 367 valeurs |
| 4 | Voies d'administration | texte, séparateur `;` | `cutanée;orale;sublinguale` |
| 5 | Statut administratif AMM | énuméré | voir ci-dessous |
| 6 | Type de procédure AMM | énuméré | voir ci-dessous |
| 7 | État de commercialisation | énuméré | `Commercialisée` (13 601), `Non commercialisée` (2 256) |
| 8 | Date d'AMM | `JJ/MM/AAAA` | `12/03/1998` |
| 9 | StatutBdm | énuméré, souvent vide | vide (13 603), `Warning disponibilité` (2 236), `Alerte` (18) |
| 10 | N° autorisation européenne | texte | souvent vide |
| 11 | Titulaire(s) | texte, séparateur `;` | `  PHARMA DEVELOPPEMENT` — 666 valeurs |
| 12 | Surveillance renforcée | `Oui`/`Non` | `Non` (15 352), `Oui` (505) |

**Statut administratif AMM** : `Autorisation active` (14 915), `Autorisation abrogée` (737),
`Autorisation archivée` (190), `Autorisation retirée` (10), `Autorisation suspendue` (5).

**Type de procédure AMM** : `Procédure nationale` (7 042), `Procédure décentralisée` (3 349),
`Procédure centralisée` (2 338), `Procédure de reconnaissance mutuelle` (1 504),
`Enreg homéo (Proc. Nat.)` (1 319), `Autorisation d'importation parallèle` (243),
`Enreg phyto (Proc. Nat.)` (57), `Enreg phyto (Proc. Dec.)` (5).

> La colonne 11 est documentée comme multi-valuée (séparateur `;`). **Aucune occurrence
> multi-valuée n'existe dans la donnée actuelle**, mais le séparateur doit tout de même être
> traité : la documentation fait foi pour le futur.

### 3.2 `CIS_CIP_bdpm.txt` — Présentations (13 colonnes)

| # | Champ | Type | Exemple / remarque |
|---:|---|---|---|
| 1 | Code CIS | 8 chiffres | clé étrangère vers 3.1 |
| 2 | Code CIP7 | 7 chiffres | `4949729` |
| 3 | Libellé de présentation | texte | `plaquette(s) PVC PVDC aluminium de 30 comprimé(s)` |
| 4 | Statut administratif | énuméré | `Présentation active`, `Présentation abrogée` |
| 5 | État de commercialisation | énuméré | voir ci-dessous |
| 6 | Date de déclaration | `JJ/MM/AAAA` | `16/03/2011` |
| 7 | Code CIP13 | 13 chiffres | `3400949497294` — **unique, 20 903 valeurs** |
| 8 | Agrément collectivités | `oui`/`non`/`inconnu` | `oui` (14 972), `non` (5 931) |
| 9 | Taux de remboursement | texte, séparateur `;` | `65%`, `65 %`, `100%`, `30%`, `15%` — **format instable, piège P4** |
| 10 | Prix du médicament (€) | décimal, **virgule** | `24,34` — vide pour 7 276 lignes |
| 11 | Prix public France (€) | décimal, **virgule** | `25,36` |
| 12 | Honoraires de dispensation (€) | décimal, **virgule** | `1,02` |
| 13 | Indications de remboursement | texte long | renseigné si plusieurs taux |

**État de commercialisation** : `Déclaration de commercialisation` (17 221),
`Déclaration d'arrêt de commercialisation` (3 518),
`Arrêt de commercialisation (le médicament n'a plus d'autorisation)` (161),
`Déclaration de suspension de commercialisation` (3).

**7 276 présentations sur 20 903 (34,8 %) n'ont aucun prix** : elles ne sont pas remboursables.
Le modèle doit distinguer *prix absent* de *prix nul*.

### 3.3 `CIS_COMPO_bdpm.txt` — Compositions (8 colonnes)

| # | Champ | Type | Exemple |
|---:|---|---|---|
| 1 | Code CIS | 8 chiffres | `60002283` |
| 2 | Élément pharmaceutique | texte | `comprimé` |
| 3 | Code substance | numérique | `42215` — 3 895 valeurs |
| 4 | Dénomination substance | texte | `ANASTROZOLE` |
| 5 | Dosage | texte libre | `1,00 mg`, `2CH à 30CH et 4DH à 60DH` |
| 6 | Référence du dosage | texte | `un comprimé` |
| 7 | Nature du composant | `SA` / `FT` | **`SA` (26 911), `FT` (5 509) — piège P3** |
| 8 | N° de liaison SA↔FT | entier | permet d'apparier substance et fraction |

Le dosage est **du texte libre non normalisable** : il mêle unités métriques et notations
homéopathiques. Il est servi tel quel, sans tentative de parsing.

### 3.4 `CIS_HAS_SMR_bdpm.txt` — Avis SMR (6 colonnes)

`Code CIS` · `Code dossier HAS` · `Motif d'évaluation` · `Date de l'avis (AAAAMMJJ)` ·
`Valeur du SMR` · `Libellé du SMR`

**Valeurs** : `Important` (9 568), `Insuffisant` (2 813), `Modéré` (1 661), `Faible` (1 162),
`Commentaires` (99), `Non précisé` (88).

Le libellé est un texte long (294 octets de moyenne par ligne) : ce fichier est le plus lourd
rapporté au nombre de lignes.

> **Format de date différent des autres fichiers** : `AAAAMMJJ` ici, `JJ/MM/AAAA` ailleurs.

### 3.5 `CIS_HAS_ASMR_bdpm.txt` — Avis ASMR (6 colonnes)

Mêmes colonnes que 3.4, avec la valeur d'ASMR en romain :
`V` (7 694), `IV` (1 263), `III` (605), `II` (207),
`Commentaires sans chiffrage de l'ASMR` (160), `I` (85), `V dans l'attente de données` (13).

L'échelle n'est donc **pas purement numérique** : trois valeurs sont textuelles. Le champ reste
une chaîne, avec un champ dérivé `niveau` (entier 1–5) renseigné seulement si applicable.

### 3.6 `HAS_LiensPageCT_bdpm.txt` — Liens vers les avis (2 colonnes)

`Code dossier HAS` · `URL de l'avis complet`

**Seul fichier qui ne contient pas de code CIS.** Il se joint aux fichiers SMR et ASMR par le
code de dossier HAS.

### 3.7 `CIS_GENER_bdpm.txt` — Groupes génériques (5 colonnes)

| # | Champ | Valeurs |
|---:|---|---|
| 1 | Identifiant du groupe | 1 671 groupes |
| 2 | Libellé du groupe | `CIMETIDINE 200 mg - TAGAMET 200 mg, comprimé pelliculé` |
| 3 | Code CIS | clé étrangère |
| 4 | **Type de générique** | `0` princeps (1 793) · `1` générique (8 829) · `2` par complémentarité posologique (36) · `4` substituable (61) |
| 5 | N° de tri dans le groupe | entier |

Noter l'absence de la valeur `3` : l'énumération est **discontinue**. Un parseur qui supposerait
un intervalle 0–4 continu accepterait une valeur inexistante.

### 3.8 `CIS_CPD_bdpm.txt` — Conditions de prescription (2 colonnes)

`Code CIS` · `Condition de prescription ou de délivrance`

Relation 1-N : un CIS porte souvent plusieurs conditions. Principales valeurs :
`liste I` (10 894), `médicament nécessitant une surveillance particulière pendant le traitement`
(1 746), `prescription hospitalière` (1 361), `liste II` (1 295),
`prescription réservée aux spécialistes et services ONCOLOGIE MEDICALE` (794),
`réservé à l'usage HOSPITALIER` (790).

### 3.9 `CIS_CIP_Dispo_Spec.txt` — Ruptures et tensions (8 colonnes)

| # | Champ | Remarque |
|---:|---|---|
| 1 | Code CIS | |
| 2 | Code CIP13 | **vide si toute la spécialité est concernée** |
| 3 | Code statut | `1` rupture · `2` tension · `3` arrêt de commercialisation · `4` remise à disposition |
| 4 | Libellé du statut | **incohérent — piège P5** |
| 5 | Date de début | `JJ/MM/AAAA` |
| 6 | Date de mise à jour | `JJ/MM/AAAA` |
| 7 | Date de remise à disposition | `JJ/MM/AAAA`, souvent vide |
| 8 | Lien vers la fiche ANSM | URL |

Distribution : tension (447), remise à disposition (115), rupture (61), arrêt (17).

> Pour les fiches antérieures au 06/10/2023, la « date de début » est en réalité la date de mise
> à jour. Cette imprécision est documentée par l'ANSM et doit être signalée dans la réponse API.

### 3.10 `CIS_MITM.txt` — Intérêt thérapeutique majeur (4 colonnes)

`Code CIS` · `Code ATC` · `Dénomination` · `Lien BDPM`

Les liens fournis utilisent `http://` et l'ancien chemin `extrait.php?specid=`. À normaliser en
HTTPS lors de l'ingestion.

### 3.11 Graphe des relations

```
CIS_bdpm (CIS)  ◄── clé pivot de toute la base
   ├── CIS_CIP_bdpm        (CIS) 1-N  présentations
   ├── CIS_COMPO_bdpm      (CIS) 1-N  composition
   ├── CIS_GENER_bdpm      (CIS) 1-N  appartenance à un groupe générique
   ├── CIS_CPD_bdpm        (CIS) 1-N  conditions de prescription
   ├── CIS_CIP_Dispo_Spec  (CIS) 1-N  ruptures  [+ CIP13 optionnel]
   ├── CIS_MITM            (CIS) 1-1  statut MITM + code ATC
   ├── CIS_HAS_SMR_bdpm    (CIS) 1-N  avis SMR ──┐
   └── CIS_HAS_ASMR_bdpm   (CIS) 1-N  avis ASMR ─┤
                                                  └─► HAS_LiensPageCT (code dossier HAS)
```

---

## 4. Écarts entre documentation officielle et donnée réelle

| Point | Documentation (PDF v4) | Donnée réelle | Décision |
|---|---|---|---|
| Nature du composant | `SA` ou **`ST`** | `SA` et **`FT`** | Accepter les deux, exposer `SA`/`FT` |
| Encodage | non précisé | Latin-1 **sauf** `CIS_CIP_bdpm.txt` en UTF-8 | Détection par fichier |
| Fins de ligne | non précisé | CRLF **sauf** `CIS_CIP_bdpm.txt` en LF | Normaliser |
| Titulaires multiples | séparateur `;` | aucune occurrence | Implémenter quand même |
| Rubrique MITM | « rubrique *xxx* », « Code ATC : *yyy* » | — | La doc contient des **marqueurs non remplacés** |
| Intégrité CIS | implicite | 4 CIS orphelins | Quarantaine |

---

## 5. Pièges de la source

Chacun est **prouvé par la mesure** et rattaché à une tâche du [backlog](12-backlog-taches.md).

### P1 — Encodages hétérogènes et mouvants → T-06

`CIS_CIP_bdpm.txt` est en UTF-8 valide ; les 8 autres fichiers accentués sont en Latin-1.

```
$ iconv -f UTF-8 -t UTF-8 CIS_CIP_bdpm.txt   # OK
$ iconv -f UTF-8 -t UTF-8 CIS_bdpm.txt       # illegal byte sequence
```

L'ANSM migre visiblement fichier par fichier. **Ne jamais coder l'encodage en dur.**
Règle à appliquer à chaque synchronisation, pour chaque fichier :

> Si le contenu est de l'UTF-8 valide (`utf8.Valid`), le prendre tel quel.
> Sinon, le décoder en **Windows-1252** (sur-ensemble de Latin-1, tolère les guillemets typographiques).

### P2 — Aucun cache HTTP amont → T-05, T-11

Ni `ETag`, ni `Last-Modified`. Un `If-Modified-Since` daté de 2030 renvoie `200` et 3 168 771
octets. Seul un **SHA-256 du contenu** permet de détecter un changement.

### P3 — `FT` au lieu de `ST` → T-07

26 911 occurrences de `SA`, 5 509 de `FT`, **zéro** de `ST`. Un parseur écrit d'après la seule
documentation officielle rejetterait 5 509 lignes, soit 17 % du fichier de composition.

### P4 — Valeurs non normalisées → T-08

| Champ | Formes observées |
|---|---|
| Taux de remboursement | `65%` (9 125) **et** `65 %` (1 753) ; `100%` (863) **et** `100 %` (378) |
| Prix | virgule décimale : `24,34` — `strconv.ParseFloat` échoue tel quel |
| Prix ≥ 1 000 € | **la virgule sert aussi de séparateur de milliers** : `1,003,48` vaut 1 003,48 € |
| Titulaire | espaces de tête : `"  PHARMA DEVELOPPEMENT"` |
| Lien MITM | `http://` et ancien chemin `extrait.php?specid=` |

**Le double rôle de la virgule dans les prix mérite un développement**, car c'est le seul piège de
cette liste qui produit une perte de données silencieuse plutôt qu'une erreur visible.

Distribution mesurée du champ « prix du médicament » de `CIS_CIP_bdpm.txt` (20 903 lignes) :

| Forme | Occurrences |
|---|---:|
| `9,99` | 7 358 |
| *(vide)* | 7 276 |
| `99,99` | 4 405 |
| `999,99` | 1 376 |
| `9,999,99` | 439 |
| `99,999,99` | 49 |

Les 488 dernières portent **deux virgules** : la première sépare les milliers, la seconde les
décimales. Deux vérifications le confirment sans ambiguïté :

1. Aucun prix à une seule virgule ne dépasse **999,20 €**. Le format à deux virgules n'apparaît
   qu'au-delà de 1 000 €, jamais en deçà.
2. L'identité `prix public = prix du médicament + honoraires de dispensation` se vérifie
   arithmétiquement sous cette lecture : `1,003,48 + 1,02 = 1,004,50`. Elle tient sur
   **13 624 des 13 627 présentations** qui portent les trois montants, soit 99,98 %. Les
   3 exceptions présentent un écart de quelques centimes sur des prix à cinq chiffres — une
   incohérence de la source, sans rapport avec le décodage.

La règle de normalisation de [09](09-pipeline-mise-a-jour.md#35-normalisation) —
`strings.ReplaceAll(s, ",", ".")` — est donc **insuffisante** : appliquée à `1,003,48` elle produit
`1.003.48`, que `ParseFloat` rejette. La règle correcte est : **seule la dernière virgule est le
séparateur décimal, les précédentes sont retirées**. Mesuré sur la source réelle, l'écart entre les
deux règles porte sur 977 valeurs de prix réparties sur 489 présentations, soit un taux de rejet de
0,32 % — assez bas pour passer sous le seuil de vraisemblance de 1 % de
[09](09-pipeline-mise-a-jour.md#36-validation) et rester invisible en exploitation.

> Ce format n'est décrit ni par la documentation officielle de l'ANSM, ni par la version initiale
> du présent document. Il a été découvert le 14/08/2026 en confrontant les parseurs de T-08 à la
> source réelle, et non par lecture de la spécification.

### P5 — Libellés de statut incohérents → T-08

Dans `CIS_CIP_Dispo_Spec.txt`, le même code porte des libellés différents :

```
445 × "2  Tension d'approvisionnement"      111 × "4  Remise à disposition"
  2 × "2  Tension dapprovisionnement"         4 × "4  REmise à disposition"
```

Apostrophe perdue, casse erratique. **Le libellé doit être dérivé du code numérique**, jamais
repris du fichier. Le libellé source est conservé dans un champ `libelle_source` pour la traçabilité.

### P6 — Intégrité référentielle imparfaite → T-09

4 codes CIS présents dans `CIS_CIP_bdpm.txt` sont absents de `CIS_bdpm.txt`. Zéro orphelin en
revanche dans `CIS_COMPO_bdpm.txt`.

Ces lignes ne doivent **ni faire échouer l'ingestion, ni disparaître en silence** : elles sont
mises en quarantaine, comptées par fichier et par motif, et exposées sur `/v1/dataset`.

### P7 — Terminaisons de ligne irrégulières → T-07

- `CIS_MITM.txt` **n'a pas de saut de ligne final** : 7 710 `\n` pour 7 711 enregistrements.
  Un lecteur naïf perd le dernier médicament.
- `CIS_CPD_bdpm.txt` contient **6 CR excédentaires** (28 385 CR pour
  28 379 lignes), ce qui coupe des enregistrements en deux. Trois d'entre eux forment des
  **lignes ne contenant qu'un CR** (lignes 27 884, 27 886 et 27 888), les trois autres sont
  bien à l'intérieur de champs.
- **`CIS_CPD_bdpm.txt` compte donc 28 376 enregistrements, pas 28 379.** Le tableau du §2
  rapporte des décomptes de sauts de ligne (`awk 'END{print NR}'`, §7), qui pour ce seul
  fichier diffèrent du nombre d'enregistrements. Un lecteur qui saute les lignes vides — ce
  que T-07 exige — en produit 28 376 ; l'écart est normal et ne doit pas être traité comme
  une perte de données.

> Mesure corrigée le 14/08/2026 après confrontation du lecteur T-07 à la source réelle.
> La rédaction initiale annonçait des lignes vides « en fin de fichier » : elles sont en
> réalité au milieu, et leur nombre exact n'avait pas été relevé. Le test `TestLive_ScannerRecordCounts`
> (`go test -tags live ./internal/bdpm/`) verrouille désormais les dix décomptes.

### P8 — Guillemets nus : `encoding/csv` échoue → T-07

Quatre fichiers contiennent des guillemets `"` au milieu de champs non délimités :
`CIS_HAS_SMR_bdpm.txt` (164), `CIS_CIP_bdpm.txt` (98), `CIS_CPD_bdpm.txt` (32), `CIS_bdpm.txt` (4).

Test réel avec `encoding/csv` (`Comma='\t'`, `FieldsPerRecord=-1`) :

```
LazyQuotes=false → ERREUR après 535 lignes :
                   parse error on line 536, column 336: bare " in non-quoted-field
LazyQuotes=true  → OK, 20 903 lignes
```

**Décision : ne pas utiliser `encoding/csv` du tout.** La documentation officielle est formelle —
*« Il n'y a pas de délimiteurs de champs »* — donc le guillemet est un caractère ordinaire, sans
sémantique. Le parseur correct est aussi le plus simple et le plus rapide :

```go
sc := bufio.NewScanner(r)
sc.Buffer(make([]byte, 1<<20), 1<<20)   // lignes SMR longues
for sc.Scan() {
    line := strings.TrimRight(sc.Text(), "\r")
    if line == "" { continue }           // P7 : lignes vides
    fields := strings.Split(line, "\t")
    // ...
}
```

Une comparaison exhaustive `encoding/csv`+LazyQuotes vs découpage brut a été exécutée sur les
trois fichiers concernés : **résultats identiques sur `CIS_CIP_bdpm.txt` (20 903 lignes) et
`CIS_HAS_SMR_bdpm.txt` (15 432 lignes)**, et **6 lignes divergentes sur `CIS_CPD_bdpm.txt`**, où
`encoding/csv` laisse traîner des `\r` en fin de champ. Le découpage brut est donc au moins aussi
correct, sans dépendre d'un mode de tolérance.

> La taille de buffer du scanner doit être augmentée : le défaut de `bufio.Scanner` est de
> 64 Kio, insuffisant en cas de ligne SMR anormalement longue.

---

## 6. Conséquences sur la conception

| Constat | Conséquence |
|---|---|
| 22 Mo, lecture seule, immuable entre deux syncs | Index intégral en RAM, pas de base de données ([ADR 0002](adr/0002-memoire-plutot-que-sqlite.md)) |
| Pas de cache HTTP amont | Détection de changement par SHA-256 ([09](09-pipeline-mise-a-jour.md)) |
| Encodage et fins de ligne mouvants | Détection par fichier à chaque sync, jamais en dur |
| Pas de délimiteur de champ | `bufio.Scanner` + `strings.Split`, pas `encoding/csv` |
| Données sales et énumérations instables | Normalisation à l'ingestion, libellés dérivés des codes |
| Intégrité référentielle imparfaite | Quarantaine comptée et exposée, jamais d'échec silencieux |
| Dénominations accentuées en majuscules | Recherche insensible aux accents et à la casse ([05](05-conception-recherche.md)) |
| 367 formes, 68 voies, 666 titulaires | Interning : `uint16` au lieu de `string` ([03](03-modele-de-donnees.md)) |
| Deux formats de date coexistants | Deux parseurs de date selon le fichier |

---

## 7. Méthode de mesure

Mesures réalisées le 14/08/2026 par téléchargement direct puis analyse en ligne de commande.
Les commandes ci-dessous les reproduisent à l'identique.

```bash
BASE=https://base-donnees-publique.medicaments.gouv.fr/download/file
FILES="CIS_bdpm CIS_CIP_bdpm CIS_COMPO_bdpm CIS_HAS_SMR_bdpm CIS_HAS_ASMR_bdpm \
       HAS_LiensPageCT_bdpm CIS_GENER_bdpm CIS_CPD_bdpm CIS_CIP_Dispo_Spec CIS_MITM"

for f in $FILES; do curl -s "$BASE/$f.txt" -o "$f.txt"; done

# Volumétrie et nombre de colonnes
for f in *.txt; do echo -n "$f "; awk -F'\t' 'END{print NR}' "$f"; \
  awk -F'\t' '{print NF}' "$f" | sort -n | uniq -c; done

# Encodage (UTF-8 valide ou non)
for f in *.txt; do iconv -f UTF-8 -t UTF-8 "$f" >/dev/null 2>&1 \
  && echo "$f UTF-8" || echo "$f Latin-1"; done

# Fins de ligne et saut de ligne final
for f in *.txt; do echo "$f CR=$(tr -dc '\r' < "$f" | wc -c) \
  wc=$(wc -l < "$f") awk=$(awk 'END{print NR}' "$f")"; done

# Guillemets nus
for f in *.txt; do echo "$f quotes=$(tr -dc '"' < "$f" | wc -c)"; done

# Intégrité référentielle
cut -f1 CIS_bdpm.txt | sort -u > /tmp/ref
cut -f1 CIS_CIP_bdpm.txt | sort -u | comm -13 /tmp/ref - | wc -l
```

Ces vérifications sont reprises telles quelles dans la tâche **T-12** sous forme de test
automatisé, de sorte qu'une évolution du format côté ANSM soit détectée immédiatement.

## 8. Références

- Page de téléchargement : <https://base-donnees-publique.medicaments.gouv.fr/telechargement>
- *Contenu et format des fichiers téléchargeables dans la BDM*, v4, mise à jour du 27/08/2025 :
  <https://base-donnees-publique.medicaments.gouv.fr/download/file/Contenu_et_format_des_fichiers_telechargeables_dans_la_BDM_v4.pdf>
- Licence Ouverte / Open Licence (Etalab) — réutilisation libre avec mention de la source et de
  la date de mise à jour.
