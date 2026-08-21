# Corpus de fuzzing

Ce répertoire recueille les **entrées ayant fait échouer une cible de
fuzzing**, versionnées pour que la régression correspondante soit rejouée à
chaque exécution de la suite de tests.

Go y écrit automatiquement un fichier lorsqu'une cible échoue, sous
`testdata/fuzz/<NomDeLaCible>/`. Ces fichiers sont alors exécutés comme des
cas de test ordinaires par `go test`, sans le drapeau `-fuzz`.

## État au 15/08/2026

**Vide, et c'est le résultat attendu.** Six cibles ont tourné 45 secondes
chacune sans produire le moindre échec :

| Cible | Exécutions | Ce qu'elle prouve |
|---|---:|---|
| `FuzzParseTousLesFichiers` | 266 689 | Aucune entrée ne fait paniquer les dix parseurs |
| `FuzzDecodeFile` | 2 682 163 | La détection d'encodage produit toujours de l'UTF-8 valide |
| `FuzzScanner` | 1 656 548 | Le découpage termine et respecte le nombre de colonnes annoncé |
| `FuzzDecodeCursor` | 5 677 874 | Un curseur forgé ne produit jamais de décalage négatif |
| `FuzzNormalize` | 554 411 | La normalisation est idempotente et sans espace parasite |
| `FuzzRequeteHTTP` | 2 979 824 | Toute URL reçoit un statut valide, sans fuite d'information interne |

## Paliers du moteur de fuzzing

Le moteur affiche par intermittence des paliers à « 0 exec/sec » de trois à
six secondes. Vérifié : ce n'est **pas** un blocage du code. Sur les 105
entrées du corpus de `FuzzNormalize`, le pire cas mesuré est de **115 µs**, et
aucune entrée construite à la main — UTF-8 invalide, marques combinantes en
série, 100 Kio d'accents — ne dépasse la milliseconde. Prévoir une marge sur
`-fuzztime` en intégration continue plutôt que de chercher une pathologie
inexistante.

Le corpus d'exploration — les entrées « intéressantes » découvertes par le
moteur — reste dans le cache de build local et n'est **pas** versionné : il
est volumineux, spécifique à une version de Go, et ne documente aucune
régression.

## Relancer

```bash
make fuzz              # 60 s par cible
go test -run xxx -fuzz '^FuzzNormalize$' -fuzztime 5m ./internal/api/
```
