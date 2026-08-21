# 06 — Sécurité

## 1. Contexte de risque

L'API sert une donnée **publique** sous Licence Ouverte : il n'y a ni secret à protéger, ni donnée
personnelle, ni donnée de santé nominative. Le risque n'est donc pas la confidentialité mais
**la disponibilité, l'intégrité et l'abus de ressource**.

Deux conséquences qui orientent tout ce document :

1. Un attaquant ne cherchera pas à *lire* la donnée — elle est téléchargeable librement — mais à
   *rendre le service indisponible*, ou à s'en servir comme relais.
2. **L'intégrité prime.** Une API de médicaments qui renvoie une donnée fausse est un risque
   sanitaire, pas un incident technique. C'est le seul point où la sécurité rejoint la sûreté.

---

## 2. Modèle de menaces (STRIDE)

| Menace | Scénario | Contre-mesure | Tâche |
|---|---|---|---|
| **Spoofing** | Réutilisation d'une clé API interceptée | TLS obligatoire, clés de 256 bits, révocation par redéploiement | T-34 |
| **Tampering** | Altération du snapshot sur le volume | SHA-256 au manifest, vérifié au chargement ; volume non exposé | T-11 |
| **Tampering** | Empoisonnement de la source amont | TLS avec vérification de certificat, seuil de rejet, refus des ingestions aberrantes | T-09 |
| **Repudiation** | Impossible de tracer un appel | `request_id` sur chaque requête et chaque log | T-38 |
| **Info. disclosure** | Fuite de clé dans les logs | Masquage systématique, jamais de `Authorization` journalisé | T-38 |
| **Info. disclosure** | Trace d'exécution renvoyée au client | `recover` renvoyant un `500` générique, détail côté serveur | T-38 |
| **DoS** | Inondation de requêtes | Rate limiting par clé et par IP, délais serveur | T-35 |
| **DoS** | Requête coûteuse (`q` très long, `limit` énorme) | Bornes strictes sur tous les paramètres | T-39 |
| **DoS** | Épuisement mémoire par corps volumineux | `MaxBytesReader`, `MaxHeaderBytes` | T-36 |
| **DoS indirect** | L'API martèle l'ANSM | Une synchronisation par jour, jitter, verrou anti-concurrence | T-40 |
| **Elevation** | Accès à `/admin/sync` avec une clé ordinaire | Clé d'administration distincte ; endpoint désactivé si absente | T-41 |
| **Elevation** | Évasion du conteneur | Non-root, `cap_drop: ALL`, `no-new-privileges`, rootfs en lecture seule | T-47 |

**Hors périmètre** : le RGPD (aucune donnée personnelle traitée hors logs techniques) et le
chiffrement au repos (donnée publique).

---

## 3. Authentification par clés API

### 3.1 Format et génération

```
mk_live_7f3a9c2e8b1d4f6a0c5e2b8d7a1f4c9e...   (préfixe + 32 octets en base64url)
```

Le préfixe `mk_live_` / `mk_test_` sert à la détection automatique de secret par les scanners de
dépôts (GitHub secret scanning, gitleaks) : une clé accidentellement publiée est repérable.

Génération avec `crypto/rand` uniquement — jamais `math/rand`, même ensemencé.

### 3.2 Stockage : SHA-256, pas Argon2id

Les clés sont stockées **hachées en SHA-256**, comparées en temps constant :

```go
sum := sha256.Sum256([]byte(presented))
for _, known := range knownHashes {
    if subtle.ConstantTimeCompare(sum[:], known[:]) == 1 { return true }
}
```

Ce choix mérite d'être justifié, car il va à l'encontre du réflexe « toujours Argon2id ».

Argon2id, bcrypt et scrypt sont des fonctions **délibérément lentes**, conçues pour un secret à
faible entropie : un mot de passe humain fait 30 à 40 bits d'entropie, donc énumérable, et le coût
de calcul est la seule défense.

Une clé API de 256 bits générée aléatoirement n'a pas ce problème. L'énumérer demanderait 2²⁵⁵
essais — hors de portée de toute puissance de calcul concevable, quel que soit le coût unitaire.
Ralentir la vérification n'apporte **rien** contre cette attaque.

En revanche le coût est bien réel : Argon2id correctement paramétré demande ~50 ms et 64 Mio de
mémoire par vérification. Sur chaque requête, cela plafonnerait l'API à une vingtaine de requêtes
par seconde et par cœur — contre 20 000 visées — et **transformerait l'authentification en
vecteur de déni de service** : un attaquant non authentifié consommerait 64 Mio et 50 ms de CPU
par requête envoyée.

