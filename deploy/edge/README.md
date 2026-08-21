# Frontal mutualisé — Caddy sur réseau Docker partagé

Cette stack est le **seul** composant de la machine à détenir les ports 80 et 443. Elle sert
meds-api **et tout autre service** hébergé sur le même serveur, chacun sur son propre domaine,
avec son propre certificat, sans conflit de port.

Elle est indépendante du cycle de vie des applications : on la démarre une fois, elle survit aux
`down`/`up` de meds-api.

```
                        :80  :443
                          │
                   ┌──────┴───────┐
                   │  edge-caddy  │   TLS, HTTP/3, en-têtes, compression
                   └──────┬───────┘
              réseau Docker `edge` (externe)
              ┌───────────┼───────────┐
         ┌────┴─────┐ ┌───┴────┐ ┌────┴─────┐
         │ meds-api │ │ blog   │ │ …        │   aucun port hôte publié
         └──────────┘ └────────┘ └──────────┘
```

---

## Installation

```bash
cp .env.example .env          # renseigner ACME_EMAIL et MEDS_DOMAIN
make up                       # crée le réseau `edge`, valide la config, démarre
```

`make up` est idempotent : il crée le réseau `edge` s'il n'existe pas et refuse de démarrer si un
Caddyfile est invalide.

Brancher ensuite meds-api dessus, depuis la racine du dépôt :

```bash
docker compose -f docker-compose.yml -f deploy/edge/compose.meds-api.yml up -d
```

**Ne pas activer le profil `caddy` du projet** dans ce mode : il publierait à son tour 80/443 et
échouerait sur `port is already allocated`.

---

## Ajouter un service

1. Déposer un fichier dans `conf.d/` (partir de `exemple-autre-service.caddy.disabled`) :

   ```caddyfile
   blog.example.org {
       import tls_config    # vide en mode ACME, certificat d'origine derrière Cloudflare
       import hardened
       reverse_proxy blog:3000
   }
   ```

2. Rattacher le conteneur au réseau `edge` avec un `container_name` stable — c'est ce nom que
   Caddy résout :

   ```yaml
   services:
     blog:
       container_name: blog
       networks: [edge]        # et surtout : aucune clé `ports:`
   networks:
     edge: { name: edge, external: true }
   ```

3. Recharger à chaud :

   ```bash
   make reload
   ```

**Aucune coupure** : `caddy reload` remplace la configuration sans fermer les connexions en cours,
et le certificat du nouveau domaine est obtenu automatiquement au premier appel — à condition que
son enregistrement DNS pointe déjà vers ce serveur.

---

## Cibles disponibles

