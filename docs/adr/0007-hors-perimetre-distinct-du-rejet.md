# ADR 0007 — Le « hors périmètre » est distinct du rejet

- **Statut** : acceptée
- **Date** : 15/08/2026
- **Décideurs** : chef de projet, Backend
- **Corrige** : [09 §3.6](../09-pipeline-mise-a-jour.md#36-validation), [12 T-09](../12-backlog-taches.md)

## Contexte

La validation d'ingestion prévoyait une seule catégorie de ligne écartée — la
**quarantaine** — et un garde-fou global : au-delà de 1 % de rejets
(`MEDS_MAX_REJECT_RATIO`), l'ingestion échoue, au motif qu'un tel taux signale
un changement de format amont.

Le piège P6 de [01](../01-analyse-source-bdpm.md) documentait **4 lignes
orphelines**, toutes dans `CIS_CIP_bdpm.txt` : des présentations dont le CIS
n'existe pas dans `CIS_bdpm.txt`. Le critère d'acceptation de T-09 reprenait ce
chiffre.

La première exécution du pipeline complet sur la source réelle, le 15/08/2026,
a mesuré **8 831 lignes orphelines**, soit 5,79 % du jeu — six fois le seuil.
L'ingestion échouait donc sur des données parfaitement saines.

| Fichier | Lignes orphelines |
|---|---:|
| `CIS_HAS_SMR_bdpm.txt` | 4 185 |
| `CIS_GENER_bdpm.txt` | 2 513 |
| `CIS_HAS_ASMR_bdpm.txt` | 2 115 |
| `CIS_CIP_Dispo_Spec.txt` | 12 |
| `CIS_CIP_bdpm.txt` | 4 |
| `CIS_MITM.txt` | 2 |
| **Total** | **8 831** |

Le chiffre a été confirmé indépendamment du code Go, par jointure en shell
(`awk 'NR==FNR{a[$1];next} !($1 in a)'`) sur les fichiers téléchargés : 4 185
lignes SMR et 2 513 lignes GENER orphelines, exactement. **P6 n'avait été
mesuré que sur `CIS_CIP_bdpm.txt`** ; le phénomène est en réalité massif dans
les fichiers HAS et génériques.

L'explication est métier et non technique : les avis de la HAS et les groupes
génériques conservent des spécialités **retirées du marché**, que
`CIS_bdpm.txt` ne liste plus. C'est une propriété structurelle et stable de la
source, pas un incident.

## Options considérées

### A. Relever `MEDS_MAX_REJECT_RATIO` au-dessus de 6 %

Rejetée. Le garde-fou existe pour détecter un changement de format amont. Avec
un plancher de bruit à 5,8 %, il ne détecterait plus qu'une catastrophe totale,
et perdrait précisément la sensibilité qui le justifie.

### B. Conserver les lignes orphelines dans le jeu servi

Rejetée. Tous les accès de l'API partent d'un CIS : un avis SMR rattaché à un
CIS absent est **inatteignable**, et un groupe générique dont tous les membres
sont hors référentiel est une coquille vide. Les servir alourdirait le Store et
les réponses sans qu'aucun appelant puisse les atteindre.

### C. Séparer deux catégories

Le **rejet** — la ligne est malformée — et le **hors périmètre** — la ligne est
bien formée mais réfère une entité absente du référentiel courant.

## Décision

**Retenir l'option C.** Deux compteurs distincts, deux sémantiques distinctes :

| | Rejet | Hors périmètre |
|---|---|---|
| Cause | Ligne malformée : colonnes, date, prix illisibles | Référent absent de `CIS_bdpm.txt` |
| Mesure 15/08/2026 | **0** | **8 831** (5,79 %) |
| Compté dans `MEDS_MAX_REJECT_RATIO` | oui | **non** |
| Garde-fou propre | `reject_ratio` ≤ 1 % | `out_of_scope.ratio` ≤ 20 % |
| Exposé sur `/v1/dataset` | `quarantine` | `out_of_scope` |

Le garde-fou de vraisemblance n'est pas supprimé mais **dédoublé** : une
envolée du taux hors périmètre au-delà de 20 % signalerait que `CIS_bdpm.txt` a
été tronqué en amont — exactement la panne que le taux de rejet ne peut plus
détecter depuis la séparation. À 5,79 % mesurés, la marge est confortable.

Les doublons de clé primaire (`cis_duplique`, `cip13_duplique`) restent, eux,
de véritables rejets : ce sont des collisions d'index, dont la conséquence
serait une donnée silencieusement perdue.

### Cas particulier des ruptures

Une rupture dont le **CIP13** est inconnu n'est pas écartée : son CIP13 est
neutralisé et la fiche reste rattachée à sa spécialité. Perdre l'information
« ce médicament est en rupture » parce que le conditionnement exact est
introuvable serait un mauvais arbitrage sur une donnée de santé.

## Conséquences

### Positives

- Le garde-fou de rejet retrouve sa sensibilité : **0 rejet mesuré**, tout
  écart deviendra immédiatement visible.
- Un second garde-fou couvre la panne que le premier ne voit plus.
- `/v1/dataset` expose les deux compteurs séparément : un intégrateur constate
  que 8 831 lignes concernent des spécialités retirées du marché, sans y lire
  un défaut de qualité de l'API.
- L'ingestion réussit sur des données saines — ce qui n'était pas le cas avant.

### Négatives — assumées

- **Une notion de plus** à expliquer, dans le manifest, sur `/v1/dataset` et
  dans la documentation d'exploitation.
- Le détail des lignes hors périmètre n'est **pas** conservé, seulement les
  compteurs par fichier et par motif : à 8 831 lignes par ingestion, un
  échantillon exhaustif gonflerait le manifest sans rien apprendre. Un audit
  ligne à ligne exige de rejouer l'ingestion.
- Le seuil de 20 % est empirique, calé sur une seule mesure. Il devra être
  réévalué après quelques semaines d'exploitation, la stabilité du taux étant
  supposée et non encore démontrée.

### Ce qui invaliderait cette décision

Si l'ANSM se mettait à publier des fichiers HAS strictement alignés sur
`CIS_bdpm.txt`, le taux hors périmètre tomberait à zéro et la distinction
perdrait son objet — sans nuire, mais sans utilité non plus.

À l'inverse, si le taux devenait très instable d'une ingestion à l'autre, le
garde-fou à seuil fixe serait inadapté et devrait céder la place à une
comparaison au snapshot précédent, comme pour les compteurs d'entités.