SHA-256 avec comparaison en temps constant donne ici les bonnes propriétés : résistance à la
préimage (le hash stocké ne permet pas de retrouver la clé), coût négligeable, et absence de fuite
temporelle. C'est aussi ce que font Stripe, GitHub et AWS pour leurs clés d'API.

> **La règle n'est pas « Argon2id partout » mais « la fonction adaptée à l'entropie du secret ».**
> Si des clés choisies par des humains devenaient possibles, la conclusion s'inverserait.
> Voir [ADR 0004](adr/0004-cles-api-sha256-plutot-quargon2id.md).

### 3.3 Configuration et cycle de vie

```bash
MEDS_API_KEYS=mk_live_aaa…,mk_live_bbb…      # clés en clair, hachées au démarrage
MEDS_ADMIN_KEY=mk_admin_ccc…                  # optionnelle ; /admin/sync désactivé si absente
```

Les clés sont hachées au démarrage puis **la variable d'environnement est effacée du processus**
(`os.Unsetenv`), pour qu'une éventuelle introspection (`/proc/self/environ`, vidage mémoire,
bibliothèque de diagnostic) ne les expose pas.

La révocation passe par un redéploiement. C'est acceptable pour un petit nombre de consommateurs ;
un magasin de clés dynamique serait une complexité disproportionnée au périmètre v1.

Refus de démarrage si `MEDS_API_KEYS` est vide : **aucun mode « ouvert par défaut »**. Un service
qui démarre sans authentification parce qu'une variable manque est une faille d'exploitation
classique.

---

## 4. Limitation de débit

Token bucket, deux niveaux appliqués simultanément :

| Portée | Défaut | Rôle |
|---|---|---|
| Par clé API | 100 req/s, rafale 200 | Protège l'équité entre consommateurs |
| Par IP | 200 req/s, rafale 400 | Protège contre l'abus avant authentification |

La limite par IP est **évaluée avant** l'authentification : sinon, un flot de requêtes non
authentifiées consommerait du calcul de hachage avant d'être rejeté.

```go
type Limiter struct {
    shards [256]struct {
        mu      sync.Mutex
        buckets map[string]*bucket
        _       [40]byte   // bourrage anti-faux-partage
    }
}
```

Le shardage par hachage de clé évite un mutex global qui deviendrait le goulot d'étranglement à
20 000 req/s. Le bourrage évite que deux shards voisins partagent une ligne de cache.

Réponse au dépassement :

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 1
RateLimit-Limit: 100
RateLimit-Remaining: 0
RateLimit-Reset: 1
```

Une goroutine de purge élimine périodiquement les buckets inactifs, afin que la table ne croisse
pas indéfiniment sous une attaque distribuée — la structure de défense ne doit pas devenir
elle-même le vecteur d'épuisement mémoire.

> **Derrière un reverse proxy, l'IP réelle vient de `X-Forwarded-For`.** Ce champ est falsifiable
> par le client : il ne doit être pris en compte que si la connexion provient d'un proxy de
> confiance déclaré (`MEDS_TRUSTED_PROXIES`). Sinon, un attaquant contourne la limite par IP en
> variant l'en-tête à chaque requête (T-35).

---

## 5. Durcissement du serveur HTTP

Les délais par défaut de `net/http` sont **illimités**. C'est la cause de la vulnérabilité
Slowloris : quelques centaines de connexions envoyant un octet par minute suffisent à saturer un
serveur non configuré.

```go
srv := &http.Server{
    Addr:              cfg.Addr,
    Handler:           handler,
    ReadHeaderTimeout: 5 * time.Second,   // ← la protection anti-Slowloris
    ReadTimeout:       10 * time.Second,
    WriteTimeout:      15 * time.Second,
    IdleTimeout:       60 * time.Second,
    MaxHeaderBytes:    16 << 10,          // 16 Kio
}
```

Les corps de requête sont bornés par `http.MaxBytesReader` (8 Kio), et chaque handler s'exécute
sous un `context` à 5 secondes.

### Validation des entrées

Tout paramètre est validé **avant** usage, avec une liste blanche :

| Paramètre | Règle |
|---|---|
| `cis` | exactement 8 chiffres |
| `cip` | exactement 7 ou 13 chiffres |
| `q` | ≤ 200 caractères, UTF-8 valide, caractères de contrôle rejetés |
| `limit` | entier dans `[1, 100]` (200 pour `/v1/suggest`) |
| `cursor` | base64url valide, hash de dataset concordant |
| énumérations | valeur appartenant à l'ensemble admis |

Un paramètre inconnu est **ignoré silencieusement** (tolérance aux évolutions de clients), mais un
paramètre connu avec une valeur invalide provoque un `400` explicite.

### Injections

L'API n'ayant ni base de données, ni système de gabarits, ni exécution de commandes, les surfaces
d'injection classiques (SQL, template, commande) sont **structurellement absentes**. Restent :

- **Injection de logs** — les caractères de contrôle et les retours à la ligne sont échappés par
  le format JSON de `slog`, qui ne peut pas produire de fausse ligne de log.
- **XSS via `/docs` ou le champ `highlight`** — la donnée BDPM contient des caractères actifs ;
  `highlight` est échappé en HTML **avant** insertion des balises `<em>` ([04](04-specification-api.md#29-get-v1suggest--autocomplétion)).
- **Traversée de chemin** — les chemins de snapshots sont construits à partir de hashs
  hexadécimaux validés, jamais d'une entrée utilisateur.

---

## 6. En-têtes de réponse

| En-tête | Valeur | Rôle |
|---|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | Force HTTPS |
| `X-Content-Type-Options` | `nosniff` | Empêche la réinterprétation du type |
| `X-Frame-Options` | `DENY` | Anti-clickjacking sur `/docs` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limite la fuite d'URL |
| `Content-Security-Policy` | `default-src 'none'; …` sur `/docs` | Confine la documentation |
| `X-Robots-Tag` | `noindex, nofollow, …, noai` | Refus d'indexation et d'entraînement |
| `X-Request-Id` | ULID | Corrélation client ↔ serveur |

`Server` est retiré : il n'apporte rien au client et renseigne un attaquant. Y compris sur la
redirection `80→443`, qui vit hors de tout bloc de site et échappait donc au `-Server` des profils
Caddy — corrigé par un bloc `http://` explicite
([ADR 0008](adr/0008-refus-indexation-et-entrainement-ia.md)).

