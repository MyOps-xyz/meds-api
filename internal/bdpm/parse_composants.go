package bdpm

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// composantsFields est le nombre de colonnes attendu dans
// CIS_COMPO_bdpm.txt (docs/01-analyse-source-bdpm.md §3.3).
const composantsFields = 8

// Colonnes de CIS_COMPO_bdpm.txt, 0-indexées.
const (
	colCompoCIS = iota
	colCompoElementPharmaceutique
	colCompoCodeSubstance
	colCompoDenominationSubstance
	colCompoDosage
	colCompoReferenceDosage
	colCompoNature
	colCompoNumeroLiaison
)

// ParseComposants lit CIS_COMPO_bdpm.txt déjà décodé et produit les
// composants du snapshot (docs/03-modele-de-donnees.md §3.3).
//
// Dosage est conservé tel quel, sans normalisation : il mêle des notations
// métriques et homéopathiques, et toute interprétation serait un risque
// sur une donnée de santé. Nature est normalisée via normalizeNature :
// « ST », annoncé par la documentation officielle mais absent de la
// donnée mesurée, est accepté et converti en « FT » (piège P3).
func ParseComposants(file string, r io.Reader, quarantine QuarantineFunc) ([]Composant, error) {
	sc := NewScanner(file, r, composantsFields, quarantine)

	var out []Composant
	for sc.Scan() {
		f := sc.Fields()
		raw := strings.Join(f, "\t")

		cis := f[colCompoCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, raw)
			continue
		}

		nature, ok := normalizeNature(f[colCompoNature])
		if !ok {
			quarantine(file, sc.Line(), ReasonNatureInvalide, raw)
			continue
		}

		numLiaisonRaw := strings.TrimSpace(f[colCompoNumeroLiaison])
		numLiaison, err := strconv.Atoi(numLiaisonRaw)
		if err != nil {
			quarantine(file, sc.Line(), ReasonNumeroLiaisonInvalide, raw)
			continue
		}

		out = append(out, Composant{
			CIS:                   strings.Clone(cis),
			ElementPharmaceutique: strings.Clone(strings.TrimSpace(f[colCompoElementPharmaceutique])),
			CodeSubstance:         strings.Clone(strings.TrimSpace(f[colCompoCodeSubstance])),
			DenominationSubstance: strings.Clone(strings.TrimSpace(f[colCompoDenominationSubstance])),
			Dosage:                strings.Clone(f[colCompoDosage]),
			ReferenceDosage:       strings.Clone(strings.TrimSpace(f[colCompoReferenceDosage])),
			Nature:                nature,
			NumeroLiaison:         numLiaison,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
