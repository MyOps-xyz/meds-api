package bdpm

import (
	"fmt"
	"io"
	"strings"
)

// presentationsFields est le nombre de colonnes attendu dans
// CIS_CIP_bdpm.txt (docs/01-analyse-source-bdpm.md §3.2).
const presentationsFields = 13

// Colonnes de CIS_CIP_bdpm.txt, 0-indexées.
const (
	colPresCIS = iota
	colPresCIP7
	colPresLibelle
	colPresStatutAdministratif
	colPresEtatCommercialisation
	colPresDateDeclaration
	colPresCIP13
	colPresAgrementCollectivites
	colPresTauxRemboursement
	colPresPrixMedicament
	colPresPrixPublic
	colPresHonoraires
	colPresIndicationsRemboursement
)

var (
	knownStatutAdministratifPres   = toSet("Présentation active", "Présentation abrogée")
	knownEtatCommercialisationPres = toSet(
		"Déclaration de commercialisation",
		"Déclaration d'arrêt de commercialisation",
		"Arrêt de commercialisation (le médicament n'a plus d'autorisation)",
		"Déclaration de suspension de commercialisation",
	)
)

// ParsePresentations lit CIS_CIP_bdpm.txt déjà décodé et produit les
// présentations du snapshot (docs/03-modele-de-donnees.md §3.2).
//
// Les prix (colonnes 10 à 12) sont convertis en centimes avec arrondi
// (parsePriceCents) : une valeur absente devient nil, jamais zéro, pour ne
// pas confondre « non remboursable » et « gratuit » (piège P4). Une ligne
// dont un identifiant (CIS, CIP7, CIP13) est mal formé, dont la date est
// invalide, dont l'agrément collectivités est hors énumération, dont le
// taux de remboursement n'est pas un entier, ou dont un prix non vide
// n'est pas convertible, part en quarantaine.
func ParsePresentations(file string, r io.Reader, quarantine QuarantineFunc, unknown UnknownValueFunc) ([]Presentation, error) {
	sc := NewScanner(file, r, presentationsFields, quarantine)

	var out []Presentation
	for sc.Scan() {
		f := sc.Fields()
		raw := strings.Join(f, "\t")

		cis := f[colPresCIS]
		if !isDigits(cis, 8) {
			quarantine(file, sc.Line(), ReasonCISInvalide, raw)
			continue
		}
		cip7 := f[colPresCIP7]
		if !isDigits(cip7, 7) {
			quarantine(file, sc.Line(), ReasonCIP7Invalide, raw)
			continue
		}
		cip13 := f[colPresCIP13]
		if !isDigits(cip13, 13) {
			quarantine(file, sc.Line(), ReasonCIP13Invalide, raw)
			continue
		}

		dateDeclaration, err := parseDateFR(f[colPresDateDeclaration])
		if err != nil {
			quarantine(file, sc.Line(), ReasonDateInvalide, raw)
			continue
		}

		agrement, err := parseTriBool(f[colPresAgrementCollectivites])
		if err != nil {
			quarantine(file, sc.Line(), ReasonAgrementCollectivitesInvalide, raw)
			continue
		}

		taux, err := parsePercentages(f[colPresTauxRemboursement])
		if err != nil {
			quarantine(file, sc.Line(), ReasonTauxRemboursementInvalide, raw)
			continue
		}

		prixMedicament, err := parsePriceCents(f[colPresPrixMedicament])
		if err != nil {
			quarantine(file, sc.Line(), ReasonPrixInvalide, raw)
			continue
		}
		prixPublic, err := parsePriceCents(f[colPresPrixPublic])
		if err != nil {
			quarantine(file, sc.Line(), ReasonPrixInvalide, raw)
			continue
		}
		honoraires, err := parsePriceCents(f[colPresHonoraires])
		if err != nil {
			quarantine(file, sc.Line(), ReasonPrixInvalide, raw)
			continue
		}

		statutAdministratif := strings.TrimSpace(f[colPresStatutAdministratif])
		etatCommercialisation := strings.TrimSpace(f[colPresEtatCommercialisation])
		reportUnknown(unknown, knownStatutAdministratifPres, file, "statut_administratif", statutAdministratif)
		reportUnknown(unknown, knownEtatCommercialisationPres, file, "etat_commercialisation", etatCommercialisation)

		out = append(out, Presentation{
			CIP13:                       strings.Clone(cip13),
			CIP7:                        strings.Clone(cip7),
			CIS:                         strings.Clone(cis),
			Libelle:                     strings.Clone(strings.TrimSpace(f[colPresLibelle])),
			StatutAdministratif:         strings.Clone(statutAdministratif),
			EtatCommercialisation:       strings.Clone(etatCommercialisation),
			DateDeclaration:             dateDeclaration,
			AgrementCollectivites:       agrement,
			TauxRemboursement:           taux,
			PrixMedicamentCents:         prixMedicament,
			PrixPublicCents:             prixPublic,
			HonorairesDispensationCents: honoraires,
			IndicationsRemboursement:    clonePtr(trimOrNil(f[colPresIndicationsRemboursement])),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", file, err)
	}
	return out, nil
}
