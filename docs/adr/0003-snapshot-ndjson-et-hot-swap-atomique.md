# ADR 0003 — Snapshot NDJSON et bascule atomique

- **Statut** : acceptée
- **Date** : 14/08/2026
- **Décideurs** : chef de projet, équipe backend, DevOps

## Contexte

L'index vit en mémoire ([ADR 0002](0002-memoire-plutot-que-sqlite.md)), mais il doit être
reconstruit à chaque démarrage. Deux questions en découlent :

1. **Depuis quoi reconstruire ?** Retélécharger 22 Mo depuis l'ANSM à chaque redémarrage serait
   lent (~30 s), fragile (dépendance à un service tiers pour démarrer) et discourtois
   ([00](../00-vision-et-perimetre.md#7-contraintes-réglementaires-et-licence)).
2. **Comment remplacer l'index en service** sans interrompre les requêtes (objectif O3) ?

Contraintes supplémentaires : le pipeline doit être auditable (exigence E6 de
[09](../09-pipeline-mise-a-jour.md)), et une ingestion défectueuse ne doit jamais remplacer une
donnée saine (E3).

## Options considérées pour la persistance

### A. Conserver les fichiers TSV bruts

Simple, mais reporte tout le coût de parsing et de normalisation à chaque démarrage, et surtout
**ne fige pas le résultat de la normalisation** : un changement de parser modifierait
silencieusement la donnée servie, sans qu'aucune trace ne subsiste de l'état précédent.

### B. Sérialisation binaire (`gob`, format maison)

Le plus rapide à recharger (~50 ms au lieu de ~300 ms) et le plus compact. Mais :

- illisible sans outil dédié — impossible d'inspecter un snapshot en production ;
- fragile à l'évolution des structures Go, avec un risque de désérialisation silencieusement
  incorrecte ;
- indiffable, donc inutilisable pour répondre à « qu'est-ce qui a changé cette nuit ? ».

### C. NDJSON, une ligne par enregistrement

- Lisible et inspectable avec `jq`, `grep`, `duckdb`, sans outil spécifique.
- Diffable ligne à ligne — c'est ce qui rend possible l'évolution « diff entre versions »
  identifiée en [00](../00-vision-et-perimetre.md#6-évolutions-identifiées-post-v1).
- Streamable : pas besoin de charger 22 Mo en mémoire pour en parser une partie.
- Indépendant des structures Go internes : le format de persistance ne casse pas quand le `Store`
  évolue.
- Coût : ~300 ms de reconstruction contre ~50 ms en binaire.

## Décision

**Snapshot NDJSON** (un fichier par entité + `manifest.json`), écrit atomiquement, et
**bascule par `atomic.Pointer[Store]`**.

### Persistance

```
/data/snapshots/<hash>/
    manifest.json          ← écrit en dernier : atteste la complétude
    specialites.ndjson
    presentations.ndjson
    …
/data/snapshots/current → <hash>
```

Écriture en trois temps : répertoire temporaire, `os.Rename` atomique, bascule du lien symbolique.
`os.Rename` étant atomique sur un même système de fichiers, `current` ne peut à aucun instant
pointer vers un snapshot incomplet — même en cas de coupure brutale d'alimentation.

Les `MEDS_SNAPSHOT_KEEP` derniers snapshots sont conservés (défaut : 3), ce qui permet un retour
arrière immédiat si l'ANSM publiait une donnée défectueuse.

### Bascule

```go
newStore, err := store.Build(snapshot)   // ~300 ms, hors chemin de requête
if err != nil { return err }             // l'ancien Store reste en service
s.ptr.Store(newStore)                    // bascule instantanée
```

La bascule est une **écriture de pointeur** : elle ne peut ni échouer, ni bloquer, ni être observée
à moitié. Les requêtes en vol terminent sur l'ancien `Store`, que le ramasse-miettes libère
ensuite. Les lecteurs n'ont aucun verrou à acquérir
([02](../02-architecture.md#5-concurrence)).

La correction de ce schéma repose sur une condition unique : **le `Store` est intégralement
immuable après construction**. C'est une propriété testée, pas une convention
([11](../11-strategie-de-tests.md#33-invariants-du-store) — `TestStore_Immutable`).

## Conséquences

### Positives

- **Zéro interruption** lors d'un rechargement, prouvé par le scénario `reload.js` (T-55).
- **Démarrage en moins d'une seconde** sans réseau : le service redémarre même si l'ANSM est
  injoignable.
- **Auditable** : un snapshot s'inspecte avec `jq`, se compare avec `diff`, s'archive.
- **Découplage** : le format de persistance ne dépend pas des structures Go internes, qui peuvent
  évoluer sans migration.
- **Retour arrière immédiat** sur une donnée amont défectueuse.
- Ouvre la voie à l'évolution « diff entre versions » sans travail préparatoire.

### Négatives — assumées

- **~300 ms de reconstruction** contre ~50 ms pour un format binaire. Négligeable devant le budget
  de démarrage de 1 s, et sans effet sur le chemin de requête puisque la construction se fait en
  arrière-plan.
- **Pointe mémoire transitoire** : deux `Store` coexistent quelques centaines de millisecondes
  pendant la bascule, d'où un budget RSS de 250 Mio en rechargement contre 150 en régime établi
  ([07](../07-performance.md#22-débit-et-ressources)).
- **Snapshot plus volumineux** que du binaire (~15 Mio contre ~8). Sans importance à cette échelle.
- **Discipline d'immuabilité à tenir** : trier en place une slice renvoyée par le `Store`
  corromprait l'index partagé. Risque réel, traité par un anti-pattern documenté
  ([07](../07-performance.md#6-anti-patterns-à-surveiller-en-revue-de-code)) et un test dédié.
- **Le scheduler intégré complique le multi-instance** : plusieurs répliques synchronisant
  chacune de leur côté multiplieraient le trafic vers l'ANSM. Atténuation documentée
  ([02](../02-architecture.md#8-mise-à-léchelle-horizontale)) : une instance écrit, les autres
  surveillent le volume.

### Ce qui invaliderait cette décision

Si le temps de reconstruction devenait pénalisant — jeu de données dix fois plus gros, ou
redémarrages très fréquents — un format binaire deviendrait justifié. Le NDJSON pourrait alors
être conservé **en parallèle**, comme format d'audit, le binaire servant de cache de démarrage.
