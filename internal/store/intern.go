// Package store est la représentation en mémoire du jeu de données
// (docs/03-modele-de-donnees.md §4).
//
// Le snapshot est optimisé pour l'humain, le Store pour le processeur : la
// conversion a lieu une fois au chargement, jamais sur le chemin d'une
// requête. Une fois Build terminé, aucun champ du Store n'est modifié —
// c'est cette immuabilité, et non un verrou, qui rend la lecture concurrente
// correcte (docs/02-architecture.md §5).
package store

import (
	"fmt"
	"math"
	"strings"
)

// Interner attribue un identifiant compact et stable à chaque chaîne
// distincte d'un champ à faible cardinalité.
//
// 367 formes pharmaceutiques pour 15 857 spécialités : stocker la chaîne
// 15 857 fois est un gaspillage pur. Un uint16 et une table de
// correspondance suffisent, avec le bénéfice annexe que comparer deux formes
// devient une comparaison d'entiers.
//
// La table est construite au chargement puis figée : Intern n'est jamais
// appelée après Build, et Resolve est sans verrou.
type Interner struct {
	values []string
	// norms est la forme normalisée de values, parallèle et de même taille.
	// Renseignée par FreezeNormalized, nil pour les tables dont aucun filtre
	// n'a besoin.
	norms []string
	index map[string]uint16
	// overflowed retient qu'une valeur n'a pas pu être internée faute de
	// place. Le débordement n'est pas signalé au point d'appel — Intern est
	// invoquée des milliers de fois et propager une erreur à chaque fois
	// alourdirait tout le chemin de construction pour un cas qui ne doit
	// jamais se produire — mais il fait échouer Build, bruyamment.
	overflowed bool
	name       string
}

// NewInterner construit une table vide. name identifie le champ dans les
// messages d'erreur.
func NewInterner(name string) *Interner {
	return &Interner{index: make(map[string]uint16), name: name}
}

// Intern retourne l'identifiant de s, en l'ajoutant si nécessaire.
//
// L'identifiant 0 est réservé à la chaîne vide, de sorte qu'un champ absent
// se lise naturellement comme un zéro : c'est ce qui permet à Spec de rester
// utilisable sans initialisation explicite de chacun de ses champs internés.
func (in *Interner) Intern(s string) uint16 {
	if s == "" {
		return 0
	}
	if id, ok := in.index[s]; ok {
		return id
	}
	if len(in.values) == 0 {
		in.values = append(in.values, "")
	}
	// 65 535 valeurs distinctes au plus. Sans ce garde-fou, la 65 536ᵉ
	// repartirait à zéro et **tous** les libellés de ce champ pointeraient
	// vers la mauvaise chaîne — une corruption silencieuse, jamais un
	// plantage. Les cardinalités observées (367 formes, 68 voies, 666
	// titulaires) laissent une marge de deux ordres de grandeur, mais la
	// source est un fichier tiers : la marge n'est pas une garantie.
	if len(in.values) > math.MaxUint16 {
		in.overflowed = true
		return 0
	}
	id := uint16(len(in.values)) //nolint:gosec // le débordement au-delà de 65 535 est refusé juste au-dessus
	in.values = append(in.values, s)
	in.index[s] = id
	return id
}

// Err rapporte un débordement de la table. Vérifiée à la fin de la
// construction du Store, elle fait échouer le chargement plutôt que de
// publier un Store dont les libellés seraient faux.
func (in *Interner) Err() error {
	if in.overflowed {
		return fmt.Errorf(
			"internement %s : plus de %d valeurs distinctes, la source a probablement changé de nature",
			in.name, math.MaxUint16)
	}
	return nil
}

// Resolve retourne la chaîne d'un identifiant. Un identifiant hors bornes
// retourne la chaîne vide plutôt que de paniquer : le Store est servi à des
// requêtes publiques, et une incohérence interne ne doit jamais dégénérer en
// interruption de service.
func (in *Interner) Resolve(id uint16) string {
	if int(id) >= len(in.values) {
		return ""
	}
	return in.values[id]
}

// Len est le nombre de valeurs distinctes internées, chaîne vide comprise
// lorsqu'au moins une valeur a été internée.
func (in *Interner) Len() int { return len(in.values) }

// Values expose la table pour les tests et les métriques. La slice est
// partagée : elle ne doit pas être modifiée.
// Freeze libère l'index de construction, inutile une fois le Store bâti.
// Le gain est modeste (quelques dizaines de kilooctets) mais il supprime
// surtout toute possibilité d'interner après Build, ce qui muterait un Store
// publié.
func (in *Interner) Freeze() { in.index = nil }

// FreezeNormalized fige la table et précalcule la forme normalisée de chaque
// valeur.
//
// La normalisation d'un champ interné appartient à l'ingestion, pas au
// filtrage : sans cette table, un filtre sur titulaire normaliserait la même
// poignée de raisons sociales une fois par spécialité candidate, soit des
// dizaines de milliers de décompositions NFD par requête pour quelques
// milliers de valeurs distinctes.
func (in *Interner) FreezeNormalized() {
	in.norms = make([]string, len(in.values))
	for i, v := range in.values {
		in.norms[i] = Normalize(v)
	}
	in.Freeze()
}

// AnyNormalizedContains teste si l'une des valeurs désignées contient needle,
// déjà normalisé. N'alloue pas, contrairement à un resolveAll suivi d'un
// parcours.
func (in *Interner) AnyNormalizedContains(ids []uint16, needle string) bool {
	for _, id := range ids {
		if int(id) < len(in.norms) && strings.Contains(in.norms[id], needle) {
			return true
		}
	}
	return false
}

// AnyEqualFold teste si l'une des valeurs désignées égale needle, à la casse
// près. N'alloue pas.
func (in *Interner) AnyEqualFold(ids []uint16, needle string) bool {
	for _, id := range ids {
		if strings.EqualFold(in.Resolve(id), needle) {
			return true
		}
	}
	return false
}

// internAll interne une liste de chaînes en une slice d'identifiants.
func (in *Interner) internAll(ss []string) []uint16 {
	if len(ss) == 0 {
		return nil
	}
	out := make([]uint16, len(ss))
	for i, s := range ss {
		out[i] = in.Intern(s)
	}
	return out
}

// resolveAll est l'opération inverse, utilisée à la sérialisation.
func (in *Interner) resolveAll(ids []uint16) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = in.Resolve(id)
	}
	return out
}
