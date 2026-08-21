# ADR 0005 — Caddy et Traefik en profils Compose

- **Statut** : acceptée
- **Date** : 14/08/2026
- **Décideurs** : chef de projet, DevOps

## Contexte

L'API écoute en HTTP simple sur le port 8080 : la terminaison TLS, la redirection HTTP → HTTPS et
la restriction de `/metrics` incombent à un frontal.

Deux exigences distinctes, qu'il est facile de confondre :

1. **En production** — certificats Let's Encrypt obtenus et renouvelés automatiquement.
2. **En développement et sur les environnements internes** — **certificats auto-générés**, sans
   génération manuelle par openssl ni CSR.

La seconde exigence était explicite dans l'énoncé du projet, et c'est elle qui départage les
candidats.

## Options considérées

### Caddy 2.11.4

- TLS automatique par défaut, sans configuration.
- **Autorité de certification interne** : sur un nom local (`localhost`, `*.localhost`), Caddy
  génère sa propre AC et signe le certificat. Aucun outil externe.
- Configuration très concise — le `Caddyfile` du projet tient en une trentaine de lignes
  ([08](../08-deploiement-docker.md#4-profil-caddy)).
- HTTP/3 activé nativement.
- Moins répandu en entreprise ; écosystème de middlewares plus restreint.

### Traefik v3.7.10

- Découverte automatique des services par labels Docker — très efficace quand plusieurs services
  cohabitent derrière le même frontal.
- ACME intégré, middlewares riches (limitation de débit, filtrage IP, réécriture, circuit breaker).
- Tableau de bord de supervision.
- **Pas d'AC interne** : sans résolveur ACME joignable, Traefik sert un certificat auto-signé
  générique que les navigateurs rejettent. Un développement local propre exige un outil tiers
  comme `mkcert`.
- Configuration plus verbeuse, répartie entre fichier statique, fichier dynamique et labels.

### Nginx

Écarté : ni ACME intégré, ni AC interne. Il faudrait ajouter certbot et un mécanisme de
rechargement — trois pièces mobiles là où les deux autres n'en demandent aucune.

## Décision

**Livrer les deux, via des profils `docker-compose`, avec Caddy en profil recommandé.**

```bash
docker compose --profile caddy   up -d      # défaut recommandé
docker compose --profile traefik up -d      # si Traefik est déjà en place
```

Le service `api` est **strictement identique** dans les deux cas ; seul le frontal change. Les deux
profils fournissent les mêmes garanties : TLS, redirection HTTP → HTTPS, en-têtes de sécurité,
compression, restriction de `/metrics` au réseau interne.

### Pourquoi Caddy est recommandé

C'est le seul des deux qui satisfait l'exigence de certificats auto-générés **sans outil externe**.
En `DOMAIN=localhost`, `docker compose --profile caddy up -d` produit une API en HTTPS avec un
certificat valide, immédiatement — ce qui est exactement l'objectif O5
([00](../00-vision-et-perimetre.md#3-objectifs)).

### Pourquoi livrer Traefik malgré tout

Le choix du frontal appartient souvent à l'exploitant, pas au projet. Une organisation qui opère
déjà un Traefik n'en déploiera pas un second : lui imposer Caddy la conduirait à réécrire
elle-même une configuration non testée. Fournir un profil Traefik vérifié en intégration continue
supprime ce risque.

## Conséquences

### Positives

- Déploiement en une commande dans les deux écosystèmes.
- Certificats auto-générés en local sans openssl ni script (profil Caddy).
- L'API reste agnostique : elle ne connaît rien du frontal, ce qui permettrait d'en ajouter un
  troisième sans la modifier.
- Les deux profils sont vérifiés par les tests de fumée (T-56), donc aucun ne se dégrade en
  silence.

### Négatives — assumées

- **Deux configurations à maintenir**, chacune devant recevoir toute évolution (en-têtes,
  restrictions, routes). Coût récurrent réel.
- **Deux jeux de tests de fumée**, soit environ 3 minutes de plus en intégration continue.
- **Parité imparfaite en local** : Traefik exige `mkcert` là où Caddy ne demande rien. Cet écart
  est documenté plutôt que masqué ([08](../08-deploiement-docker.md#5-profil-traefik)).
- **Risque de dérive** si une modification n'est appliquée qu'à un profil. Atténuation : les tests
  de fumée vérifient les mêmes assertions sur les deux, notamment la présence des en-têtes de
  sécurité et le refus de `/metrics` depuis l'extérieur.

### Ce qui invaliderait cette décision

Si la maintenance des deux profils devenait pesante — divergence répétée, temps de CI excessif —
il faudrait n'en garder qu'un. Ce serait alors Caddy, pour l'exigence de certificats auto-générés,
la configuration Traefik étant reversée en documentation d'exemple non testée, avec une mention
explicite de son statut.
