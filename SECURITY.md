# Politique de sécurité

## Versions supportées

Le projet suit le versionnage sémantique. Seule la dernière version mineure publiée reçoit des
correctifs de sécurité.

| Version | Supportée |
|---|---|
| dernière `v0.x` publiée | oui |
| versions antérieures | non |

## Signaler une vulnérabilité

**N'ouvrez pas d'issue publique.** Utilisez le canal privé de GitHub :

> [Signaler une vulnérabilité](https://github.com/MyOps-xyz/meds-api/security/advisories/new)

Ce formulaire crée un avis de sécurité privé, visible des seuls mainteneurs, et permet de préparer
un correctif avant toute divulgation.

Merci d'inclure autant que possible :

- la version affectée (`meds-api --version`) et le mode de déploiement (binaire, image Docker,
  profil `caddy` / `traefik` / frontal mutualisé) ;
- la configuration pertinente, **clés API expurgées** ;
- les étapes de reproduction, ou une requête HTTP minimale ;
- l'impact estimé : lecture de données non autorisée, contournement d'authentification, déni de
  service, exécution de code.

### Délais

| Étape | Délai visé |
|---|---|
| Accusé de réception | 72 heures |
| Évaluation initiale et qualification | 7 jours |
| Correctif ou plan d'atténuation | 90 jours au plus |

La divulgation publique se fait après publication du correctif, via un avis de sécurité GitHub. Le
signalement est crédité, sauf demande contraire.

## Périmètre

### Dans le périmètre

- Contournement de l'authentification par clé API ou de la protection de `POST /admin/sync`.
- Contournement de la limitation de débit permettant un déni de service.
- Fuite d'un secret — clé API, clé d'administration — dans une réponse, un journal ou une métrique.
- Exposition de `/metrics` ou de `/debug/*` par les configurations de frontal fournies dans
  [`deploy/`](deploy/).
- Injection, traversée de chemin, ou plantage déclenchable à distance par une requête HTTP.
- Empoisonnement du pipeline d'ingestion menant à l'exécution de code ou à une consommation
  mémoire non bornée.
- Faiblesse de durcissement du conteneur : élévation de privilèges, échappement, écriture hors
  volumes prévus.

### Hors périmètre

- **Exactitude des données médicales.** L'API restitue fidèlement les fichiers publiés par l'ANSM.
  Une erreur présente à la source n'est pas une vulnérabilité ; elle relève de l'ANSM.
- Absence d'en-tête de sécurité sur un déploiement qui n'emploie pas les frontaux fournis.
- Déni de service exigeant un volume de trafic disproportionné, ou obtenu depuis une clé API
  légitime au-delà des quotas configurés.
- Résultats bruts de scanners automatiques, sans démonstration d'impact.
- Vulnérabilités d'une dépendance déjà signalées par `govulncheck` et sans correctif amont — la CI
  les surveille déjà à chaque exécution.

## Modèle de menace

Les hypothèses de sécurité, le modèle d'authentification, la comparaison en temps constant des
clés et les mesures de durcissement du conteneur sont documentés dans
[`docs/06-securite.md`](docs/06-securite.md). Une contribution touchant à ces mécanismes est
attendue avec la mise à jour correspondante de ce document.

## Bonnes pratiques d'exploitation

- Générer les clés avec 32 octets d'entropie et les faire tourner régulièrement.
- Ne jamais réutiliser une clé API comme `MEDS_ADMIN_KEY` — la configuration le refuse au
  démarrage.
- Laisser `MEDS_CORS_ORIGINS` vide si aucun navigateur n'appelle l'API ; le joker `*` est refusé.
- N'exposer `/metrics` que sur un réseau privé : les frontaux fournis le refusent
  inconditionnellement depuis l'extérieur.
- Suivre les mises à jour : la CI exécute `govulncheck` et Trivy à chaque commit, et Dependabot
  ouvre les montées de version hebdomadairement.
