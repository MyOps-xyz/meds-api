# 07 — Performance

## 1. D'où viennent les budgets

Les cibles de ce document ne sont pas des ambitions décoratives : elles sont **dérivées de la
taille réelle du jeu de données**, mesurée en [01](01-analyse-source-bdpm.md).

Le raisonnement de départ tient en trois lignes :

- 22 Mo de données, 15 857 spécialités, 20 903 présentations.
- L'index complet, recherche comprise, occupe ~63 Mo de tas — il tient en RAM avec une marge
  considérable, et sa partie chaude tient dans le cache L3 d'un processeur serveur.
- Aucune entrée/sortie, aucun verrou, aucun appel réseau sur le chemin de lecture.

Dans ces conditions, une réponse ne coûte structurellement que trois choses : un accès à une table
de hachage, une sérialisation JSON, et l'écriture sur la socket. **La sérialisation JSON domine
largement le reste** — c'est le seul poste qui mérite d'être optimisé.

Un budget de 200 µs au p99 pour un lookup est donc généreux, pas ambitieux : il laisse deux ordres
de grandeur au-dessus du coût réel de l'accès à la donnée.

---

## 2. Budgets

Mesurés sur une machine de référence : **2 vCPU, 512 Mio de RAM**, conteneur Docker, charge
générée depuis un hôte distinct pour ne pas fausser la mesure.

### 2.1 Latence (p99, hors réseau)

| Opération | Budget | Coût attendu | **Mesuré (p99)** | Justification |
|---|---:|---:|---:|---|
| `GET /v1/medicaments/{cis}` | 200 µs | ~30 µs | **18,9 µs** | Un accès de map + sérialisation |
| `GET /v1/presentations/{cip}` | 200 µs | ~35 µs | **17,4 µs** | Idem, avec le parent inclus |
| `GET /v1/medicaments/{cis}?include=all` | 1 ms | ~300 µs | **31,5 µs** | Jusqu'à 37 présentations et 40 avis |
| `GET /v1/medicaments?q=…` | 3 ms | ~600 µs | **71,3 µs** | Intersection de postings + tri + sérialisation |
| Recherche avec repli trigramme | 3 ms | ~2 ms | **387,4 µs** | Chemin dégradé, < 5 % des requêtes |
| `GET /v1/suggest?q=…` | 1 ms | ~150 µs | **55,8 µs** | Dichotomie + balayage borné |
| `GET /v1/dataset` | 200 µs | ~50 µs | **25,6 µs** | Réponse pré-calculée |
| `304 Not Modified` | 50 µs | ~10 µs | **21,3 µs** | Comparaison d'`ETag`, aucune sérialisation |

**Mesure du 15/08/2026** — `make bench-budgets` : jeu de données réel de l'ANSM, `GOMAXPROCS=2`
pour approcher la machine de référence, 20 000 requêtes par opération après 2 000 d'échauffement,
appel direct au `http.Handler` par `httptest`. **Les huit budgets sont tenus**, avec des marges de
6 à 42 fois selon l'opération.

L'appel direct est ce que mesure cette section — l'intitulé dit « hors réseau ». Un générateur de
charge externe ne conviendrait pas : sur le poste de mesure, k6 rapporte un p99 de ~207 ms
**identique sur toutes les routes**, y compris les plus légères. Une latence uniforme sur des
opérations dont le coût varie d'un facteur vingt ne mesure pas le serveur mais la pile réseau de
la machine virtuelle. k6 reste l'outil du débit et de la tenue en charge
([tests/load](../tests/load/README.md)) ; il n'est pas celui du budget de traitement.

**Le budget du `304` a d'abord été dépassé** — 61,5 µs mesurés pour 50 µs — et la cause était
réelle : la réponse était intégralement sérialisée *avant* la comparaison d'`ETag`, puis jetée. Un
`304` coûtait donc le prix d'un `200`. La négociation de cache a été remontée en tête de
`WriteJSON` et des handlers, ce qui la fait précéder la recherche elle-même : 21,3 µs au p99,
10,0 µs au p50 — soit exactement le coût attendu ci-dessus.

### 2.2 Débit et ressources

| Indicateur | Budget |
|---|---:|
| Débit soutenu (lookups, 2 vCPU) | > 20 000 req/s |
| Débit soutenu (recherches, 2 vCPU) | > 5 000 req/s |
| Taux d'erreur sous charge nominale | 0 % |
| RSS en régime établi | < 150 Mio |
| RSS pendant un rechargement | < 250 Mio |
| Construction du `Store` | < 500 ms |
| Démarrage à froid (snapshot présent) | < 1 s |
| Synchronisation complète | < 60 s |
| Taille de l'image Docker | < 25 Mio |
| Indisponibilité pendant un rechargement | **0 ms** |

