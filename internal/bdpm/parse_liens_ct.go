package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// liensCTFields est le nombre de colonnes attendu dans
// HAS_LiensPageCT_bdpm.txt (docs/01-analyse-source-bdpm.md §3.6).
const liensCTFields = 2

// Colonnes de HAS_LiensPageCT_bdpm.txt, 0-indexées.
const (
	colLienCodeDossierHAS = iota
	colLienURL
)

// ParseLiensCT lit HAS_LiensPageCT_bdpm.txt déjà décodé et produit les
// liens vers les avis de la commission de la transparence
// (docs/01-analyse-source-bdpm.md §3.6). Ce n'est pas une entité du
// snapshot en tant que telle : c'est le seul fichier qui ne porte pas de
// code CIS, et T-10 s'en sert pour résoudre AvisSMR.LienAvisCT et
// AvisASMR.LienAvisCT par jointure sur le code de dossier HAS. L'URL est
// réécrite en HTTPS (règle générale du pipeline, docs/09-pipeline-mise-a-jour.md
// §3.5).
func ParseLiensCT(file string, r io.Reader, quarantine QuarantineFunc) ([]LienAvisCT, error) {
	sc := NewScanner(file, r, liensCTFields, quarantine)

	var out []LienAvisCT
	for sc.Scan() {
		f := sc.Fields()

		codeDossierHAS := strings.TrimSpace(f[colLienCodeDossierHAS])
		if codeDossierHAS == "" {
			quarantine(file, sc.Line(), ReasonCodeDossierHASVide, strings.Join(f, "\t"))
			continue
		}

		out = append(out, LienAvisCT{
			CodeDossierHAS: strings.Clone(codeDossierHAS),
			URL:            httpsify(f[colLienURL]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