### Refus d'indexation et d'entraînement IA

`X-Robots-Tag` est posé aux **cinq** endroits qui portent des en-têtes de sécurité — les deux
Caddyfile de `deploy/edge/`, `deploy/caddy/Caddyfile`, le middleware Traefik et `SecurityHeaders`.
Redondance délibérée : l'application est aussi jointe directement sur le réseau `internal`, où
aucun frontal n'intervient.

Sur le frontal mutualisé, l'en-tête vit dans le fragment `indexation-refusee` et non dans
`hardened`, pour qu'un service à référencer puisse simplement ne pas l'importer — un `header`
contenant `-Server` est `deferred` et ne peut plus être neutralisé
([08 §6.3](08-deploiement-docker.md)).

Trois fichiers le disent en clair, servis par les profils Caddy depuis `deploy/static/` :

| Fichier | Format | Contenu |
|---|---|---|
| `/robots.txt` | RFC 9309 | `Disallow: /` général, `Content-Signal`, sections par robot IA |
| `/ai.txt` | Spawning | Opt-out de la collecte pour l'entraînement |
| `/llms.txt` | Markdown | Politique lisible, et pointeur vers la source ANSM |

S'y ajoute un `403` au frontal sur une liste de robots d'entraînement notoires, fondé sur le
`User-Agent`. **Ce filtrage ne s'applique jamais aux trois fichiers ci-dessus** : la RFC 9309
§2.3.1.4 lit un `robots.txt` en `4xx` comme « aucune restriction », et un `403` y produirait
l'effet inverse de celui recherché. Portée, limites et coût de maintenance de la liste :
[ADR 0008](adr/0008-refus-indexation-et-entrainement-ia.md).

Le profil `traefik` porte l'en-tête mais **pas** les trois fichiers — Traefik ne sert pas de
statique sans un service supplémentaire.

### CORS

Désactivé par défaut. Activé uniquement sur liste blanche explicite :

```bash
MEDS_CORS_ORIGINS=https://app.example.com,https://admin.example.com
```

**Jamais `Access-Control-Allow-Origin: *` combiné à `Allow-Credentials: true`** — combinaison
interdite par la spécification et refusée par les navigateurs, mais qu'on rencontre encore.
L'authentification passant par un en-tête `Authorization` et non par un cookie, les credentials ne
sont pas nécessaires.

---

## 7. Sécurité du conteneur

Détail du montage dans [08](08-deploiement-docker.md) ; principes ici.

