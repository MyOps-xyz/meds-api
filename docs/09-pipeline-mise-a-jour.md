# 09 — Pipeline de mise à jour

## 1. Exigences

| # | Exigence | Origine |
|---|---|---|
| E1 | Détecter un changement sans `ETag` ni `Last-Modified` | Piège P2 — [01](01-analyse-source-bdpm.md#p2--aucun-cache-http-amont--t-05-t-11) |
| E2 | Recharger **sans aucune interruption de service** | Objectif O3 |
| E3 | Ne jamais servir un jeu de données partiel ou incohérent | Principe d'intégrité — [00](00-vision-et-perimetre.md#2-vision) |
| E4 | Rester courtois envers l'ANSM | [00](00-vision-et-perimetre.md#7-contraintes-réglementaires-et-licence) |
| E5 | Rester résilient à une indisponibilité de la source | Objectif de disponibilité |
| E6 | Tracer ce qui a été ingéré, rejeté et pourquoi | Exactitude, auditabilité |

---

## 2. Vue d'ensemble

```
   déclencheur (cron interne | POST /admin/sync | CLI bdpm-sync)
        │
        ├─ verrou acquis ? ── non ──► 409, abandon
        │ oui
        ▼
 ① Téléchargement des 10 fichiers en parallèle
        │
        ▼
 ② SHA-256 par fichier → hash global
        │
   inchangé ? ── oui ──► fin (aucune reconstruction), métrique "unchanged"
        │ non
        ▼
 ③ Détection d'encodage (par fichier)
        │
        ▼
 ④ Parsing TSV → ⑤ Normalisation → ⑥ Validation
        │
   taux de rejet > seuil ? ── oui ──► ÉCHEC, ancien Store conservé, alerte
        │ non
        ▼
 ⑦ Écriture du snapshot (tmp + os.Rename atomique) + manifest.json
        │
        ▼
 ⑧ Construction du nouveau Store (~300 ms, en arrière-plan)
        │
        ▼
 ⑨ storePtr.Store(nouveau)  ── bascule atomique, 0 ms d'indisponibilité
        │
        ▼
 ⑩ Purge des snapshots au-delà de MEDS_SNAPSHOT_KEEP
```

**Point clé : rien n'est publié avant que tout soit prêt.** Les étapes ① à ⑧ n'ont aucun effet
observable ; seule l'étape ⑨ modifie ce que voient les clients, et elle est instantanée.

---

## 3. Étapes

### 3.1 Téléchargement

Les dix fichiers sont récupérés **en parallèle** via un `errgroup` : la synchronisation dure le
temps du fichier le plus lent, non leur somme (~30 s au lieu de ~2 min).

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(4)                       // 4 connexions simultanées : parallèle sans être agressif
for _, f := range files {
    g.Go(func() error { return dl.fetch(ctx, f) })
}
if err := g.Wait(); err != nil { return err }   // un seul échec annule tout (E3)
```

Paramètres du client :

| Paramètre | Valeur | Raison |
|---|---|---|
| Délai global | 5 min | Couvre une source lente |
| Délai par fichier | 2 min | 4,5 Mio sur une liaison dégradée |
| Tentatives | 3 | Absorbe une coupure passagère |
| Attente entre tentatives | 2 s, 8 s, 32 s (+ aléa) | Backoff exponentiel avec jitter |
| `User-Agent` | `meds-api/1.0 (+https://…)` | Identification loyale (E4) |
| Taille maximale | 100 Mio par fichier | Borne de sécurité ([06](06-securite.md#8-sécurité-du-pipeline-de-mise-à-jour)) |
| TLS | Vérification stricte | Jamais `InsecureSkipVerify` |

Le téléchargement se fait **en mémoire** : 22 Mio au total, inutile de passer par le disque.

**Une seule erreur annule toute la synchronisation** (E3) : neuf fichiers sur dix produiraient un
jeu de données incohérent, pire qu'un jeu périmé.

### 3.2 Détection de changement

Faute d'`ETag` et de `Last-Modified` exploitables (P2), la seule méthode fiable est le hachage :

```go
h := sha256.Sum256(content)                     // par fichier
global := sha256.Sum256(concat(sortedHashes))   // hash global du dataset
```

Comparé au `hash` du manifest courant. S'il est identique, la synchronisation s'arrête ici :
ni parsing, ni écriture, ni reconstruction. C'est le cas nominal la plupart des jours, puisque
seuls deux ou trois fichiers changent réellement chaque mois.

Coût : 22 Mio téléchargés et hachés, soit ~30 s de réseau et ~50 ms de calcul. Acceptable une
fois par jour.

### 3.3 Détection d'encodage

Par fichier, à chaque synchronisation, **jamais en dur** (P1) :

```go
func decode(b []byte) (string, string) {
    if utf8.Valid(b) {
        return string(b), "utf-8"
    }
    d, _ := charmap.Windows1252.NewDecoder().Bytes(b)
    return string(d), "windows-1252"
}
```

Windows-1252 plutôt qu'ISO-8859-1 : c'en est un sur-ensemble, qui couvre en plus les guillemets
typographiques et le tiret cadratin de la plage 0x80–0x9F, où ISO-8859-1 ne place que des
caractères de contrôle.

L'encodage retenu est **inscrit au manifest** : une bascule de l'ANSM vers l'UTF-8 devient ainsi
visible et traçable, au lieu d'être un changement silencieux.

### 3.4 Parsing

`bufio.Scanner` + `strings.Split`, jamais `encoding/csv` — démonstration en
[01](01-analyse-source-bdpm.md#p8--guillemets-nus--encodingcsv-échoue--t-07).

```go
sc := bufio.NewScanner(strings.NewReader(text))
sc.Buffer(make([]byte, 1<<20), 1<<20)          // 1 Mio : le défaut de 64 Kio est insuffisant
for line := 1; sc.Scan(); line++ {
    row := strings.TrimRight(sc.Text(), "\r")   // P7 : CRLF hétérogènes
    if strings.TrimSpace(row) == "" { continue } // P7 : lignes vides
    fields := strings.Split(row, "\t")
    if len(fields) != expected {
        rep.Quarantine(file, line, "nb_colonnes", row)
        continue
    }
    // …
}
```

`bufio.Scanner` traite correctement le fichier sans saut de ligne final (P7,
`CIS_MITM.txt`) : le dernier enregistrement est bien restitué.

Une ligne mal formée est **mise en quarantaine, jamais fatale** : une ligne perdue est un incident
mineur, un refus d'ingestion pour une ligne est un incident majeur.

### 3.5 Normalisation

| Traitement | Détail | Piège |
|---|---|---|
| Dates `JJ/MM/AAAA` → ISO | `time.Parse("02/01/2006", v)` | — |
| Dates `AAAAMMJJ` → ISO | Format distinct pour SMR et ASMR | — |
| Prix → centimes | **Dernière** virgule → point, virgules précédentes retirées, puis entier | P4 |
| Taux de remboursement | Retrait des espaces et du `%`, découpage sur `;`, entier | P4 |
| Champs multi-valués | Découpage sur `;`, détrimage, retrait des vides | P4 |
| Espaces parasites | `strings.TrimSpace` sur tous les champs texte | P4 |
| Libellés de statut | **Dérivés du code**, source conservée dans `libelle_source` | P5 |
| Nature de composant | `SA` ou `FT` ; `ST` accepté et converti en `FT` | P3 |
| URLs | `http://` → `https://` | P4 |
| Chaîne vide | Convertie en `null` | — |

La conversion de prix mérite un mot, car c'est la plus facile à rater — et elle l'a été : la
rédaction initiale de ce paragraphe remplaçait toutes les virgules par un point, ce qui rejette
les 488 prix supérieurs à 1 000 € où la virgule sert **aussi** de séparateur de milliers
([01](01-analyse-source-bdpm.md#p4--valeurs-non-normalisées--t-08)).

```go
func parsePriceCents(s string) (*int32, error) {
    s = strings.TrimSpace(s)
    if s == "" { return nil, nil }              // absent ≠ zéro
    s = strings.ReplaceAll(s, " ", "")
    if strings.Count(s, ",") > 1 {              // « 1,003,48 » : milliers + décimales
        i := strings.LastIndex(s, ",")
        s = strings.ReplaceAll(s[:i], ",", "") + "." + s[i+1:]
    } else {
        s = strings.ReplaceAll(s, ",", ".")
    }
    f, err := strconv.ParseFloat(s, 64)
    if err != nil { return nil, err }
    c := int32(math.Round(f * 100))             // arrondi, pas troncature
    return &c, nil
}
```

`math.Round` et non une troncature : `24.34 * 100` vaut `2433.9999…` en virgule flottante, une
troncature donnerait `2433` — soit un centime perdu sur une part importante des présentations.

### 3.6 Validation

Trois niveaux :

1. **Structure** — nombre de colonnes, format des codes (CIS à 8 chiffres, CIP13 à 13).
2. **Intégrité référentielle** — tout CIS référencé doit exister dans `CIS_bdpm.txt` (P6).

   Les lignes orphelines sont écartées comme **hors périmètre**, catégorie distincte du rejet et
   exclue du taux surveillé : elles réfèrent des spécialités retirées du marché, ce qui est une
   propriété structurelle de la source et non un défaut de qualité.
   **8 831 lignes mesurées le 15/08/2026** — dont 4 185 dans `CIS_HAS_SMR_bdpm.txt` et 2 513 dans
   `CIS_GENER_bdpm.txt` — là où P6 n'en documentait que 4, faute d'avoir été mesuré ailleurs que
   sur `CIS_CIP_bdpm.txt`. Les compter comme des rejets condamnerait chaque ingestion
   ([ADR 0007](adr/0007-hors-perimetre-distinct-du-rejet.md)).

3. **Vraisemblance globale** — garde-fou contre un changement de format amont :

| Contrôle | Seuil | Motif |
|---|---|---|
| Taux de rejet | ≤ 1 % (`MEDS_MAX_REJECT_RATIO`) | Au-delà, le format a probablement changé. **0 % mesuré** le 15/08/2026 |
| Taux hors périmètre | ≤ 20 % | Une envolée signale un `CIS_bdpm.txt` tronqué en amont. **5,79 % mesuré** |
| Nombre de spécialités | ≥ 10 000 | 15 857 aujourd'hui ; un effondrement est anormal |
| Variation vs snapshot précédent | ≤ ±20 % | Une chute brutale signale un problème amont |
| Fichier vide | interdit | — |

Un dépassement fait **échouer l'ingestion**, sans toucher au `Store` en service (E3). Une alerte
est émise ; l'exploitant tranche.

### 3.7 Écriture du snapshot

```
/data/snapshots/
  ├── 3f2a9c…/                       ← nommé par le hash global
  │     ├── manifest.json
  │     ├── specialites.ndjson
  │     ├── presentations.ndjson
  │     └── … (une entité par fichier)
  ├── 8b1d4f…/
  └── current → 3f2a9c…              ← lien symbolique
```

Écriture atomique, en trois temps :

```go
tmp := filepath.Join(dir, ".tmp-"+hash)
writeAll(tmp)                        // 1. tout écrire dans un répertoire temporaire
os.Rename(tmp, final)                // 2. rename atomique du répertoire
atomicSymlink(final, currentLink)    // 3. bascule du lien symbolique
```

`os.Rename` est atomique sur un même système de fichiers : à aucun instant `current` ne pointe
vers un snapshot incomplet, même en cas de coupure brutale. Les répertoires `.tmp-*` résiduels
sont purgés au démarrage suivant.

Le manifest est écrit **en dernier** : sa présence atteste qu'un snapshot est complet et
exploitable.

### 3.8 Construction et bascule

```go
newStore, err := store.Build(snapshot)   // ~300 ms, hors chemin de requête
if err != nil {
    metrics.SyncFailed.Inc()
    return err                            // l'ancien Store reste en service
}
s.ptr.Store(newStore)                     // ⑨ bascule instantanée
```

C'est le cœur de l'exigence E2. La bascule est une **écriture de pointeur** : elle ne peut ni
échouer, ni bloquer, ni être observée à moitié. Les requêtes en cours terminent sur l'ancien
`Store`, que le ramasse-miettes libère ensuite.

Pointe mémoire transitoire : deux `Store` coexistent quelques centaines de millisecondes, d'où le
budget RSS de 250 Mio pendant un rechargement ([07](07-performance.md#22-débit-et-ressources)).

### 3.9 Purge

Les `MEDS_SNAPSHOT_KEEP` snapshots les plus récents sont conservés (défaut : 3). Les précédents
sont supprimés. Conserver plusieurs versions permet un retour arrière immédiat en cas de donnée
amont défectueuse, pour un coût de ~15 Mio par snapshot (NDJSON, plus compact que la source).

---

## 4. Planification

```bash
MEDS_SYNC_CRON=0 4 * * *      # quotidien à 04:00
MEDS_SYNC_JITTER=30m          # décalage aléatoire dans [0, 30 min]
MEDS_SYNC_ON_START=true       # synchroniser si aucun snapshot n'est présent
```

**Pourquoi 04:00 et pourquoi un jitter.** L'ANSM publie ses mises à jour en journée ; une
synchronisation nocturne trouve donc une donnée stable et évite les heures de pointe. Le jitter
répond à E4 : si l'API est déployée en plusieurs exemplaires, un cron identique ferait converger
toutes les instances sur la même seconde. Le décalage aléatoire les étale.

Le déclenchement est aussi possible par `POST /admin/sync` (asynchrone, `202`, `409` si déjà en
cours) et par la CLI `bdpm-sync`, utilisée pour l'amorçage, les crons externes et les tests.

Un **verrou en mémoire** garantit qu'une seule synchronisation s'exécute à la fois, quelle que
soit la source du déclenchement.

---

## 5. Comportement face aux pannes

| Situation | Comportement | Service |
|---|---|---|
| ANSM injoignable | 3 tentatives avec backoff, puis abandon jusqu'au prochain cron | **Intact**, donnée périmée |
| Un fichier en erreur | Synchronisation annulée en bloc | **Intact** |
| Format amont modifié (rejets > 1 %) | Ingestion rejetée, alerte | **Intact** |
| Disque plein | Écriture temporaire en échec, pas de bascule | **Intact** |
| Panne pendant l'écriture | Répertoire `.tmp-*` orphelin, purgé au démarrage | **Intact** |
| Construction du `Store` en échec | Ancien `Store` conservé | **Intact** |
| Aucun snapshot au démarrage | `/readyz` en `503`, endpoints en `503` | **Indisponible** — seul cas |

**Une seule situation rend le service indisponible : n'avoir jamais réussi une ingestion.** Toutes
les autres dégradent la fraîcheur, jamais la disponibilité.

L'âge du dataset est exposé (`/v1/dataset`, métrique `meds_dataset_age_seconds`) : c'est à la
supervision de décider qu'une donnée trop ancienne mérite une alerte (seuil recommandé : 72 h,
soit trois cycles manqués).

---

## 6. Rapport d'ingestion

Émis à chaque synchronisation, en log structuré et dans le manifest, pour satisfaire E6.

```json
{
  "level": "info", "msg": "sync completed",
  "sync_id": "01J8Z9K2M3N4P5Q6R7S8T9V0W1",
  "duration_ms": 32411, "changed": true,
  "hash": "3f2a9c…", "previous_hash": "8b1d4f…",
  "files": [
    { "name": "CIS_bdpm.txt", "bytes": 3168771, "lines": 15857,
      "encoding": "windows-1252", "changed": false }
  ],
  "counts": { "specialites": 15857, "presentations": 20903 },
  "delta": { "specialites": 12, "presentations": -3 },
  "quarantine": { "total": 4, "reject_ratio": 0.000026,
                  "by_reason": { "cis_orphelin": 4 } },
  "store_build_ms": 287
}
```

Le `delta` par rapport au snapshot précédent est la donnée la plus utile en exploitation : elle
répond d'un coup d'œil à « qu'est-ce qui a changé cette nuit ? ». Une variation aberrante est
aussi ce qui déclenche le garde-fou de vraisemblance (§3.6).

---

## 7. Vérification

Couvert par les tâches T-12, T-42 et T-54.

1. **Détection de changement** — deux synchronisations consécutives sur une source inchangée :
   la seconde s'arrête à l'étape ②, sans écriture ni reconstruction.
2. **Bascule sans coupure** — 200 utilisateurs virtuels pendant 5 min, `POST /admin/sync` à
   t+2 min : **zéro requête en erreur**, p99 dégradé de moins de 20 % pendant le swap.
3. **Résilience** — source simulée renvoyant `500`, puis un fichier tronqué, puis un fichier au
   format modifié : dans les trois cas, l'ancien `Store` reste servi et une alerte est émise.
4. **Atomicité** — interruption du processus pendant l'écriture : au redémarrage, `current` pointe
   toujours vers le snapshot précédent, valide, et le `.tmp-*` est purgé.
5. **Quarantaine** — jeu de test contenant des lignes malformées : elles sont comptées, motivées,
   exposées sur `/v1/dataset`, et n'empêchent pas l'ingestion.
6. **Golden files** — les échantillons figés des dix fichiers produisent un snapshot identique
   octet pour octet entre deux exécutions (ingestion déterministe).
