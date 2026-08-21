package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// mitmFields est le nombre de colonnes attendu dans CIS_MITM.txt
// (docs/01-analyse-source-bdpm.md §3.10).
const mitmFields = 4

// Colonnes de CIS_MITM.txt, 0-indexées.
const (
	colMitmCIS = iota
	colMitmCodeATC
	colMitmDenomination
	colMitmLienBDPM
)

// ParseMITM lit CIS_MITM.txt déjà décodé et produit les statuts de
// médicament d'intérêt thérapeutique majeur du snapshot
// (docs/03-modele-de-donnees.md §3.7). LienBDPM est réécrit en HTTPS, la
// source ne fournissant que du HTTP avec l'ancien chemin
// extrait.php?specid=.
func ParseMITM(file string, r io.Reader, quarantine QuarantineFunc) ([]InfoMITM, error) {
	sc := NewScanner(file, r, mitmFields, quarantine)

	var out []InfoMITM
	for sc.Scan() {
		f := sc.Fields()

		cis := f[colMitmCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, strings.Join(f, "\t"))
			continue
		}

		out = append(out, InfoMITM{
			CIS:          strings.Clone(cis),
			CodeATC:      strings.Clone(strings.TrimSpace(f[colMitmCodeATC])),
			Denomination: strings.Clone(strings.TrimSpace(f[colMitmDenomination])),
			LienBDPM:     httpsify(f[colMitmLienBDPM]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
