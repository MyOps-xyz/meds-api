# ADR 0002 — Index en mémoire plutôt que SQLite

- **Statut** : acceptée
- **Date** : 14/08/2026
- **Décideurs** : chef de projet, équipe backend

## Contexte

L'énoncé du projet écartait explicitement les bases relationnelles serveur (MySQL, PostgreSQL) et
laissait le choix entre un chargement intégral en RAM, SQLite, ou une autre solution.

La mesure a tranché la question avant même la discussion technique
([01](../01-analyse-source-bdpm.md#2-inventaire-des-fichiers)) :

| Grandeur | Valeur mesurée |
|---|---:|
| Taille totale des 10 fichiers | **22,3 Mo** |
| Spécialités | 15 857 |
| Présentations | 20 903 |
| Lignes toutes entités confondues | ~152 500 |

Trois caractéristiques du jeu de données comptent autant que sa taille :

1. **Lecture seule** — aucune écriture ne vient de l'API.
2. **Immuable entre deux synchronisations** — le contenu ne change qu'une fois par jour, en bloc.
3. **Requêtes connues à l'avance** — 17 endpoints, tous exprimables par un accès indexé.

## Options considérées

### SQLite (en fichier ou en mémoire)

- Requêtes SQL flexibles, FTS5 pour la recherche plein-texte, index B-tree.
- Format de persistance éprouvé, outillage de diagnostic abondant.
- Coût par requête : ~10 à 50 µs, incluant préparation de la requête, parcours de B-tree,
  désérialisation des lignes et conversion de types. En base `:memory:`, on économise les
  syscalls mais pas le reste.
- Impose `CGO_ENABLED=1` avec `mattn/go-sqlite3` — ce qui **interdirait l'image `distroless/static`**
  et le binaire statique ([08](../08-deploiement-docker.md)). Les portages purement Go
  (`modernc.org/sqlite`) évitent CGO mais sont sensiblement plus lents.
- Le rechargement à chaud demanderait de basculer de fichier et de rouvrir les connexions, avec
  une fenêtre d'incohérence à gérer.

### Structures Go en mémoire

- Coût par lookup : ~50 ns pour un accès de map, contre 10 à 50 µs en SQLite — **deux à trois
  ordres de grandeur**.
- Contrôle total du placement mémoire : structure de tableaux, internement, index CSR
  ([03](../03-modele-de-donnees.md#4-représentation-en-mémoire-le-store)).
- Rechargement à chaud par simple bascule de pointeur, sans fenêtre d'incohérence.
- En contrepartie : chaque accès doit être prévu à la construction ; il n'y a pas de requête
  ad hoc.

### Base embarquée clé-valeur (BoltDB, Badger, Pebble)

Écartée d'emblée : elles apportent la durabilité et les transactions, dont on n'a aucun besoin sur
une donnée en lecture seule reconstructible en 30 secondes, tout en restant plus lentes qu'un
accès mémoire direct.

## Décision

**Index intégralement en mémoire**, construit au démarrage depuis un snapshot NDJSON
([ADR 0003](0003-snapshot-ndjson-et-hot-swap-atomique.md)), sans aucune base de données.

Le raisonnement tient en une phrase : **SQLite résout des problèmes que ce projet n'a pas** —
persistance transactionnelle, jeu de données dépassant la RAM, requêtes imprévisibles, écritures
concurrentes — tout en imposant un coût par requête et une contrainte de compilation bien réels.

Une base de données est un excellent choix quand la donnée ne tient pas en mémoire, ou qu'on ne
sait pas à l'avance comment on l'interrogera. Ici, ni l'un ni l'autre.

## Conséquences

### Positives

- Lookups en ~50 ns, laissant deux ordres de grandeur de marge sur les budgets
  ([07](../07-performance.md#1-doù-viennent-les-budgets)).
- `CGO_ENABLED=0` préservé : binaire statique, image `distroless/static` de 20 Mio, aucune
  bibliothèque système à corriger.
- Rechargement à chaud trivial et sans fenêtre d'incohérence.
- Une dépendance en moins, un format de fichier en moins, un mode de panne en moins.
- Pas de fichier de base à sauvegarder : le volume est reconstructible depuis l'ANSM.

### Négatives — assumées

- **Aucune requête ad hoc.** Un besoin analytique non prévu (« combien de génériques par
  titulaire ? ») demande du code, là où SQL suffirait. Atténuation : le snapshot NDJSON reste
  interrogeable hors ligne avec `jq` ou `duckdb`, sans toucher au service.
- **Chaque nouvel axe d'accès coûte un index** à concevoir, construire et faire tenir en mémoire.
- **Empreinte proportionnelle à la donnée.** À 22 Mo c'est indolore ; à 2 Go, la décision
  s'inverserait.
- **Temps de démarrage non nul** (~300 ms de construction), là où SQLite ouvrirait un fichier
  instantanément. Sans effet : le budget de démarrage est de 1 s.
- **Diagnostic moins immédiat** — pas de `sqlite3` pour explorer. Compensé par le snapshot NDJSON,
  lisible et diffable.

### Ce qui invaliderait cette décision

- Le jeu de données dépassant ~1 Go (soit 45 fois sa taille actuelle).
- Un besoin de requêtes réellement arbitraires exposé aux utilisateurs.
- Un passage en écriture, avec des exigences transactionnelles.

Aucune de ces évolutions n'est prévisible : la BDPM croît de quelques centaines de spécialités par
an, et le périmètre exclut l'écriture ([00](../00-vision-et-perimetre.md#4-non-objectifs-v1)).
