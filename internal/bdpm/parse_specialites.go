package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// specialitesFields est le nombre de colonnes attendu dans CIS_bdpm.txt
// (docs/01-analyse-source-bdpm.md §3.1).
const specialitesFields = 12

// Colonnes de CIS_bdpm.txt, 0-indexées.
const (
	colSpecCIS = iota
	colSpecDenomination
	colSpecForme
	colSpecVoies
	colSpecStatutAMM
	colSpecProcedureAMM
	colSpecEtatCommercialisation
	colSpecDateAMM
	colSpecStatutBdm
	colSpecNumEuro
	colSpecTitulaires
	colSpecSurveillance
)

// Valeurs connues des énumérations conservées telles quelles dans le
// snapshot (docs/01-analyse-source-bdpm.md §3.1), pour le signalement des
// valeurs inconnues sans rejet de ligne.
var (
	knownStatutAMM = toSet(
		"Autorisation active", "Autorisation abrogée", "Autorisation archivée",
		"Autorisation retirée", "Autorisation suspendue",
	)
	knownProcedureAMM = toSet(
		"Procédure nationale", "Procédure décentralisée", "Procédure centralisée",
		"Procédure de reconnaissance mutuelle", "Enreg homéo (Proc. Nat.)",
		"Autorisation d'importation parallèle", "Enreg phyto (Proc. Nat.)", "Enreg phyto (Proc. Dec.)",
	)
	knownEtatCommercialisationSpec = toSet("Commercialisée", "Non commercialisée")
	knownStatutBdm                 = toSet("Warning disponibilité", "Alerte")
)

func toSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// ParseSpecialites lit CIS_bdpm.txt déjà décodé en UTF-8 et produit les
// spécialités du snapshot (docs/03-modele-de-donnees.md §3.1).
//
// Une ligne dont le CIS n'est pas huit chiffres, la date d'AMM invalide ou
// la surveillance renforcée hors de {Oui, Non} part en quarantaine plutôt
// que d'interrompre l'ingestion. Seule une erreur d'entrée/sortie ou une
// ligne trop longue (Scanner.Err) est retournée comme erreur fatale.
func ParseSpecialites(file string, r io.Reader, quarantine QuarantineFunc, unknown UnknownValueFunc) ([]Specialite, error) {
	sc := NewScanner(file, r, specialitesFields, quarantine)

	var out []Specialite
	for sc.Scan() {
		f := sc.Fields()

		cis := f[colSpecCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, strings.Join(f, "\t"))
			continue
		}

		dateAMM, err := parseDateFR(f[colSpecDateAMM])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, strings.Join(f, "\t"))
			continue
		}

		surveillance, err := parseOuiNon(f[colSpecSurveillance])
		if err != nil {
			quarantine(file, sc.Line(), ReasonSurveillanceRenforceeInvalide, strings.Join(f, "\t"))
			continue
		}

		statutAMM := strings.TrimSpace(f[colSpecStatutAMM])
		procedureAMM := strings.TrimSpace(f[colSpecProcedureAMM])
		etatCommercialisation := strings.TrimSpace(f[colSpecEtatCommercialisation])
		reportUnknown(unknown, knownStatutAMM, file, "statut_amm", statutAMM)
		reportUnknown(unknown, knownProcedureAMM, file, "procedure_amm", procedureAMM)
		reportUnknown(unknown, knownEtatCommercialisationSpec, file, "etat_commercialisation", etatCommercialisation)

		statutBdm := trimOrNil(f[colSpecStatutBdm])
		if statutBdm != nil {
			reportUnknown(unknown, knownStatutBdm, file, "statut_bdm", *statutBdm)
		}

		out = append(out, Specialite{
			CIS:                          strings.Clone(cis),
			Denomination:                 strings.Clone(strings.TrimSpace(f[colSpecDenomination])),
			FormePharmaceutique:          strings.Clone(strings.TrimSpace(f[colSpecForme])),
			VoiesAdministration:          cloneAll(splitMulti(f[colSpecVoies])),
			StatutAMM:                    strings.Clone(statutAMM),
			ProcedureAMM:                 strings.Clone(procedureAMM),
			EtatCommercialisation:        strings.Clone(etatCommercialisation),
			DateAMM:                      dateAMM,
			StatutBdm:                    clonePtr(statutBdm),
			NumeroAutorisationEuropeenne: clonePtr(trimOrNil(f[colSpecNumEuro])),
			Titulaires:                   cloneAll(splitMulti(f[colSpecTitulaires])),
			SurveillanceRenforcee:        surveillance,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
