# ADR 0008 — Refus d'indexation et d'entraînement IA au frontal

- **Statut** : acceptée
- **Date** : 21/08/2026
- **Décideurs** : chef de projet, DevOps
- **Complète** : [ADR 0005](0005-caddy-et-traefik-en-profils.md), [ADR 0006](0006-frontal-mutualise-sur-reseau-externe.md)

## Contexte

meds-api n'expose librement que deux routes : `/docs` et `/openapi.json`
([06 §2](../06-securite.md)). Tout le reste exige une clé. Ces deux routes suffisent pourtant à
rendre le domaine intéressant pour un robot : `/openapi.json` décrit l'intégralité du modèle de
données, et `/docs` en produit une page HTML lisible.

Jusqu'ici rien ne l'interdisait. Le fragment `(hardened)` ne portait que HSTS,
`X-Content-Type-Options`, `Referrer-Policy` et `-Server`, et `GET /robots.txt` retombait sur
l'attrape-tout de `internal/api/server.go` — un `404` au format RFC 9457. Un robot y lit
l'absence de politique, ce qui vaut autorisation.

Deux raisons de trancher explicitement :

1. **L'API n'apporte rien à un corpus.** Les données viennent de la BDPM, publiée en open data
   par l'ANSM et déjà accessible en téléchargement direct. Moissonner cette API coûte de la
   bande passante des deux côtés pour obtenir une copie dégradée de ce qui est disponible à la
   source.
2. **Le budget de requêtes est fini.** Le dimensionnement de [07](../07-performance.md) suppose
   un trafic d'API. Un robot d'entraînement qui déroule la pagination consomme le quota de
   limitation prévu pour des clients légitimes.

## Options considérées

### A. Ne rien faire

Le refus reste implicite. Rejetée : l'absence de `robots.txt` n'est pas un refus, et le
`404` JSON servi à sa place est encore moins interprétable qu'un fichier vide.

### B. Refus déclaratif seul

`X-Robots-Tag` sur chaque réponse, plus `robots.txt`, `llms.txt` et `ai.txt`. Couvre tous les
robots qui coopèrent — c'est-à-dire les moteurs de recherche établis et la majorité des
collecteurs d'entraînement des grands éditeurs, qui documentent leur jeton et le respectent.

Ne couvre rien d'autre. Un collecteur qui ignore `robots.txt` ignore aussi l'en-tête.

### C. Refus déclaratif **et** filtrage effectif par `User-Agent`

B, plus un `403` au frontal sur une liste de robots d'entraînement notoires.

### D. Filtrage comportemental (empreinte TLS, limitation par ASN, défi de calcul)

Le seul dispositif qui résiste à un agent déterminé. Rejeté ici : il demande un composant de
plus à exploiter, produit des faux positifs sur des clients légitimes, et le service protégé est
une redistribution d'open data — l'enjeu ne justifie pas ce coût. Derrière Cloudflare, la règle
« AI Scrapers and Crawlers » du tableau de bord couvre déjà partiellement ce besoin sans ajouter
de composant.

## Décision

**Retenir l'option C**, en assumant que le second volet est une gêne et non une barrière.

### Volet déclaratif

L'en-tête est posé aux **cinq** endroits qui portent déjà des en-têtes de sécurité, avec la même
valeur :

```
X-Robots-Tag: noindex, nofollow, noarchive, nosnippet, noimageindex, notranslate, noai, noimageai
```

`deploy/edge/Caddyfile`, `deploy/edge/Caddyfile.cloudflare`, `deploy/caddy/Caddyfile`,
`deploy/traefik/dynamic/middlewares.yml` et `SecurityHeaders` dans `internal/api/middleware.go`.
La redondance est délibérée : c'est le contrat de parité entre profils de
[11](../11-strategie-de-tests.md), et l'application est aussi jointe directement sur le réseau
`internal`, où aucun frontal n'intervient.

`noai` et `noimageai` ne sont pas normalisés — convention issue de Spawning. Ils sont inoffensifs
pour les robots qui ne les comprennent pas.

Sur le frontal mutualisé, l'en-tête est porté par le fragment `indexation-refusee`, **pas** par
`hardened`. Ce n'est pas un détail de rangement : un bloc `header` contenant `-Server` est marqué
`deferred` par Caddy et appliqué à l'écriture de la réponse, donc après tout `header` posé plus
loin dans le bloc de site. Placé dans `hardened`, `X-Robots-Tag` serait devenu **non
neutralisable** — vérifié le 21/08/2026 : `header -X-Robots-Tag`, l'écrasement par `all` et la
sous-directive `defer` échouent tous les trois. Le frontal étant mutualisé (ADR 0006), il
hébergera un jour un service qui doit, lui, être référencé ; rendre l'import optionnel est la
seule composition qui le permette.

Les trois fichiers vivent dans `deploy/static/`, **hors** de `deploy/edge/`, monté en lecture
seule sur `/srv/indexation` par les deux profils Caddy. Une seule source pour les deux frontaux.

- `robots.txt` — RFC 9309, plus un `Content-Signal`, plus des sections nommées par robot.
- `ai.txt` — format Spawning.
- `llms.txt` — le format est à l'origine un index destiné à *faciliter* la lecture par un modèle.
  Ce fichier en fait l'usage inverse et le dit explicitement, pour qu'un agent qui le cherche
  trouve le refus plutôt qu'un `404` qu'il interpréterait comme une absence de politique.

### Volet effectif

Un `header_regexp User-Agent` sur une liste de robots d'entraînement notoires renvoie `403`, dans
les deux profils Caddy.

