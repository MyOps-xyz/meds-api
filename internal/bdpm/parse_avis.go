package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// avisFields est le nombre de colonnes attendu dans CIS_HAS_SMR_bdpm.txt et
// CIS_HAS_ASMR_bdpm.txt, qui partagent le même schéma
// (docs/01-analyse-source-bdpm.md §3.4 et §3.5).
const avisFields = 6

// Colonnes communes à CIS_HAS_SMR_bdpm.txt et CIS_HAS_ASMR_bdpm.txt,
// 0-indexées.
const (
	colAvisCIS = iota
	colAvisCodeDossierHAS
	colAvisMotifEvaluation
	colAvisDateAvis
	colAvisValeur
	colAvisLibelle
)

// avisRow est la ligne commune décodée aux six colonnes, avant
// spécialisation en AvisSMR ou AvisASMR. La date est déjà convertie en ISO
// 8601 depuis le format AAAAMMJJ propre à ces deux fichiers
// (docs/03-modele-de-donnees.md §3.6, note sur les deux formats de date).
type avisRow struct {
	cis             string
	codeDossierHAS  string
	motifEvaluation string
	dateAvis        string
	valeur          string
	libelle         string
}

// parseAvisRows lit un fichier d'avis HAS (SMR ou ASMR, schéma identique)
// et retourne les lignes valides, déjà normalisées. Une ligne dont le CIS
// n'est pas huit chiffres ou dont la date d'avis n'est pas au format
// AAAAMMJJ part en quarantaine.
func parseAvisRows(file string, r io.Reader, quarantine QuarantineFunc) ([]avisRow, error) {
	sc := NewScanner(file, r, avisFields, quarantine)

	var out []avisRow
	for sc.Scan() {
		f := sc.Fields()
		raw := strings.Join(f, "\t")

		cis := f[colAvisCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, raw)
			continue
		}

		dateAvis, err := parseDateCompact(f[colAvisDateAvis])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}

		out = append(out, avisRow{
			cis:             strings.Clone(cis),
			codeDossierHAS:  strings.Clone(strings.TrimSpace(f[colAvisCodeDossierHAS])),
			motifEvaluation: strings.Clone(strings.TrimSpace(f[colAvisMotifEvaluation])),
			dateAvis:        dateAvis,
			valeur:          strings.Clone(strings.TrimSpace(f[colAvisValeur])),
			libelle:         strings.Clone(strings.TrimSpace(f[colAvisLibelle])),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}

// ParseAvisSMR lit CIS_HAS_SMR_bdpm.txt déjà décodé et produit les avis de
// service médical rendu du snapshot (docs/03-modele-de-donnees.md §3.6).
// LienAvisCT est toujours nil : sa résolution est le rôle de T-10.
func ParseAvisSMR(file string, r io.Reader, quarantine QuarantineFunc) ([]AvisSMR, error) {
	rows, err := parseAvisRows(file, r, quarantine)
	if err != nil {
		return nil, err
	}
	out := make([]AvisSMR, len(rows))
	for i, row := range rows {
		out[i] = AvisSMR{
			CIS:             row.cis,
			CodeDossierHAS:  row.codeDossierHAS,
			MotifEvaluation: row.motifEvaluation,
			DateAvis:        row.dateAvis,
			Valeur:          row.valeur,
			Libelle:         row.libelle,
			LienAvisCT:      nil,
		}
	}
	return out, nil
}

// ParseAvisASMR lit CIS_HAS_ASMR_bdpm.txt déjà décodé et produit les avis
// d'amélioration du service médical rendu du snapshot
// (docs/03-modele-de-donnees.md §3.6). Niveau n'est renseigné que lorsque
// Valeur est un chiffre romain pur (« I » à « V ») ; les valeurs textuelles
// (« Commentaires sans chiffrage de l'ASMR », « V dans l'attente de
// données », « Commentaires ») laissent Niveau à nil. LienAvisCT est
// toujours nil : sa résolution est le rôle de T-10.
func ParseAvisASMR(file string, r io.Reader, quarantine QuarantineFunc) ([]AvisASMR, error) {
	rows, err := parseAvisRows(file, r, quarantine)
	if err != nil {
		return nil, err
	}
	out := make([]AvisASMR, len(rows))
	for i, row := range rows {
		var niveau *int
		if n, ok := romanASMRToNiveau(row.valeur); ok {
			niveau = &n
		}
		out[i] = AvisASMR{
			CIS:             row.cis,
			CodeDossierHAS:  row.codeDossierHAS,
			MotifEvaluation: row.motifEvaluation,
			DateAvis:        row.dateAvis,
			Valeur:          row.valeur,
			Niveau:          niveau,
			Libelle:         row.libelle,
			LienAvisCT:      nil,
		}
	}
	return out, nil
}
