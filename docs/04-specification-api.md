# 04 — Spécification de l'API

> Ce document est la **source de vérité fonctionnelle**. Le fichier `api/openapi.yaml` en est la
> transcription formelle, embarquée dans le binaire via `go:embed` et servie sur `/openapi.json`.
> En cas de divergence, ce document fait foi et l'OpenAPI doit être corrigé (T-33).

## 1. Conventions générales

| Aspect | Règle |
|---|---|
| Base | `/v1` — la version est dans le chemin |
| Méthodes | `GET` uniquement, sauf `POST /admin/sync` |
| Content-Type | `application/json; charset=utf-8` |
| Erreurs | `application/problem+json` — **RFC 9457** |
| Dates | ISO 8601 (`2011-03-16`) ; horodatages en RFC 3339 UTC |
| Montants | entiers en **centimes**, suffixés `_cents` (voir [03](03-modele-de-donnees.md#32-presentationsndjson)) |
| Absence de valeur | `null`, jamais `""` ni `0` |
| Casse des champs | `snake_case` |
| Langue des valeurs | valeurs métier **conservées telles quelles** (`Autorisation active`) |
| Pagination | curseur opaque |
| Cache | `ETag` + `If-None-Match` → `304` ; `Cache-Control: public, max-age=3600` |
| Compression | `gzip` si `Accept-Encoding` le propose et corps > 1 Kio |

### 1.1 Authentification

Toutes les routes `/v1/*` exigent une clé API :

```http
GET /v1/medicaments?q=doliprane HTTP/1.1
Authorization: Bearer mk_live_7f3a9c2e8b1d4f6a0c5e2b8d7a1f4c9e
```

Exemptés : `/healthz`, `/readyz`, `/metrics`, `/openapi.json`, `/docs`.
`POST /admin/sync` exige la clé d'administration, distincte. Détails : [06](06-securite.md).

### 1.2 Pagination

Curseur opaque, préféré à `offset`/`limit` pour deux raisons : il reste cohérent si le dataset
est rechargé pendant un parcours, et il ne dégrade pas sur les grands décalages.

```
GET /v1/medicaments?q=paracetamol&limit=50
GET /v1/medicaments?q=paracetamol&limit=50&cursor=eyJvIjo1MCwiaCI6IjNmMmEifQ
```

Le curseur encode en base64url `{offset, hash_dataset}`. **Si le dataset a changé entre deux
pages, le curseur est rejeté** avec `400 cursor_stale` : mieux vaut une erreur explicite qu'une
pagination silencieusement incohérente.

`limit` : défaut 20, maximum 100 (200 pour `/v1/suggest`). Au-delà, `400`.

```json
{
  "data": [ /* … */ ],
  "meta": { "total": 231, "limit": 50, "has_more": true,
            "next_cursor": "eyJvIjo1MCwiaCI6IjNmMmEifQ",
            "dataset_version": "2026-08-14T04:12:33Z" }
}
```

### 1.3 Format d'erreur (RFC 9457)

```json
{
  "type": "https://meds-api.example/errors/not_found",
  "title": "Ressource introuvable",
  "status": 404,
  "detail": "Aucune spécialité ne porte le code CIS 99999999.",
  "instance": "/v1/medicaments/99999999",
  "request_id": "01J8Z9K2M3N4P5Q6R7S8T9V0W1"
}
```

| Statut | `type` | Cause |
|---|---|---|
| 400 | `invalid_parameter` | Paramètre absent, mal formé ou hors bornes |
| 400 | `cursor_stale` | Curseur émis pour une autre version du dataset |
| 401 | `unauthorized` | Clé absente ou invalide |
| 404 | `not_found` | Ressource inexistante |
| 406 | `not_acceptable` | `Accept` incompatible avec `application/json` |
| 429 | `rate_limited` | Quota dépassé — `Retry-After` fourni |
| 500 | `internal_error` | Anomalie serveur — `request_id` à communiquer |
| 503 | `not_ready` | Aucun dataset chargé (démarrage ou synchronisation en échec) |

`request_id` est systématiquement présent et renvoyé dans l'en-tête `X-Request-Id`.

---

## 2. Endpoints

### 2.1 `GET /v1/medicaments` — recherche et filtres

| Paramètre | Type | Défaut | Description |
|---|---|---|---|
| `q` | string | — | Recherche plein-texte sur dénomination, substances et titulaire |
| `commercialise` | bool | — | `true` ⇒ `Commercialisée` uniquement |
| `statut_amm` | enum | — | `active`, `abrogee`, `archivee`, `retiree`, `suspendue` |
| `procedure_amm` | enum | — | `nationale`, `centralisee`, `decentralisee`, `reconnaissance_mutuelle`, `homeo`, `phyto`, `importation_parallele` |
| `forme` | string | — | Forme pharmaceutique (correspondance exacte, insensible à la casse) |
| `voie` | string | — | Voie d'administration (`orale`, `cutanée`…) |
| `titulaire` | string | — | Titulaire de l'AMM |
| `substance` | string | — | Code ou dénomination de substance |
| `surveillance` | bool | — | Surveillance renforcée (triangle noir) |
| `mitm` | bool | — | Médicament d'intérêt thérapeutique majeur |
| `generique` | enum | — | `princeps`, `generique`, `complementarite`, `substituable` |
| `remboursable` | bool | — | Au moins une présentation avec un taux de remboursement |
| `rupture` | bool | — | Fait l'objet d'une fiche de disponibilité active |
| `sort` | enum | `pertinence` | `pertinence` (si `q`), `denomination`, `date_amm` |
| `limit` / `cursor` | | 20 | Pagination |

Les filtres se combinent en **ET**. Sans `q`, le tri par pertinence bascule sur `denomination`.

```http
GET /v1/medicaments?q=paracetamol&commercialise=true&limit=2
```

```json
{
  "data": [
    {
      "cis": "60234100",
      "denomination": "DOLIPRANE 1000 mg, comprimé",
      "forme_pharmaceutique": "comprimé",
      "voies_administration": ["orale"],
      "statut_amm": "Autorisation active",
      "etat_commercialisation": "Commercialisée",
      "date_amm": "1995-06-12",
      "titulaires": ["OPELLA HEALTHCARE FRANCE"],
      "surveillance_renforcee": false,
      "substances": ["PARACÉTAMOL"],
      "nb_presentations": 3,
      "mitm": false,
      "score": 18.42,
      "_links": { "self": "/v1/medicaments/60234100",
                  "presentations": "/v1/medicaments/60234100/presentations" }
    }
  ],
  "meta": { "total": 231, "limit": 2, "has_more": true,
            "next_cursor": "eyJvIjoyLCJoIjoiM2YyYSJ9",
            "dataset_version": "2026-08-14T04:12:33Z" }
}
```

La représentation en liste est **allégée** (pas de composition ni d'avis) : la liste sert à
choisir, la fiche à consulter. `score` n'apparaît que si `q` est fourni.

### 2.2 `GET /v1/medicaments/{cis}` — fiche

| Paramètre | Description |
|---|---|
| `include` | Liste séparée par des virgules : `presentations`, `composition`, `generiques`, `avis`, `conditions`, `ruptures`, ou `all` |

Sans `include`, seule la spécialité est renvoyée. Ce choix est délibéré : la fiche complète d'un
médicament à 37 présentations et 40 avis SMR pèse plusieurs dizaines de kilooctets, inutiles pour
un appelant qui ne veut qu'un libellé.

```http
GET /v1/medicaments/60234100?include=presentations,composition
```

```json
{
  "data": {
    "cis": "60234100",
    "denomination": "DOLIPRANE 1000 mg, comprimé",
    "forme_pharmaceutique": "comprimé",
    "voies_administration": ["orale"],
    "statut_amm": "Autorisation active",
    "procedure_amm": "Procédure nationale",
    "etat_commercialisation": "Commercialisée",
    "date_amm": "1995-06-12",
    "statut_bdm": null,
    "numero_autorisation_europeenne": null,
    "titulaires": ["OPELLA HEALTHCARE FRANCE"],
    "surveillance_renforcee": false,
    "mitm": { "code_atc": "N02BE01" },
    "presentations": [
      { "cip13": "3400936030497", "cip7": "3603049",
        "libelle": "plaquette(s) thermoformée(s) de 8 comprimé(s)",
        "etat_commercialisation": "Déclaration de commercialisation",
        "agrement_collectivites": true, "taux_remboursement": [65],
        "prix_medicament_cents": 178, "prix_public_cents": 280,
        "honoraires_dispensation_cents": 102 }
    ],
    "composition": [
      { "element_pharmaceutique": "comprimé", "code_substance": "02202",
        "denomination_substance": "PARACÉTAMOL", "dosage": "1000,00 mg",
        "reference_dosage": "un comprimé", "nature": "SA", "numero_liaison": 1 }
    ]
  },
  "meta": { "dataset_version": "2026-08-14T04:12:33Z" }
}
```

`404` si le CIS est inconnu. `400` si le CIS n'est pas composé de 8 chiffres.

### 2.3 Sous-ressources d'un médicament

| Endpoint | Renvoie |
|---|---|
| `GET /v1/medicaments/{cis}/presentations` | Présentations, prix, remboursement |
| `GET /v1/medicaments/{cis}/composition` | Substances actives et fractions thérapeutiques |
| `GET /v1/medicaments/{cis}/generiques` | Groupe générique et médicaments équivalents |
| `GET /v1/medicaments/{cis}/avis` | Avis SMR et ASMR avec liens vers la HAS |
| `GET /v1/medicaments/{cis}/conditions` | Conditions de prescription et de délivrance |

Toutes renvoient `404` si le CIS n'existe pas, et un tableau **vide** (`200`) si le CIS existe
sans donnée associée. Cette distinction compte : 1 248 spécialités n'ont aucune présentation.

`/generiques` expose le groupe **et ses autres membres**, ce qui répond directement à la question
métier « par quoi puis-je substituer ce médicament ? » :

```json
{
  "data": {
    "groupe_id": "1", "libelle": "CIMETIDINE 200 mg - TAGAMET 200 mg, comprimé pelliculé",
    "type": "generique", "type_code": 1, "ordre": 2,
    "membres": [
      { "cis": "65383183", "denomination": "TAGAMET 200 mg, comprimé pelliculé",
        "type": "princeps", "type_code": 0, "etat_commercialisation": "Non commercialisée" }
    ]
  }
}
```

`/avis` regroupe les deux échelles, triées par date décroissante :

```json
{
  "data": {
    "smr": [ { "code_dossier_has": "CT-4521", "motif_evaluation": "Renouvellement d'inscription",
               "date_avis": "2019-03-06", "valeur": "Important",
               "libelle": "Le service médical rendu par …",
               "lien_avis_ct": "https://www.has-sante.fr/…" } ],
    "asmr": [ { "code_dossier_has": "CT-4521", "date_avis": "2019-03-06",
                "valeur": "V", "niveau": 5,
                "libelle": "Cette spécialité n'apporte pas d'amélioration …",
                "lien_avis_ct": "https://www.has-sante.fr/…" } ]
  }
}
```

### 2.4 `GET /v1/presentations/{cip}` — lookup par code-barres

`{cip}` accepte indifféremment un **CIP7** (7 chiffres) ou un **CIP13** (13 chiffres) ; la
distinction se fait sur la longueur. C'est l'endpoint du scan de code-barres en officine.

```json
{
  "data": {
    "cip13": "3400936030497", "cip7": "3603049",
    "libelle": "plaquette(s) thermoformée(s) de 8 comprimé(s)",
    "statut_administratif": "Présentation active",
    "etat_commercialisation": "Déclaration de commercialisation",
    "date_declaration": "2011-03-16",
    "agrement_collectivites": true,
    "taux_remboursement": [65],
    "prix_medicament_cents": 178, "prix_public_cents": 280,
    "honoraires_dispensation_cents": 102,
    "indications_remboursement": null,
    "medicament": { "cis": "60234100", "denomination": "DOLIPRANE 1000 mg, comprimé",
                    "_link": "/v1/medicaments/60234100" }
  }
}
```

Le médicament parent est **inclus d'office** : après un scan, l'appelant veut toujours savoir de
quel médicament il s'agit. Lui imposer un second appel serait un défaut de conception.

`400` si le code ne fait ni 7 ni 13 chiffres.

### 2.5 Substances

| Endpoint | Description |
|---|---|
| `GET /v1/substances?q=&limit=&cursor=` | Référentiel des 3 895 substances |
| `GET /v1/substances/{code}` | Une substance et son nombre de spécialités |
| `GET /v1/substances/{code}/medicaments` | Spécialités contenant cette substance |

```json
{ "data": { "code": "02202", "denomination": "PARACÉTAMOL", "nb_specialites": 230,
            "_links": { "medicaments": "/v1/substances/02202/medicaments" } } }
```

### 2.6 Groupes génériques

`GET /v1/groupes-generiques?q=&limit=&cursor=` et `GET /v1/groupes-generiques/{id}`.
Le détail inclut tous les membres avec leur type et leur état de commercialisation.

### 2.7 `GET /v1/ruptures` — disponibilité

| Paramètre | Description |
|---|---|
| `statut` | `rupture`, `tension`, `arret`, `remise_disposition` |
| `actives` | `true` (défaut) ⇒ exclut les remises à disposition passées |
| `cis` | Filtre sur un médicament |
| `depuis` | Date ISO ⇒ fiches mises à jour depuis |

```json
{
  "data": [
    { "cis": "62000612", "cip13": null,
      "denomination": "AMIKACINE VIATRIS 50 mg/1 ml, solution injectable",
      "statut": "tension_approvisionnement", "code_statut": 2,
      "libelle": "Tension d'approvisionnement",
      "date_debut": "2026-07-30", "date_debut_approximative": false,
      "date_mise_a_jour": "2026-07-31", "date_remise_disposition": null,
      "lien_ansm": "https://ansm.sante.fr/…" }
  ]
}
```

`libelle` est **dérivé du code**, jamais repris du fichier source (piège P5). `cip13: null`
signifie que toute la spécialité est concernée.

### 2.8 `GET /v1/mitm` — intérêt thérapeutique majeur

Filtres : `atc` (préfixe de code ATC, ex. `N02`), `q`, pagination.

### 2.9 `GET /v1/suggest` — autocomplétion

Endpoint distinct de `/v1/medicaments`, optimisé pour la frappe au kilomètre : préfixes
uniquement, charge utile minimale, budget de latence < 1 ms.

```http
GET /v1/suggest?q=dolip&limit=5
```

```json
{
  "data": [
    { "type": "medicament", "cis": "60234100",
      "label": "DOLIPRANE 1000 mg, comprimé", "highlight": "<em>DOLIP</em>RANE 1000 mg, comprimé" },
    { "type": "substance", "code": "02202", "label": "PARACÉTAMOL" }
  ]
}
```

`type` vaut `medicament` ou `substance`. Le champ `highlight` est **échappé en HTML** avant
insertion des balises `<em>` : la donnée source contient des caractères actifs.

### 2.10 `GET /v1/dataset` — état et provenance

Endpoint d'observabilité métier, et support de la **conformité Licence Ouverte** (mention de la
source et de la date de mise à jour).

```json
{
  "data": {
    "version": "2026-08-14T04:12:33Z",
    "hash": "3f2a9c…",
    "generated_at": "2026-08-14T04:12:33Z",
    "age_seconds": 46821,
    "source": {
      "name": "Base de données publique des médicaments (BDPM)",
      "publisher": "ANSM — Agence nationale de sécurité du médicament et des produits de santé",
      "url": "https://base-donnees-publique.medicaments.gouv.fr/",
      "licence": "Licence Ouverte / Open Licence (Etalab)"
    },
    "counts": { "specialites": 15857, "presentations": 20903, "composants": 32420,
                "substances": 3895, "groupes_generiques": 1671, "ruptures": 640, "mitm": 7711 },
    "quarantine": { "total": 4, "reject_ratio": 0.000026,
                    "by_reason": { "cis_orphelin": 4 } },
    "files": [ { "name": "CIS_bdpm.txt", "sha256": "a1b2…", "bytes": 3168771,
                 "lines": 15857, "encoding": "windows-1252" } ],
    "last_sync": { "started_at": "2026-08-14T04:12:01Z", "duration_ms": 32411,
                   "status": "success", "changed": true }
  }
}
```

### 2.11 Exploitation

| Endpoint | Auth | Réponse |
|---|---|---|
| `GET /healthz` | non | `200` dès que le processus vit |
| `GET /readyz` | non | `200` si un dataset est chargé, sinon `503` |
| `GET /metrics` | non* | Métriques Prometheus |
| `GET /openapi.json` | non | Contrat OpenAPI 3.1 |
| `GET /docs` | non | Documentation interactive (Scalar embarqué) |
| `POST /admin/sync` | clé admin | Déclenche une synchronisation |

\* `/metrics` ne doit pas être exposé publiquement : le reverse proxy le restreint au réseau
interne ([08](08-deploiement-docker.md)).

`POST /admin/sync` est **asynchrone** : il répond `202 Accepted` immédiatement et renvoie `409`
si une synchronisation est déjà en cours. Une ingestion prend une trentaine de secondes ; tenir
la connexion ouverte inviterait aux dépassements de délai côté proxy.

```json
{ "data": { "status": "accepted", "sync_id": "01J8Z9K2M3N4P5Q6R7S8T9V0W1" } }
```

---

## 3. Mise en cache

L'`ETag` est le SHA-256 tronqué de `hash_dataset + méthode + chemin + paramètres normalisés`.
Le dataset ne changeant qu'une fois par jour, un client bien élevé obtient un `304` sur la quasi-
totalité de ses requêtes répétées.

```http
GET /v1/medicaments/60234100 HTTP/1.1
If-None-Match: "a3f9c2e1b8d47f60"

HTTP/1.1 304 Not Modified
ETag: "a3f9c2e1b8d47f60"
Cache-Control: public, max-age=3600
```

Les paramètres sont **normalisés avant hachage** (tri des clés, casse, valeurs par défaut
explicitées) afin que `?limit=20&q=x` et `?q=x` produisent le même `ETag`.

## 4. Versionnement

`/v1` est un contrat stable. Sont considérés **non cassants** : l'ajout d'un champ, d'un endpoint,
d'un paramètre optionnel, ou d'une valeur à une énumération alimentée par la source BDPM.

Sont **cassants**, et imposeraient `/v2` : le retrait ou le renommage d'un champ, le changement de
type d'un champ, la modification du sens d'un paramètre.

> Les consommateurs doivent donc **tolérer les champs inconnus** et **ne pas supposer close** une
> énumération issue de la BDPM : l'ANSM a déjà fait évoluer ses valeurs
> ([01](01-analyse-source-bdpm.md#4-écarts-entre-documentation-officielle-et-donnée-réelle)).

## 5. Limites

| Élément | Valeur |
|---|---:|
| `limit` maximum | 100 (200 sur `/v1/suggest`) |
| Longueur de `q` | 200 caractères |
| Taille d'URL | 4 Kio |
| Corps de requête | 8 Kio (`POST /admin/sync`) |
| Délai par requête | 5 s |
| Débit par défaut | 100 req/s, rafale 200 |

## 6. Avertissement obligatoire

Servi dans la description OpenAPI et sur `/docs` (tâche T-31) :

> Les données proviennent de la Base de données publique des médicaments (ANSM), diffusée sous
> Licence Ouverte (Etalab). Elles sont fournies à titre informatif et **ne se substituent ni au
> Résumé des Caractéristiques du Produit, ni à l'avis d'un professionnel de santé**. Cette API ne
> constitue pas un dispositif médical et n'apporte aucune aide à la décision clinique. La date de
> mise à jour du jeu de données est exposée sur `/v1/dataset`.
