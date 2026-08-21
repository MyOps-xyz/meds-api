# ADR 0001 — Go plutôt que Python

- **Statut** : acceptée
- **Date** : 14/08/2026
- **Décideurs** : chef de projet, équipe backend

## Contexte

Le choix était ouvert entre Python et Go. L'API doit servir un jeu de données de 22 Mo
intégralement chargé en mémoire ([01](../01-analyse-source-bdpm.md)), avec des budgets serrés :
p99 < 200 µs sur un lookup, > 20 000 req/s sur 2 vCPU, RSS < 150 Mio, image Docker < 25 Mio
([07](../07-performance.md)).

Trois contraintes pèsent particulièrement :

1. **L'index reste en mémoire pendant toute la vie du processus** et doit être lisible par de
   nombreuses requêtes concurrentes, sans verrou.
2. Le **rechargement à chaud** exige de remplacer atomiquement une structure volumineuse pendant
   que le service continue de répondre.
3. L'image doit être **petite et sans surface d'attaque** — pas d'interpréteur, pas de
   bibliothèque système.

## Options considérées

### Python 3.13 + FastAPI

- Écosystème riche, développement rapide, typage progressif avec Pydantic v2 (validation en Rust).
- Python 3.13 propose un mode expérimental sans GIL ; il reste marqué expérimental et souffre
  encore d'une pénalité en mono-thread.
- Empreinte : ~250 à 400 Mio de RSS pour le même index. Chaque objet Python porte un en-tête et un
  compteur de références ; 15 857 objets `Specialite` avec leurs attributs coûtent un ordre de
  grandeur de plus qu'en Go.
- Image : ~150 Mio, avec un interpréteur et des bibliothèques système à maintenir.
- Le rechargement à chaud est réalisable (échange de référence, atomique sous GIL), mais le
  ramasse-miettes doit alors libérer plusieurs centaines de milliers d'objets — pause perceptible.

### Go 1.26.6 + bibliothèque standard

- Binaire statique, image `distroless/static` de ~20 Mio.
- Concurrence native sans GIL ; `atomic.Pointer` rend le rechangement à chaud trivial et sûr.
- Structures compactes : contrôle du placement mémoire, structure de tableaux, internement
  ([03](../03-modele-de-donnees.md#4-représentation-en-mémoire-le-store)).
- Bibliothèque standard suffisante : routage, JSON, logs, crypto — deux dépendances externes en
  tout ([02](../02-architecture.md#33-dépendances-externes)).
- Développement plus verbeux ; pas de typage générique aussi expressif que Pydantic pour la
  validation déclarative.

## Décision

**Go 1.26.6**, avec la bibliothèque standard pour l'essentiel.

Le facteur déterminant n'est pas la vitesse brute — Python aurait probablement tenu une charge
réaliste. C'est la **combinaison** de trois propriétés que Go offre sans effort et Python au prix
de contorsions :

1. **Lecture concurrente sans verrou** sur une structure immuable, avec une mise à l'échelle
   linéaire sur les cœurs.
2. **Bascule atomique de pointeur** pour le rechargement sans coupure — une ligne de code, une
   garantie du modèle mémoire, plutôt qu'un raisonnement sur le GIL.
3. **Empreinte réduite** : 150 Mio contre 400, et une image 7 fois plus petite, ce qui autorise
   `distroless` et donc une surface d'attaque quasi nulle ([06](../06-securite.md#7-sécurité-du-conteneur)).

## Conséquences

### Positives

- Budgets de performance atteignables avec un ordre de grandeur de marge.
- Déploiement simple : un binaire statique, aucune dépendance système.
- Surface d'attaque minimale : ni interpréteur, ni shell, ni gestionnaire de paquets.
- Deux dépendances externes seulement, donc peu de maintenance de sécurité.
- `go test -race` donne une vérification fiable de l'exactitude concurrente.

### Négatives — assumées

- **Développement plus verbeux.** La validation des paramètres, déclarative avec Pydantic, doit
  être écrite à la main (T-32). Coût estimé : 2 points.
- **Pas de génération automatique de l'OpenAPI depuis les types.** FastAPI la produit gratuitement ;
  ici le contrat est écrit à la main et validé par test (T-33). C'est aussi un avantage : le
  contrat devient une décision explicite plutôt qu'un reflet de l'implémentation.
- **Écosystème santé moins fourni** en Python (pandas, bibliothèques de terminologies médicales).
  Sans effet ici, le périmètre excluant tout traitement analytique
  ([00](../00-vision-et-perimetre.md#4-non-objectifs-v1)).
- **Vivier de développeurs plus restreint** que Python dans le domaine santé.

### Ce qui invaliderait cette décision

Si le périmètre évoluait vers de l'analyse de données, de l'apprentissage automatique ou une
intégration avec des bibliothèques scientifiques, Python redeviendrait le bon choix. La frontière
nette entre ingestion, stockage et API ([02](../02-architecture.md#32-arborescence-du-code))
permettrait alors de ne réécrire qu'une partie.