| Mesure | Mise en œuvre |
|---|---|
| Image minimale | `distroless/static` — ni shell, ni gestionnaire de paquets, ni utilitaire |
| Non-root | UID 65532 (`nonroot`), aucun `USER root` résiduel |
| Système de fichiers en lecture seule | `read_only: true`, seul `/data` est inscriptible |
| Capacités | `cap_drop: [ALL]` — aucune n'est nécessaire, le port d'écoute est > 1024 |
| Élévation | `no-new-privileges: true` |
| Binaire statique | `CGO_ENABLED=0` — aucune bibliothèque dynamique à compromettre |
| Build reproductible | `-trimpath`, `-ldflags "-s -w"` |
| Ressources | limites CPU et mémoire déclarées, pour qu'une fuite ne prenne pas l'hôte |

**Chaîne d'approvisionnement** : versions d'images épinglées **par digest** et non par étiquette
(une étiquette est mutable) ; `go.sum` vérifié ; `govulncheck` et `trivy` bloquants en intégration
continue sur les sévérités haute et critique ; SBOM CycloneDX produit à chaque publication.

---

## 8. Sécurité du pipeline de mise à jour

Le synchroniseur est le seul composant qui **écrit** et qui sort vers Internet : c'est la surface
la plus sensible.

| Risque | Contre-mesure |
|---|---|
| Interception de la source | HTTPS avec vérification de certificat, jamais `InsecureSkipVerify` |
| Réponse anormalement volumineuse | Lecture bornée (`io.LimitReader`, 100 Mio par fichier) |
| Source corrompue ou format modifié | Ingestion rejetée si le taux de rejet dépasse le seuil |
| Écriture partielle sur coupure | Écriture en fichier temporaire puis `os.Rename` atomique |
| Snapshot altéré sur disque | SHA-256 du manifest vérifié au chargement |
| Synchronisations concurrentes | Verrou en mémoire ; `409` si déjà en cours |
| Abus de `/admin/sync` | Clé d'administration distincte, limite de débit propre, endpoint désactivé si non configuré |

**Décompression** : la source ne propose pas de compression, mais si l'ANSM l'activait, la
décompression devrait être bornée pour éviter une bombe de décompression (`io.LimitReader` en
sortie de décompresseur, pas seulement en entrée).

---

## 9. Journalisation et données personnelles

Aucune donnée de santé personnelle n'est traitée. Les seules données à caractère personnel
possibles sont les **adresses IP** dans les logs d'accès.

| Règle | Mise en œuvre |
|---|---|
| Minimisation | Aucun corps de requête journalisé ; seuls méthode, chemin, statut, durée, request_id |
| Masquage | `Authorization` jamais journalisé, même tronqué |
| Rétention | 30 jours par défaut, à documenter dans la politique de l'exploitant |
| Anonymisation | Option `MEDS_LOG_ANONYMIZE_IP` tronquant le dernier octet (IPv4) ou les 80 derniers bits (IPv6) |

---

## 10. Réponse à incident

| Incident | Réaction |
|---|---|
| Clé compromise | La retirer de `MEDS_API_KEYS`, redéployer (< 1 min) |
| Abus depuis une IP | Blocage au niveau du reverse proxy (Caddy/Traefik), sans redéploiement |
| Snapshot corrompu | Revenir au précédent (`MEDS_SNAPSHOT_KEEP=3` en conserve trois), relancer une sync |
| Source ANSM inaccessible | Aucune action : l'ancien dataset continue d'être servi, l'âge est exposé |
| Vulnérabilité dans une dépendance | `govulncheck` en CI ; reconstruire et redéployer (surface réduite à 2 dépendances) |

## 11. Vérification

Couvert par la tâche **T-39** :

1. Requête sans clé, avec clé invalide, avec clé valide → `401`, `401`, `200`.
2. Analyse temporelle sur 10 000 tentatives : aucune corrélation entre le préfixe de la clé
   présentée et le temps de réponse.
3. Dépassement de quota → `429` avec `Retry-After` et en-têtes `RateLimit-*`.
4. Falsification de `X-Forwarded-For` depuis une IP non déclarée de confiance → sans effet.
5. Corps de 10 Mio → `413`.
6. `q` de 10 000 caractères, `limit=99999`, curseur forgé → `400`, sans panique.
7. Requête déclenchant une panic simulée → `500` générique, sans trace d'exécution, processus vivant.
8. `docker exec` dans le conteneur → échec (aucun shell).
9. Écriture hors `/data` → refusée (rootfs en lecture seule).
10. `trivy image` et `govulncheck` → aucune vulnérabilité haute ou critique.
