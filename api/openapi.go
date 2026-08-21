// Package openapi embarque le contrat OpenAPI du service.
//
// Le contrat vit ici, à l'emplacement conventionnel `api/`, et en un seul
// exemplaire. La première rédaction en gardait une copie dans internal/api,
// parce que go:embed ne peut pas remonter au-dessus de son propre paquet ;
// un test et une cible Make tenaient les deux fichiers alignés. Deux copies
// versionnées d'un même contrat restent une verrue : il suffit que `api/`
// soit lui-même un paquet Go pour que la copie disparaisse.
//
// Le nom de paquet diffère volontairement du répertoire : internal/api est
// déjà le paquet `api`, et importer les deux exigerait sinon un alias à
// chaque usage.
package openapi

import _ "embed"

// Spec est le contrat OpenAPI 3.1, au format YAML.
//
// Il est servi tel quel — converti en JSON — sur /openapi.json, et sert de
// source à la page /docs. L'embarquer plutôt que le lire sur disque garantit
// qu'il ne peut ni manquer ni diverger de la version déployée : l'image
// distroless ne contient que le binaire.
//
//go:embed openapi.yaml
var Spec []byte