| Commande | Effet |
|---|---|
| `make up` | Crée le réseau, valide, démarre (mode ACME) |
| `make up-cloudflare` | Idem derrière Cloudflare, après vérification des certificats |
| `make validate-cloudflare` | Valide la configuration Cloudflare |
| `make reload` | Valide puis recharge à chaud (après ajout d'un service) |
| `make validate` | Valide la configuration sans toucher au frontal en service |
| `make routes` | Liste les domaines effectivement servis |
| `make fmt` | Reformate les Caddyfile |
| `make logs` | Suit le journal |
| `make ca` | Exporte l'AC interne (environnements en `.localhost`) |
| `make backup` | Archive les certificats et la clé de compte ACME |

---

## Certificats

| `MEDS_DOMAIN` | Comportement |
|---|---|
| Nom public (`meds.example.org`) | Certificat Let's Encrypt automatique (HTTP-01 ou TLS-ALPN-01), renouvelé sans intervention |
| Nom en `.localhost` | Caddy génère son **AC interne** et signe lui-même — ni openssl, ni CSR, ni script |

Pour faire reconnaître l'AC interne par le poste de développement :

```bash
make ca
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain caddy-root.crt      # macOS
```

Le volume `caddy-data` **doit être persisté**. Il contient les certificats et la clé de compte
ACME : sans lui, chaque redémarrage redemande tous les certificats de tous les domaines et
consomme le quota Let's Encrypt (50 certificats par domaine et par semaine). C'est le seul volume
de la machine qui mérite une sauvegarde — celui de meds-api est intégralement reconstructible
depuis l'ANSM.

---

## Ce qui est fermé par défaut

- **Domaine non déclaré dans `conf.d/`** → aucun certificat demandé, poignée de main TLS refusée.
  Vérifié : `curl https://inconnu.localhost/` échoue au niveau TLS.
- **`/metrics` et `/debug/*`** → `403` inconditionnel depuis le frontal public. Prometheus scrute
  `api:8080/metrics` **en direct** sur le réseau `internal`, sans passer par ici.

  Le filtrage par `not remote_ip private_ranges` a été essayé puis écarté : sous Docker Desktop,
  OrbStack, un `userland-proxy` ou un CDN, l'adresse source vue par Caddy est celle d'une
  passerelle privée. La règle laisse alors fuiter `/metrics` vers Internet **en croyant le
  refuser** — mesuré le 15/08/2026, `curl https://…/metrics` renvoyait `200` au lieu de `403`.
  Un refus inconditionnel ne dépend d'aucune hypothèse sur la topologie réseau.

---

## Vérifications effectuées le 15/08/2026

Sur Caddy 2.11.4-alpine, deux services simultanés derrière un même frontal :

| Vérification | Résultat |
|---|---|
| Routage HTTPS vers l'amont | `200`, corps du backend servi |
| En-têtes HSTS, nosniff, Referrer-Policy | présents ; `Server` retiré |
| HTTP/3 annoncé | `alt-svc: h3=":443"` |
| Redirection HTTP → HTTPS | `308` vers `https://…` |
| `/metrics`, `/debug/pprof/*` | `403` |
| Ajout d'un 2ᵉ service + `caddy reload` | les deux servis, aucune coupure sur le 1ᵉʳ |
| Certificats | un par domaine, SAN distincts, AC interne |
| Domaine inconnu | poignée de main TLS refusée |
| Conteneur `read_only` + `cap_drop: ALL` | démarre et passe `healthy` |

---

## Variante : Cloudflare en proxy

Lorsque le trafic entre par Cloudflare, le frontal ne fait **plus d'ACME du tout** : le certificat
vient de Cloudflare et vaut quinze ans.

```bash
docker compose -f docker-compose.yml -f compose.cloudflare.yml up -d
```

### Le réglage qui prime : « Full (strict) »

Dans Cloudflare, *SSL/TLS → Overview*, un seul mode est acceptable :

| Mode | Ce qu'il fait | Verdict |
|---|---|---|
| **Flexible** | Cloudflare parle à votre serveur **en clair** | Les clés API circulent en HTTP. Rédhibitoire. |
| **Full** | Chiffré, mais le certificat de l'origine n'est pas vérifié | Une interposition entre Cloudflare et le serveur passe inaperçue. |
| **Full (strict)** | Chiffré **et** vérifié | **Le seul correct.** |

C'est l'erreur Cloudflare la plus répandue. Sur une API dont chaque requête porte une clé dans son
en-tête `Authorization`, le mode Flexible revient à publier ces clés.

### Marche à suivre

1. **DNS** — un enregistrement `A` vers le serveur, **nuage orange activé** (proxied).
2. **SSL/TLS → Overview** → *Full (strict)*. Puis *Edge Certificates* → « Always Use HTTPS » et HSTS.
3. **SSL/TLS → Origin Server** → *Create Certificate*. Couvrir `example.org` **et** `*.example.org`
   si plusieurs services partagent le frontal — un seul certificat suffit alors pour tous.
   Déposer les deux fichiers :

   ```
   deploy/edge/certs/origin.pem        # le certificat
   deploy/edge/certs/origin-key.pem    # la clé privée, jamais versionnée
   ```

4. **SSL/TLS → Origin Server → Authenticated Origin Pulls** → activer, puis récupérer l'autorité
   de Cloudflare dans `deploy/edge/certs/cloudflare-origin-pull-ca.pem`.
5. **Pare-feu** — n'ouvrir que le **443**, et uniquement aux [plages Cloudflare](https://www.cloudflare.com/ips/).
   Le port 80 reste fermé : Cloudflare redirige déjà à sa périphérie, et il n'y a plus de challenge
   ACME à servir.
6. **Démarrer** — `cp .env.example .env` (renseigner `MEDS_DOMAIN`), puis la commande ci-dessus.

### Pourquoi deux protections contre le contournement

Sans elles, votre adresse IP sert l'API **sans passer par Cloudflare** : ni WAF, ni protection
contre le déni de service, ni règles de trafic. Il suffit de connaître l'IP.

- Le **pare-feu** filtre par adresse source. Il tombe si les plages Cloudflare changent sans que
  vous les mettiez à jour.
- **Authenticated Origin Pulls** exige un certificat client signé par Cloudflare. Il ne dépend
  d'aucune liste à tenir à jour.

Les deux se cumulent ; aucune ne remplace l'autre.

### Ne pas activer « Cache Everything »

L'API émet `Cache-Control: public, max-age=3600`, ce qui est correct — les données BDPM sont
publiques et identiques pour toutes les clés. Mais une règle Cloudflare « Cache Everything » sur
`/v1/*` ferait servir des réponses depuis le cache de la périphérie, sans que la clé API soit
revalidée à chaque appel.

Par défaut Cloudflare ne met pas en cache un chemin sans extension de fichier : **ne touchez à
rien** et le comportement est correct. Si vous ajoutez des règles de cache, excluez explicitement
`/v1/*`.

### Ce qui change dans la configuration

Un seul fragment diffère entre les deux variantes, `tls_config` — vide côté ACME, porteur du
certificat et de l'authentification client côté Cloudflare. **Les fichiers de `conf.d/` sont
identiques** dans les deux montages : basculer de l'un à l'autre ne demande aucune retouche des
services.

Deux réglages propres à ce montage :

- **HTTP/1.1 et HTTP/2 seulement.** Cloudflare négocie HTTP/3 avec le navigateur mais se connecte
  à l'origine en h1 ou h2. Annoncer h3 ici ajouterait un `alt-svc` trompeur, puisque aucun client
  ne parle jamais directement à ce serveur.
- **`auto_https disable_redirects`.** Le port 80 n'étant pas publié, laisser Caddy tenter de
  l'écouter ne produirait qu'un avertissement à chaque démarrage.

### Vérifications du 16/08/2026

Montage éprouvé avec une autorité et un certificat client factices reproduisant la mécanique
Cloudflare :

| Vérification | Résultat |
|---|---|
| Client **sans** certificat (contournement par l'IP) | **poignée de main TLS refusée** — rejet avant HTTP |
| Client **avec** certificat signé par l'autorité | `200`, backend atteint |
| `/metrics` avec un certificat valide | `403` |
| Protocole négocié | HTTP/2, aucun `alt-svc` h3 |
| En-têtes | HSTS, `nosniff`, `Referrer-Policy` présents ; aucun `Server` |

### Sauvegarde

Contrairement au montage ACME, le volume `caddy-data` n'a **plus** à être sauvegardé : il ne
contient plus de certificat obtenu par quota. Le certificat d'origine se régénère en trois clics
depuis le tableau de bord Cloudflare.

## Dépannage

**`Bind for 0.0.0.0:80 failed: port is already allocated`**
Un autre processus détient le port. Sur un poste de développement, c'est souvent le proxy intégré
de Docker Desktop ou d'OrbStack. `sudo lsof -nP -iTCP:80 -sTCP:LISTEN` identifie le coupable. Sur
un serveur, c'est généralement un Apache/Nginx installé par le paquet système, ou le profil
`caddy` du projet resté actif.

**Le certificat Let's Encrypt n'est pas délivré**
Le challenge HTTP-01 passe par le port 80 : il doit rester joignable depuis Internet, non filtré
par le pare-feu, et le DNS du domaine doit déjà pointer vers ce serveur au moment du démarrage.

**Erreur 526 côté Cloudflare (« Invalid SSL certificate »)**
Le mode est « Full (strict) » mais l'origine ne présente pas un certificat que Cloudflare
reconnaît. Vérifier que `certs/origin.pem` est bien le certificat d'origine **Cloudflare** et non
un auto-signé quelconque, et que son nom couvre le domaine demandé.

**Erreur 525 (« SSL handshake failed »)**
La poignée de main échoue. Avec Authenticated Origin Pulls, la cause la plus fréquente est que
l'option est activée côté Cloudflare mais que `certs/cloudflare-origin-pull-ca.pem` est absent ou
périmé côté serveur — ou l'inverse : Caddy exige un certificat client que Cloudflare n'envoie pas
encore.

**`502` sur un service**
Caddy résout l'amont par le nom du conteneur : vérifier que celui-ci est bien sur le réseau `edge`
(`docker inspect -f '{{json .NetworkSettings.Networks}}' <conteneur>`) et que son `container_name`
correspond exactement à celui écrit dans `conf.d/`.
