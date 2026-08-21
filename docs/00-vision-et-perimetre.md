# 00 — Vision et périmètre

## 1. Problème

La **Base de Données Publique des Médicaments** (BDPM) est le référentiel officiel français des
médicaments disposant d'une autorisation de mise sur le marché. Elle est produite par l'ANSM,
enrichie par la HAS (avis SMR/ASMR) et le CEPS (prix, remboursement), et publiée en open data.

Sa distribution, en revanche, est brute :

- **10 fichiers TSV** téléchargés séparément, sans en-tête, sans délimiteur de champ ;
- **encodages hétérogènes** entre fichiers, et qui changent dans le temps ;
- **aucune API**, aucun format structuré (ni JSON, ni XML, ni Parquet) ;
- **aucun cache HTTP** exploitable (ni `ETag`, ni `Last-Modified`) ;
- des **écarts entre la documentation officielle et la donnée réelle** (voir [01](01-analyse-source-bdpm.md)).

Conséquence : chaque intégrateur — éditeur de logiciel de prescription, officine, application de
santé, laboratoire de recherche — réimplémente le même parseur, redécouvre les mêmes pièges, et
maintient sa propre copie potentiellement périmée.

## 2. Vision

> Offrir un accès immédiat, fiable et rapide à la donnée médicamenteuse française,
> en absorbant une fois pour toutes la complexité de la source.

Trois qualités non négociables, dans cet ordre :

1. **Exactitude** — la donnée servie est fidèle à la source. Aucune ligne n'est perdue en silence :
   ce qui ne peut pas être interprété est mis en quarantaine, compté et exposé.
2. **Fraîcheur** — le jeu de données se rafraîchit automatiquement, sans intervention ni coupure.
3. **Vitesse** — une réponse en moins d'une milliseconde, pour que l'API puisse être appelée
   dans une boucle d'interface sans dégrader l'expérience.

L'exactitude prime sur la fraîcheur, qui prime sur la vitesse. Une synchronisation qui produirait
un jeu de données incohérent doit être **rejetée**, l'ancien restant servi.

## 3. Objectifs

| # | Objectif | Mesure de succès |
|---|---|---|
| O1 | Exposer 100 % des données des 10 fichiers BDPM | Chaque champ de chaque fichier est atteignable par au moins un endpoint |
| O2 | Recherche tolérante aux accents et aux fautes | `paracetamol`, `paracétamol`, `paracetamo` renvoient les mêmes résultats en tête |
| O3 | Mise à jour automatisée sans coupure | Aucune requête en erreur pendant un rechargement ; validé par test de charge |
| O4 | Performance de référence | Tous les budgets du [document 07](07-performance.md) tenus |
| O5 | Déploiement en une commande | `docker compose --profile caddy up -d` suffit, TLS compris |
| O6 | Sécurité par défaut | Aucun accès anonyme aux données ; image non-root, rootfs en lecture seule |
| O7 | Contrat d'API explicite | OpenAPI 3.1 validé par `spectral lint`, sans erreur |

## 4. Non-objectifs (v1)

Ces exclusions sont délibérées et documentées pour éviter la dérive de périmètre.

| Hors périmètre | Pourquoi |
|---|---|
| Écriture, correction ou enrichissement de la donnée | L'API est un miroir fidèle de la source officielle. Toute correction créerait une divergence non traçable avec l'ANSM. |
| Interactions médicamenteuses, contre-indications, posologies | **Absentes de la BDPM.** Elles relèvent du Thésaurus ANSM et des RCP, sources distinctes. Les inventer serait dangereux. |
| Aide à la décision clinique ou à la prescription | Franchirait le seuil du **dispositif médical** au sens du règlement (UE) 2017/745, avec les obligations de certification correspondantes. Voir §7. |
| Notices et RCP en texte intégral | Non fournis dans les fichiers téléchargeables ; disponibles uniquement en HTML sur le portail. |
| Historique et versionnement du dataset | v1 sert l'état courant. Le diff entre versions est une évolution identifiée (§6). |
| Base de données relationnelle | Injustifiée pour 22 Mo en lecture seule. Voir [ADR 0002](adr/0002-memoire-plutot-que-sqlite.md). |
| Interface web / frontend | L'API est destinée à des intégrations machine. Seule la doc interactive `/docs` est servie. |
| Multi-tenant, facturation, quotas commerciaux | Les clés API portent un simple quota technique, pas un modèle commercial. |