---

## 3. Les décisions qui portent la performance

### 3.1 Ne rien faire au moment de la requête

Le gain principal ne vient d'aucune micro-optimisation, mais du fait que **tout le travail est
déplacé au chargement** : parsing, normalisation, indexation, résolution des jointures (les liens
HAS sont fusionnés dans les avis dès l'ingestion). Une requête ne fait que lire et sérialiser.

### 3.2 Lecture sans verrou

`atomic.Pointer[Store]` et immuabilité ([02](02-architecture.md#5-concurrence)) : aucun mutex sur
le chemin de lecture, donc **aucune dégradation quand le nombre de cœurs augmente**. C'est ce qui
permet une mise à l'échelle verticale linéaire, là où un `sync.RWMutex` global s'effondrerait
au-delà de quelques cœurs sous forte lecture.

### 3.3 Localité mémoire

Le format CSR ([03](03-modele-de-donnees.md#42-relations-1-n-en-format-csr)) place les
présentations d'un même médicament côte à côte en mémoire. Lire les 3 présentations d'un DOLIPRANE
touche une ou deux lignes de cache, contre autant de défauts de cache qu'il y a d'éléments avec
une `map[string][]*Presentation`.

À cette échelle, **la localité compte davantage que la complexité algorithmique** : tout est en
O(1) ou O(log n), la différence se joue sur les défauts de cache.

### 3.4 Sérialisation JSON

Poste dominant du temps de réponse. Trois mesures :

```go
var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
```

- Tampons repris dans un `sync.Pool` — évite une allocation et une pression GC par requête.
- Écriture directe dans le `http.ResponseWriter` via `json.Encoder`, sans passer par un
  `[]byte` intermédiaire.
- Vues de sérialisation **distinctes** pour la liste et la fiche : la liste omet composition et
  avis ([04](04-specification-api.md#21-get-v1medicaments--recherche-et-filtres)), ce qui divise
  la charge utile par cinq sur l'endpoint le plus sollicité.

Un générateur de code (`easyjson`, `go-json`) reste en réserve **si et seulement si** le profilage
montre que `encoding/json` est le facteur limitant. On ne l'introduit pas par principe : ce serait
une dépendance et une étape de génération pour un gain hypothétique.

### 3.5 `ETag` et `304`

Le dataset ne changeant qu'une fois par jour, un client qui respecte les en-têtes de cache obtient
un `304` sur la quasi-totalité de ses requêtes répétées — soit ~10 µs et quelques dizaines
d'octets au lieu d'une sérialisation complète. **C'est l'optimisation au meilleur rapport
effort/gain de tout le projet**, et elle est purement déclarative.

### 3.6 Compression

`gzip` au-delà de 1 Kio de corps. En dessous, la compression coûte plus de CPU qu'elle n'économise
de temps de transmission. Niveau 5 : le niveau 9 triple le coût CPU pour ~3 % de taille en moins.

Écueil à éviter : compresser puis calculer l'`ETag` sur le résultat compressé. L'`ETag` doit porter
sur la **représentation logique**, sinon un client changeant d'`Accept-Encoding` invalide son cache
sans raison.

---

## 4. Réglage du ramasse-miettes

Le profil mémoire est atypique : un gros tas essentiellement **statique** (le `Store`), et très
peu d'allocations par requête. Le comportement par défaut du GC Go (`GOGC=100`, déclenchement au
doublement du tas) est mal adapté — il déclencherait des cycles inutiles sur un tas qui ne varie
presque pas.

```bash
GOGC=200          # laisse le tas croître davantage avant un cycle
GOMEMLIMIT=400MiB # plafond souple : force le GC avant l'OOM killer
```

`GOMEMLIMIT` est le paramètre important. Il transforme un dépassement mémoire en pression GC
plutôt qu'en mort du conteneur, ce qui compte particulièrement pendant un rechargement, moment où
deux `Store` coexistent brièvement.

`GOMAXPROCS` doit refléter la **limite CPU du conteneur**, pas le nombre de cœurs de l'hôte : sans
cela, le runtime crée trop de threads et le temps est perdu en changements de contexte. Go 1.25+
lit les limites cgroup automatiquement ; la variable reste réglable explicitement en secours.

---

## 5. Protocole de mesure

Trois niveaux, du plus fin au plus représentatif. Détail des scénarios en
[11](11-strategie-de-tests.md).

### 5.1 Micro-benchmarks Go

```bash
go test -bench=. -benchmem -benchtime=10s -count=10 ./internal/... > new.txt
benchstat old.txt new.txt
```

`benchstat` avec `-count=10` fournit un intervalle de confiance : une variation de 3 % sur une
seule exécution n'est pas un signal. **Toute régression supérieure à 10 % sur un benchmark de
référence bloque la fusion** (T-55).

Benchmarks de référence : `BenchmarkLookupCIS`, `BenchmarkSearchSimple`,
`BenchmarkSearchTrigram`, `BenchmarkSuggest`, `BenchmarkSerializeList`, `BenchmarkBuildStore`.

### 5.2 Test de charge

```bash
k6 run --vus 200 --duration 5m tests/load/mixed.js
```

Répartition représentative d'un usage réel :

| Scénario | Part |
|---|---:|
| Lookup par CIS ou CIP | 50 % |
| Recherche plein-texte | 30 % |
| Autocomplétion | 15 % |
| Sous-ressources et `/v1/dataset` | 5 % |

Critères de réussite : p99 dans les budgets du §2, taux d'erreur nul, RSS stable sur toute la
durée (une dérive continue signale une fuite).

### 5.3 Profilage

`net/http/pprof` monté **uniquement** si `MEDS_PPROF=true`, jamais exposé publiquement.

```bash
go tool pprof -http=:8081 http://localhost:8080/debug/pprof/profile?seconds=30
go tool pprof -http=:8081 http://localhost:8080/debug/pprof/heap
```

### 5.4 Test spécifique : rechargement sous charge

Le plus important des trois, car il valide la promesse centrale de l'architecture.

```
1. Lancer 200 utilisateurs virtuels pendant 5 minutes.
2. Déclencher POST /admin/sync à t+2 min.
3. Vérifier : aucune requête en erreur, p99 dégradé de moins de 20 % pendant le swap,
   RSS revenu à son niveau initial 30 s après.
```

**Zéro erreur est un critère absolu, pas une moyenne** : une seule `503` pendant un rechargement
invaliderait la conception.

---

## 6. Anti-patterns à surveiller en revue de code

Consignés ici parce qu'ils annuleraient les garanties ci-dessus et passent facilement en revue.

| À proscrire | Pourquoi | À la place |
|---|---|---|
| `storePtr.Load()` plusieurs fois dans un handler | La donnée peut changer entre deux appels → réponse incohérente | Charger une fois en variable locale |
| Trier une slice renvoyée par le `Store` | Modifierait l'index partagé — corruption silencieuse | Copier avant de trier |
| `mutex` sur le chemin de lecture | Détruit la mise à l'échelle multi-cœur | Immuabilité + `atomic.Pointer` |
| Concaténer du JSON à la main | Injection et échappement incorrect | `json.Encoder` |
| `fmt.Sprintf` dans une boucle chaude | Allocations et réflexion | `strconv`, `strings.Builder` |
| Construire la réponse entière en `[]byte` | Double le pic mémoire sur les grosses réponses | Écriture en flux |
| Journaliser à chaque requête en `debug` | Les I/O de log deviennent le facteur limitant | `info` en production, échantillonnage |
| Recalculer un `ETag` coûteux | Annulerait le gain du `304` | Hash pré-calculé du dataset + paramètres |

---

## 7. Marge et limites

Les budgets sont fixés avec une marge d'environ **un ordre de grandeur** par rapport au coût
attendu (§2.1). Ce n'est pas de la prudence excessive : la marge absorbe le bruit de mesure, la
variabilité des machines virtuelles mutualisées, et les évolutions futures du jeu de données.

Ce que ces budgets ne couvrent pas, et qui dominera la latence perçue en production :

- **La latence réseau** — quelques millisecondes à quelques dizaines, soit dix à cent fois le
  temps de traitement. En pratique, l'API n'est jamais le facteur limitant.
- **La terminaison TLS** — une poignée de millisecondes pour une nouvelle connexion ; d'où
  l'importance de la réutilisation des connexions et de HTTP/2 côté proxy.

Autrement dit : au-delà de ces budgets, optimiser le code serait du temps perdu. L'effort utile
porterait alors sur la topologie de déploiement (proximité géographique, mise en cache CDN des
réponses publiques), pas sur le programme.