**Ce filtrage passe après le service des fichiers, et c'est essentiel.** La RFC 9309 §2.3.1.4
interprète un `robots.txt` en `4xx` comme « aucune restriction ». Servir un `403` sur `robots.txt`
au robot que l'on cherche à refuser produirait donc exactement l'effet inverse. L'ordre est obtenu
par construction : `handle` précède `respond` dans l'ordre des directives de Caddy.

Deux exclusions volontaires de la liste :

- `Google-Extended` et `Applebot-Extended` sont des **jetons `robots.txt`**, pas des `User-Agent`.
  Ils ne matcheraient jamais dans le motif, et filtrer `Applebot` bloquerait Siri et Spotlight —
  autre décision, autre arbitrage.
- `Applebot` tout court, pour la même raison.

Inclusions discutables et assumées : `ChatGPT-User`, `Claude-User` et `Perplexity-User` désignent
des récupérations **demandées par un humain**, pas du moissonnage. Les refuser est un choix ;
le motif est commenté pour qu'il reste facile de les retirer.

## Conséquences

### Corollaire : `Server` retiré aussi de la redirection HTTP→HTTPS

La vérification de ce changement a mis au jour un reste de la politique « `Server` retiré »
([06 §6](../06-securite.md)). L'en-tête était bien absent de toutes les réponses des blocs de
site — routes applicatives, `403`, fichiers statiques — mais **présent sur la redirection
`80→443`**, mesuré le 21/08/2026 : `curl -I http://…/` renvoyait `308` avec `Server: Caddy`.

Cette redirection est générée par `auto_https` et vit hors de tout bloc de site : elle
n'exécute aucune directive `header`, donc aucun `-Server` ne l'atteint. Le correctif est de la
reprendre explicitement, dans les profils qui publient le port 80 :

```caddyfile
{
	auto_https disable_redirects
}

http:// {
	header -Server
	redir https://{host}{uri} permanent
}
```

Bloc attrape-tout, donc valable pour tous les domaines de `conf.d/` sans duplication par service.

**`auto_https disable_redirects` ne casse pas le challenge ACME HTTP-01.** C'était le risque de la
manœuvre — un échec silencieux aurait bloqué le renouvellement de tous les certificats de la
machine soixante jours plus tard, précisément le scénario que l'[ADR 0006](0006-frontal-mutualise-sur-reseau-externe.md)
signale comme le pire. Le point a donc été vérifié plutôt que supposé : le 21/08/2026, contre un
ACME réel (Pebble configuré sur `httpPort: 80`) et avec ce montage actif, Caddy a servi la clé
d'authentification et obtenu le certificat. La route du challenge est interceptée **avant** le
routage, le bloc `http://` ne la voit jamais.

La variante Cloudflare ne reçoit pas ce bloc : elle ne publie pas le port 80 et Cloudflare
redirige à sa périphérie.

Portée de ce que cela corrige : une divulgation du nom du logiciel serveur, sans numéro de
version, sur une réponse sans contenu. Mince — retenu par cohérence avec une politique déjà
décidée, pas parce que la fuite était grave.

### Positives

- Le refus est explicite, lisible par un humain comme par un robot, et exprimé dans les trois
  formats que les collecteurs sont susceptibles de lire.
- Le budget de limitation reste disponible pour les clients légitimes.
- Les trois fichiers pointent vers la source ANSM : un collecteur bien intentionné y trouve un
  meilleur chemin que le moissonnage.
- `deploy/static/` étant partagé, modifier la politique se fait à un seul endroit pour les deux
  profils Caddy.

### Négatives — assumées

- **La liste d'`User-Agent` vieillit.** Elle sera périmée dans six mois. C'est un coût de
  maintenance récurrent, sans échéance forcée : une liste périmée dégrade le volet effectif, elle
  ne casse rien.
- **Un agent qui falsifie son `User-Agent` passe au travers**, et c'est trivial à faire. Le volet
  effectif ne trompe que les robots honnêtes sur leur identité — soit, en pratique, ceux qui
  respectaient déjà `robots.txt`. Son apport réel est mince ; il est retenu parce qu'il coûte
  deux lignes.
- **Le profil `traefik` n'a que l'en-tête, pas les fichiers.** Traefik ne sert pas de statique
  sans un service supplémentaire, et en ajouter un pour trois fichiers texte coûterait plus que
  le gain. Conséquence à connaître : ne pas exposer meds-api derrière le profil `traefik` si le
  refus déclaratif par fichiers est exigé.
- **Trois duplications de plus à tenir en parité** : le motif `User-Agent` existe en trois
  exemplaires (deux Caddyfile edge, le profil dédié), la valeur de `X-Robots-Tag` en cinq. La
  duplication des fragments entre les deux Caddyfile edge est une contrainte préexistante du
  montage (ADR 0006), pas une régression introduite ici.
- **Un faux positif est possible** si un client légitime porte un `User-Agent` contenant l'un des
  motifs. Le risque est faible — ce sont des noms de produits — mais il augmente avec les outils
  génériques : `Scrapy` a été écarté du motif pour cette raison, c'est un cadre de collecte
  employé pour tout et n'importe quoi. `img2dataset` est conservé, son nom dit son usage.

### Ce qui invaliderait cette décision

Si le service devait un jour publier du contenu rédactionnel destiné à être trouvé — un guide,
une documentation publique —, `noindex` global deviendrait contre-productif et il faudrait passer
à un refus par chemin plutôt que par domaine.

À l'inverse, si un moissonnage effectif venait à dégrader le service malgré ces mesures, il
faudrait reconsidérer l'option D : la réponse serait alors une limitation comportementale, pas
une liste d'`User-Agent` plus longue.