## 5. Utilisateurs cibles

**Intégrateur logiciel santé** — développe un logiciel de prescription ou de dispensation.
Besoin : lookup fiable par code CIS ou CIP, avec prix et taux de remboursement.
Attente : latence négligeable, contrat stable, disponibilité.

**Développeur d'application grand public** — construit un service d'information sur les
médicaments. Besoin : recherche par nom commercial ou substance, tolérante aux fautes de frappe,
avec autocomplétion.

**Analyste / chercheur** — étudie le marché du médicament ou la pénurie de traitements.
Besoin : parcours exhaustif, filtres sur statut de commercialisation, accès aux ruptures de stock
et aux MITM.

**Exploitant de la plateforme** — déploie et supervise l'API. Besoin : déploiement simple,
supervision claire, mises à jour sans intervention, diagnostic rapide.

## 6. Évolutions identifiées (post-v1)

Consignées ici pour ne pas polluer le périmètre v1, mais anticipées dans l'architecture.

- **Diff entre versions du dataset** — le format de snapshot NDJSON est conçu pour rendre ce
  calcul trivial ([ADR 0003](adr/0003-snapshot-ndjson-et-hot-swap-atomique.md)).
- **Export en masse** — NDJSON streamé pour les intégrations qui préfèrent une copie locale.
- **Classification ATC complète** — actuellement disponible seulement pour les MITM.
- **Webhooks** sur changement de dataset ou sur nouvelle rupture de stock.
- **Réplication multi-instance** — l'état étant entièrement dérivé d'un snapshot immuable,
  la mise à l'échelle horizontale est déjà possible ; reste à documenter le partage du volume.

## 7. Contraintes réglementaires et licence

**Licence des données.** La BDPM est diffusée sous **Licence Ouverte / Open Licence (Etalab)**.
La réutilisation est libre, y compris commerciale, sous réserve de **mentionner la paternité** et
la **date de dernière mise à jour**. → L'API doit exposer cette mention dans `/v1/dataset` et dans
la documentation OpenAPI (tâche T-31).

**Statut de dispositif médical.** Une API qui se contente de restituer fidèlement une donnée
publique n'est pas un dispositif médical. En revanche, ajouter une aide à la décision (alerte
d'interaction, suggestion de posologie) ferait basculer le produit sous le règlement (UE) 2017/745.
C'est la raison de fond du non-objectif correspondant au §4.

**Avertissement d'usage.** Toute réponse de l'API doit être considérée comme informative. La
documentation doit porter un avertissement explicite : *les données ne se substituent pas au RCP
ni à l'avis d'un professionnel de santé*. → tâche T-31.

**Données personnelles.** L'API ne traite **aucune donnée de santé à caractère personnel** : elle
ne sert qu'un référentiel de produits. Le RGPD ne s'applique qu'aux logs techniques (adresses IP),
dont la rétention doit être bornée et documentée ([10](10-observabilite.md)).

**Courtoisie envers la source.** L'ANSM ne publie pas de quota, mais le service est financé par
la puissance publique. Le synchroniseur doit : s'identifier par un `User-Agent` explicite,
ne télécharger qu'une fois par jour, appliquer un jitter aléatoire, et ne jamais réessayer en
boucle serrée ([09](09-pipeline-mise-a-jour.md)).

## 8. Critères d'acceptation du projet

Le projet est considéré livré quand, simultanément :

