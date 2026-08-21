package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// conditionsFields est le nombre de colonnes attendu dans
// CIS_CPD_bdpm.txt (docs/01-analyse-source-bdpm.md §3.8).
const conditionsFields = 2

// Colonnes de CIS_CPD_bdpm.txt, 0-indexées.
const (
	colCondCIS = iota
	colCondCondition
)

// ParseConditions lit CIS_CPD_bdpm.txt déjà décodé et produit les
// conditions de prescription ou de délivrance du snapshot
// (docs/03-modele-de-donnees.md §3.7). Une relation 1-N : un CIS porte
// souvent plusieurs conditions, une par ligne.
func ParseConditions(file string, r io.Reader, quarantine QuarantineFunc) ([]Condition, error) {
	sc := NewScanner(file, r, conditionsFields, quarantine)

	var out []Condition
	for sc.Scan() {
		f := sc.Fields()

		cis := f[colCondCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, strings.Join(f, "\t"))
			continue
		}

		out = append(out, Condition{
			CIS:       strings.Clone(cis),
			Condition: strings.Clone(strings.TrimSpace(f[colCondCondition])),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
