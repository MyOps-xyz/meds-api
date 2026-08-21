package bdpm

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// groupesFields est le nombre de colonnes attendu dans CIS_GENER_bdpm.txt
// (docs/01-analyse-source-bdpm.md §3.7).
const groupesFields = 5

// Colonnes de CIS_GENER_bdpm.txt, 0-indexées.
const (
	colGroupeID = iota
	colGroupeLibelle
	colGroupeCIS
	colGroupeType
	colGroupeOrdre
)

// ParseGroupes lit CIS_GENER_bdpm.txt déjà décodé et produit les
// appartenances brutes à un groupe générique (une par ligne). La
// dénormalisation en groupes portant leurs membres
// (docs/03-modele-de-donnees.md §3.5) est le rôle de T-10 ; ce parseur se
// limite à valider et normaliser chaque ligne.
//
// Le type de générique doit être l'une des valeurs {0, 1, 2, 4} : la
// valeur 3 n'existe pas, l'énumération est discontinue
// (docs/01-analyse-source-bdpm.md §3.7). Un parseur qui accepterait un
// intervalle 0-4 continu laisserait passer une valeur inexistante ; celui-ci
// met la ligne en quarantaine à la place.
func ParseGroupes(file string, r io.Reader, quarantine QuarantineFunc) ([]GroupeAppartenance, error) {
	sc := NewScanner(file, r, groupesFields, quarantine)

	var out []GroupeAppartenance
	for sc.Scan() {
		f := sc.Fields()
		raw := strings.Join(f, "\t")

		cis := f[colGroupeCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, raw)
			continue
		}

		typeRaw := strings.TrimSpace(f[colGroupeType])
		typeVal, err := strconv.Atoi(typeRaw)
		if err != nil {
			quarantine(file, sc.Line(), ReasonTypeGeneriqueInconnu, raw)
			continue
		}
		typeLibelle, ok := genericTypeLabel(typeVal)
		if !ok {
			quarantine(file, sc.Line(), ReasonTypeGeneriqueInconnu, raw)
			continue
		}

		ordreRaw := strings.TrimSpace(f[colGroupeOrdre])
		ordre, err := strconv.Atoi(ordreRaw)
		if err != nil {
			quarantine(file, sc.Line(), ReasonOrdreInvalide, raw)
			continue
		}

		out = append(out, GroupeAppartenance{
			GroupeID:    strings.Clone(strings.TrimSpace(f[colGroupeID])),
			Libelle:     strings.Clone(strings.TrimSpace(f[colGroupeLibelle])),
			CIS:         strings.Clone(cis),
			Type:        typeVal,
			TypeLibelle: typeLibelle,
			Ordre:       ordre,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
