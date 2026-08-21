# ADR 0004 — Clés API hachées en SHA-256 plutôt qu'Argon2id

- **Statut** : acceptée
- **Date** : 14/08/2026
- **Décideurs** : chef de projet, équipe backend, référent sécurité

## Contexte

L'accès à l'API est protégé par des clés, présentées en `Authorization: Bearer`. Ces clés doivent
être stockées côté serveur sous une forme qui ne permette pas de les retrouver en cas de fuite de
la configuration.

Le réflexe courant — et la recommandation de l'OWASP pour les mots de passe — est d'employer une
fonction de dérivation lente : **Argon2id**, bcrypt ou scrypt. La conception initiale de ce projet
retenait d'ailleurs Argon2id, avant que l'analyse des budgets ne conduise à la réexaminer.

Contexte chiffré :

- Budget de débit : **> 20 000 req/s sur 2 vCPU** ([07](../07-performance.md#22-débit-et-ressources)).
- Chaque requête `/v1/*` exige une vérification de clé.
- Les clés sont **générées par le serveur**, jamais choisies par un humain.

## Le raisonnement

### Pourquoi les fonctions lentes existent

Argon2id, bcrypt et scrypt sont **délibérément coûteuses**. Leur raison d'être est la protection
d'un secret à faible entropie : un mot de passe humain porte 30 à 40 bits d'entropie, ce qui le
rend énumérable. Face à un attaquant qui a volé la base de hachés, le coût de calcul par essai est
la seule défense — ralentir chaque tentative d'un facteur 10⁶ transforme une attaque de quelques
heures en plusieurs siècles.

### Pourquoi cet argument ne s'applique pas ici

Une clé API générée par `crypto/rand` sur 32 octets porte **256 bits d'entropie**. L'énumérer
demanderait de l'ordre de 2²⁵⁵ essais. Aucune puissance de calcul concevable n'en approche, quel
que soit le coût unitaire d'un essai.

Ralentir la vérification n'apporte donc **strictement rien** contre l'attaque que la fonction
lente est censée contrer : cette attaque est déjà impossible du fait de l'entropie.

### Ce que coûterait Argon2id

Correctement paramétré (RFC 9106, ~64 Mio de mémoire, 3 itérations), Argon2id demande environ
**50 ms et 64 Mio par vérification**. Conséquences :

| Effet | Conséquence |
|---|---|
| Débit | ~20 req/s par cœur, contre 20 000 visées — **facteur 1 000** |
| Latence | +50 ms sur un budget de 200 µs — **250 fois le budget** |
| Mémoire | 64 Mio par vérification concurrente ; 10 requêtes simultanées = 640 Mio |
| **Sécurité** | **L'authentification devient elle-même un vecteur de déni de service** |

Ce dernier point est le plus grave et le moins intuitif : un attaquant **non authentifié**
consommerait 50 ms de CPU et 64 Mio de RAM par requête envoyée. Quelques dizaines de requêtes par
seconde suffiraient à effondrer le service. **Employer Argon2id ici affaiblirait la sécurité au
lieu de la renforcer**, puisque le risque réel sur ce service est la disponibilité, pas la
confidentialité ([06](../06-securite.md#1-contexte-de-risque)).

## Décision

**SHA-256 avec comparaison en temps constant.**

```go
sum := sha256.Sum256([]byte(presented))
for _, known := range knownHashes {
    if subtle.ConstantTimeCompare(sum[:], known[:]) == 1 { return true }
}
```

Propriétés obtenues :

| Propriété | Assurée par |
|---|---|
| Résistance à la préimage | SHA-256 — le haché ne permet pas de retrouver la clé |
| Absence de fuite temporelle | `subtle.ConstantTimeCompare` |
| Coût négligeable | ~500 ns, invisible dans le budget de 200 µs |
| Impossibilité d'énumérer | 256 bits d'entropie de la clé |

Mesures complémentaires ([06](../06-securite.md#3-authentification-par-clés-api)) : génération par
`crypto/rand` uniquement, préfixe `mk_live_` pour la détection automatique de secret par les
scanners de dépôts, effacement de la variable d'environnement après hachage au démarrage.

C'est également la pratique de Stripe, GitHub et AWS pour leurs clés d'API — pour exactement ce
raisonnement.

## Conséquences

### Positives

- Budget de débit tenu : l'authentification est invisible dans le profil de latence.
- L'authentification cesse d'être un vecteur d'épuisement de ressources.
- Aucune dépendance supplémentaire (`crypto/sha256` et `crypto/subtle` sont dans la bibliothèque
  standard).
- Vérification simple, donc auditable.

### Négatives — assumées

- **Une fuite de la configuration expose les hachés**, à partir desquels une clé faible serait
  retrouvable. Sans effet tant que les clés sont générées par le serveur — mais c'est précisément
  l'hypothèse à protéger (voir ci-dessous).
- **Contre-intuitif en revue de sécurité.** Un auditeur pressé signalera « SHA-256 pour des
  secrets, non conforme OWASP ». D'où cet ADR, à lui présenter.
- **Pas de facteur de coût réglable** si le contexte changeait.
- **Révocation par redéploiement** uniquement (limite du magasin de clés statique, indépendante de
  cette décision).

### La condition à protéger

Cette décision repose entièrement sur une hypothèse : **les clés sont générées aléatoirement avec
au moins 128 bits d'entropie, jamais choisies par un humain**.

Si un jour des clés définies par l'utilisateur devenaient possibles, la conclusion **s'inverserait
immédiatement** et Argon2id redeviendrait obligatoire.

Pour que cette hypothèse ne se dégrade pas silencieusement, elle est verrouillée par le code
(T-34) : au démarrage, toute clé qui ne respecte pas le format attendu — préfixe `mk_live_` /
`mk_test_` et longueur minimale — **empêche le démarrage**, avec un message renvoyant à cet ADR.

> **La règle générale n'est pas « Argon2id partout », mais « la fonction adaptée à l'entropie du
> secret ».** Appliquer une recommandation hors de son contexte peut dégrader la sécurité au lieu
> de l'améliorer.
