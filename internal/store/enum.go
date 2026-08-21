package store

import (
	"fmt"
	"math"
	"time"
)

// enumTable est une table d'internement bornée à 255 modalités, pour les
// champs d'énumération stockés en uint8.
//
// Elle n'a pas de liste de valeurs figée à la compilation, et c'est
// délibéré : l'ANSM ajoute des modalités sans préavis, et une table close
// obligerait soit à rejeter la valeur — donc à mutiler la donnée — soit à la
// remplacer par un « autre » qui la perdrait. Ici la modalité inconnue
// obtient simplement un code de plus, et le libellé exact est restitué à la
// sérialisation.
//
// Les 255 places sont un plafond confortable : les quatre champs concernés
// comptent respectivement 5, 8, 2 et 3 modalités observées.
type enumTable struct {
	name   string
	values []string
	index  map[string]uint8
}

func newEnumTable(name string) *enumTable {
	return &enumTable{name: name, index: make(map[string]uint8)}
}

// intern retourne le code de s. Le code 0 est réservé à la chaîne vide.
//
// Le débordement est une erreur de construction, jamais une erreur de
// requête : il fait échouer le chargement du snapshot, l'ancien Store
// restant en service.
func (e *enumTable) intern(s string) (uint8, error) {
	if s == "" {
		return 0, nil
	}
	if id, ok := e.index[s]; ok {
		return id, nil
	}
	if len(e.values) == 0 {
		e.values = append(e.values, "")
	}
	if len(e.values) > 255 {
		return 0, fmt.Errorf(
			"énumération %s : plus de 255 modalités distinctes, la source a probablement changé de nature",
			e.name)
	}
	id := uint8(len(e.values)) //nolint:gosec // le débordement au-delà de 255 est refusé trois lignes plus haut
	e.values = append(e.values, s)
	e.index[s] = id
	return id, nil
}

func (e *enumTable) resolve(id uint8) string {
	if int(id) >= len(e.values) {
		return ""
	}
	return e.values[id]
}

func (e *enumTable) freeze() { e.index = nil }

// epoch est l'origine des dates entières du Store.
var epoch = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// dateAbsente est la valeur d'une date non renseignée. Elle est distincte
// de zéro, qui est une date valide (1970-01-01), et le restant très en
// dehors de toute date plausible pour un médicament.
const dateAbsente int32 = -1 << 30

// parseDay convertit une date ISO 8601 en nombre de jours depuis l'epoch.
//
// Stocker un int32 plutôt qu'une chaîne de dix octets économise six octets
// et une indirection par date, sur cinq dates par présentation ou rupture.
// Une date vide donne dateAbsente ; une date illisible aussi, sans faire
// échouer le chargement : le snapshot a déjà été validé à l'ingestion, et
// une date résiduellement corrompue ne doit pas priver l'API de tout le
// reste de l'enregistrement.
func parseDay(iso string) int32 {
	d, _ := ParseDay(iso)
	return d
}

// DateAbsente est la valeur d'une date non renseignée, exposée pour que les
// appelants puissent construire un filtre de date sans réécrire la sentinelle.
const DateAbsente = dateAbsente

// ParseDay est le pendant strict de parseDay : le booléen est faux lorsque la
// date est vide ou illisible.
//
// C'est le point de conversion unique pour les dates **entrantes**, comme
// FormatDay l'est pour les sortantes. L'API l'emploie sur les paramètres de
// requête, où une date illisible doit produire un 400 plutôt que d'être
// silencieusement assimilée à une date absente.
func ParseDay(iso string) (int32, bool) {
	if iso == "" {
		return dateAbsente, false
	}
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return dateAbsente, false
	}
	return int32(t.Sub(epoch).Hours() / 24), true
}

// FormatDay est l'opération inverse, appliquée à la sérialisation. C'est le
// point de conversion unique employé par la couche de sérialisation.
func FormatDay(d int32) string {
	if d == dateAbsente {
		return ""
	}
	return epoch.AddDate(0, 0, int(d)).Format("2006-01-02")
}

// parseDayPtr convertit une date optionnelle.
func parseDayPtr(iso *string) (int32, bool) {
	if iso == nil || *iso == "" {
		return dateAbsente, false
	}
	d := parseDay(*iso)
	return d, d != dateAbsente
}

// parseUint convertit un code numérique en entier non signé. Les codes CIS,
// CIP et de substance de la BDPM sont des entiers, mais la source les
// transporte en chaînes ; la conversion est faite une fois au chargement.
func parseUint(s string) (uint64, bool) {
	if s == "" || len(s) > 20 {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// Conversions bornées.
//
// Les valeurs viennent d'un fichier tiers : elles sont validées à
// l'ingestion, mais cette garantie est distante de plusieurs paquets. La
// rendre **locale** coûte une comparaison et supprime toute possibilité de
// débordement silencieux — un CIS tronqué par un `uint32(…)` non gardé
// désignerait un autre médicament, ce qui est exactement le genre de
// défaillance qu'on ne veut pas sur une donnée de santé.

// toUint32 convertit un entier non signé en uint32, en signalant le
// débordement plutôt qu'en tronquant.
func toUint32(v uint64) (uint32, bool) {
	if v > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

// parseUint32 lit un code numérique et le borne à 32 bits. C'est la porte
// d'entrée unique pour les CIS, CIP7 et codes de substance.
func parseUint32(s string) (uint32, bool) {
	v, ok := parseUint(s)
	if !ok {
		return 0, false
	}
	return toUint32(v)
}

// toInt32 borne un entier signé. Les compteurs concernés (nombre de
// spécialités par substance, ordre dans un groupe, numéro de liaison) valent
// quelques milliers au plus ; un dépassement signalerait une source
// corrompue, et saturer vaut mieux que replier sur une valeur négative.
func toInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	default:
		return int32(v)
	}
}

// toUint8 borne un code d'énumération. Les valeurs admises sont vérifiées à
// l'ingestion ({0,1,2,4} pour les types de générique, {1,2,3,4} pour les
// statuts de rupture) ; hors bornes, 0 est retourné, ce qui se traduira par
// un libellé vide plutôt que par un code fantaisiste.
func toUint8(v int) uint8 {
	if v < 0 || v > math.MaxUint8 {
		return 0
	}
	return uint8(v)
}