1. Les 7 objectifs O1–O7 sont vérifiés par un test automatisé ou une procédure documentée.
2. Les 7 pièges de la source listés en [01](01-analyse-source-bdpm.md#5-pièges-de-la-source) sont
   couverts par un test de non-régression.
3. `docker compose --profile caddy up -d` puis `--profile traefik` aboutissent tous deux à une
   API fonctionnelle en HTTPS sur une machine vierge.
4. Le test de charge valide les budgets de [07](07-performance.md).
5. `govulncheck` et `trivy` ne remontent aucune vulnérabilité de sévérité haute ou critique.

### Recette du 15/08/2026

| # | Objectif | Vérification | Verdict |
|---|---|---|---|
| O1 | 100 % des données exposées | `TestO1_AtteignabiliteDesChamps` — 74 champs énumérés par réflexion, tous atteints sur 17 routes | **tenu** |
| O2 | Recherche tolérante | `TestLive_RechercheSurDonneesReelles` — `paracetamol`, `paracétamol`, `paracetamo`, `paracetamoll` : 227 résultats, même tête | **tenu** |
| O3 | Mise à jour sans coupure | `reload.js` — bascule réelle pendant 896 094 requêtes, **0 erreur**, 0 réponse incohérente | **tenu** |
| O4 | Budgets de performance | `make bench-budgets` — **les 8 budgets de [07 §2.1](07-performance.md) tenus** sur données réelles, `GOMAXPROCS=2`, 20 000 mesures par opération | **tenu** |
| O5 | Déploiement en une commande | `--profile caddy` et `--profile traefik` : HTTPS servi par l'AC interne, sans étape manuelle | **tenu** |
| O6 | Sécurité par défaut | `TestO6_AucunAccesAnonymeAuxDonnees` — 18 routes `/v1/*` en `401` ; image `nonroot`, `read_only`, sans shell | **tenu** |
| O7 | Contrat explicite | `spectral lint` sans erreur ni avertissement ; 24 routes, 17 schémas | **tenu** |

**Les sept objectifs sont tenus.** O4 a longtemps semblé hors d'atteinte parce qu'il était mesuré
au mauvais endroit : un générateur de charge externe mesure le réseau autant que le serveur, alors
que le budget est explicitement « hors réseau ». Mesuré par appel direct au handler, le budget de
recherche est tenu avec une marge de 42 fois — 71,3 µs pour 3 ms. La mesure a en revanche révélé
un vrai défaut sur le `304`, corrigé (voir [07 §2.1](07-performance.md#21-latence-p99-hors-réseau)).

Les critères 3 et 5 sont tenus ; le critère 2 l'est par les golden files ([T-12](12-backlog-taches.md))
et le fuzzing.

## Glossaire

| Terme | Définition |
|---|---|
| **AMM** | Autorisation de Mise sur le Marché. Autorisation administrative de commercialisation. |
| **ANSM** | Agence Nationale de Sécurité du Médicament et des produits de santé. Producteur de la BDPM. |
| **ASMR** | Amélioration du Service Médical Rendu. Progrès apporté par rapport à l'existant, coté de I (majeur) à V (absence de progrès). Détermine le prix. |
| **ATC** | Anatomical Therapeutic Chemical. Classification internationale des médicaments (OMS). |
| **BDPM** | Base de Données Publique des Médicaments. |
| **CEPS** | Comité Économique des Produits de Santé. Fixe les prix. |
| **CIP7 / CIP13** | Code Identifiant de Présentation, sur 7 ou 13 chiffres. Identifie une **boîte** (le code-barres). |
| **CIS** | Code Identifiant de Spécialité, 8 chiffres. Identifie un **médicament**. Clé pivot de toute la base. |
| **CPD** | Conditions de Prescription et de Délivrance (liste I, liste II, stupéfiants, réservé à l'hôpital…). |
| **Fraction thérapeutique (FT)** | Partie active d'une substance. Ex. : la substance est le *bésilate d'amlodipine*, la fraction thérapeutique est l'*amlodipine*. |
| **Générique** | Médicament de composition identique à un princeps dont le brevet a expiré. |
| **HAS** | Haute Autorité de Santé. Produit les avis SMR et ASMR. |
| **MITM** | Médicament d'Intérêt Thérapeutique Majeur. Son indisponibilité met en jeu le pronostic vital. |
| **Présentation** | Conditionnement commercial d'une spécialité (boîte de 20 comprimés, flacon de 100 ml…). |
| **Princeps** | Médicament original de référence d'un groupe générique. |
| **RCP** | Résumé des Caractéristiques du Produit. Document de référence destiné aux professionnels. |
| **SMR** | Service Médical Rendu. Intérêt clinique : *Important*, *Modéré*, *Faible*, *Insuffisant*. Détermine le taux de remboursement. |
| **Spécialité** | Un médicament identifié par un nom commercial, un dosage et une forme. |
| **Substance active (SA)** | Composant produisant l'effet thérapeutique. |
