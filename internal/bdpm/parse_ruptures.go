package bdpm

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// rupturesFields est le nombre de colonnes attendu dans
// CIS_CIP_Dispo_Spec.txt (docs/01-analyse-source-bdpm.md §3.9).
const rupturesFields = 8

// Colonnes de CIS_CIP_Dispo_Spec.txt, 0-indexées.
const (
	colRuptCIS = iota
	colRuptCIP13
	colRuptCodeStatut
	colRuptLibelleStatut
	colRuptDateDebut
	colRuptDateMiseAJour
	colRuptDateRemiseDisposition
	colRuptLienANSM
)

// ParseRuptures lit CIS_CIP_Dispo_Spec.txt déjà décodé et produit les
// ruptures et tensions d'approvisionnement du snapshot
// (docs/03-modele-de-donnees.md §3.7).
//
// Statut et Libelle sont dérivés de CodeStatut (ruptureStatus), jamais du
// libellé source qui est incohérent — piège P5,
// docs/01-analyse-source-bdpm.md §5 : le même code y porte des libellés
// différents selon la ligne (apostrophe perdue, casse erratique).
// LibelleSource conserve la valeur brute pour l'audit. CIP13 vide devient
// nil : cela signifie que toute la spécialité est concernée, pas qu'on
// ignore laquelle. DateDebutApproximative vaut true pour toute fiche dont
// la date de début est antérieure au 06/10/2023, l'ANSM documentant que
// cette date est alors en réalité la date de mise à jour.
func ParseRuptures(file string, r io.Reader, quarantine QuarantineFunc) ([]Rupture, error) {
	sc := NewScanner(file, r, rupturesFields, quarantine)

	var out []Rupture
	for sc.Scan() {
		f := sc.Fields()
		raw := strings.Join(f, "\t")

		cis := f[colRuptCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, raw)
			continue
		}

		var cip13 *string
		if v := strings.TrimSpace(f[colRuptCIP13]); v != "" {
			if !isDigits(v, 13) {
				quarantine(file, sc.Line(), ReasonCIP13Invalide, raw)
				continue
			}
			c := strings.Clone(v)
			cip13 = &c
		}

		codeStatutRaw := strings.TrimSpace(f[colRuptCodeStatut])
		codeStatut, err := strconv.Atoi(codeStatutRaw)
		if err != nil {
			quarantine(file, sc.Line(), ReasonCodeStatutInconnu, raw)
			continue
		}
		statut, libelle, ok := ruptureStatus(codeStatut)
		if !ok {
			quarantine(file, sc.Line(), ReasonCodeStatutInconnu, raw)
			continue
		}

		dateDebut, err := parseDateFR(f[colRuptDateDebut])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}
		approximative, err := isDateDebutApproximative(f[colRuptDateDebut])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}

		dateMiseAJour, err := parseDateFR(f[colRuptDateMiseAJour])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}

		dateRemiseDisposition, err := parseDateFROptional(f[colRuptDateRemiseDisposition])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}

		out = append(out, Rupture{
			CIS:                    strings.Clone(cis),
			CIP13:                  cip13,
			CodeStatut:             codeStatut,
			Statut:                 statut,
			Libelle:                libelle,
			LibelleSource:          strings.Clone(strings.TrimSpace(f[colRuptLibelleStatut])),
			DateDebut:              dateDebut,
			DateDebutApproximative: approximative,
			DateMiseAJour:          dateMiseAJour,
			DateRemiseDisposition:  clonePtr(dateRemiseDisposition),
			LienANSM:               httpsify(f[colRuptLienANSM]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
