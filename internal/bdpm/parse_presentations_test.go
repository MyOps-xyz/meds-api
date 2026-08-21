package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

// presentationLine construit une ligne CIS_CIP_bdpm.txt (13 colonnes) à
// partir de l'exemple de docs/03-modele-de-donnees.md §3.2, avec le taux
// de remboursement en paramètre pour couvrir les deux graphies observées
// (piège P4).
func presentationLine(taux string) string {
	return strings.Join([]string{
		"60002283", "4949729", "plaquette(s) PVC PVDC aluminium de 30 comprimé(s)",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", taux, "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"
}

func TestParsePresentations_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(presentationLine("65%")), nil, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("len(pres) = %d, attendu 1", len(pres))
	}

	got, err := json.Marshal(pres[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}

	want := `{
		"cip13": "3400949497294",
		"cip7": "4949729",
		"cis": "60002283",
		"libelle": "plaquette(s) PVC PVDC aluminium de 30 comprimé(s)",
		"statut_administratif": "Présentation active",
		"etat_commercialisation": "Déclaration de commercialisation",
		"date_declaration": "2011-03-16",
		"agrement_collectivites": true,
		"taux_remboursement": [65],
		"prix_medicament_cents": 2434,
		"prix_public_cents": 2536,
		"honoraires_dispensation_cents": 102,
		"indications_remboursement": null
	}`

	assertJSONEqual(t, got, []byte(want))
}

// TestParsePresentations_P4_TauxRemboursementBothForms prouve que « 65% »
// et « 65 % » produisent tous deux [65] au niveau d'un parseur complet, pas
// seulement de la primitive de normalisation.
func TestParsePresentations_P4_TauxRemboursementBothForms(t *testing.T) {
	t.Parallel()

	for _, taux := range []string{"65%", "65 %"} {
		t.Run(taux, func(t *testing.T) {
			t.Parallel()
			pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(presentationLine(taux)), nil, nil)
			if err != nil {
				t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
			}
			if len(pres) != 1 || len(pres[0].TauxRemboursement) != 1 || pres[0].TauxRemboursement[0] != 65 {
				t.Fatalf("TauxRemboursement = %v, attendu [65]", pres[0].TauxRemboursement)
			}
		})
	}
}

// TestParsePresentations_PrixAbsent_Null prouve que 7 276 présentations
// sans prix produisent null, jamais 0 (critère d'acceptation T-08).
func TestParsePresentations_PrixAbsent_Null(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "non", "", "", "", "", "",
	}, "\t") + "\n"

	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line), nil, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("len(pres) = %d, attendu 1", len(pres))
	}
	got := pres[0]
	if got.PrixMedicamentCents != nil {
		t.Errorf("PrixMedicamentCents = %d, attendu nil (absent ≠ zéro)", *got.PrixMedicamentCents)
	}
	if got.PrixPublicCents != nil {
		t.Errorf("PrixPublicCents = %d, attendu nil", *got.PrixPublicCents)
	}
	if got.HonorairesDispensationCents != nil {
		t.Errorf("HonorairesDispensationCents = %d, attendu nil", *got.HonorairesDispensationCents)
	}
	if len(got.TauxRemboursement) != 0 {
		t.Errorf("TauxRemboursement = %v, attendu vide", got.TauxRemboursement)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	if strings.Contains(string(b), `"prix_medicament_cents":0`) {
		t.Errorf("le JSON contient un prix à zéro au lieu de null : %s", b)
	}
	if !strings.Contains(string(b), `"prix_medicament_cents":null`) {
		t.Errorf("le JSON ne contient pas prix_medicament_cents:null : %s", b)
	}
}

// TestParsePresentations_AgrementInconnu_Nullable prouve que « inconnu »
// devient null, jamais false.
func TestParsePresentations_AgrementInconnu_Nullable(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "inconnu", "", "", "", "", "",
	}, "\t") + "\n"

	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line), nil, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("len(pres) = %d, attendu 1", len(pres))
	}
	if pres[0].AgrementCollectivites != nil {
		t.Errorf("AgrementCollectivites = %v, attendu nil", *pres[0].AgrementCollectivites)
	}
}

func TestParsePresentations_InvalidCIP13_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"34009494972", "oui", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCIP13Invalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCIP13Invalide)
	}
}

func TestParsePresentations_InvalidCIS_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"6000228", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

func TestParsePresentations_InvalidCIP7_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "494972", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCIP7Invalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCIP7Invalide)
	}
}

func TestParsePresentations_InvalidDate_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "31/02/2011",
		"3400949497294", "oui", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

func TestParsePresentations_InvalidAgrement_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "peut-être", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonAgrementCollectivitesInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonAgrementCollectivitesInvalide)
	}
}

func TestParsePresentations_InvalidTaux_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", "abc%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonTauxRemboursementInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonTauxRemboursementInvalide)
	}
}

// TestParsePresentations_IndicationsRemboursement_NonNil couvre la branche
// non-nil de clonePtr : un champ optionnel présent doit être conservé.
func TestParsePresentations_IndicationsRemboursement_NonNil(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", "65%;30%", "24,34", "25,36", "1,02", "réservé aux formes graves",
	}, "\t") + "\n"

	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line), nil, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("len(pres) = %d, attendu 1", len(pres))
	}
	if pres[0].IndicationsRemboursement == nil || *pres[0].IndicationsRemboursement != "réservé aux formes graves" {
		t.Errorf("IndicationsRemboursement = %v, attendu \"réservé aux formes graves\"", pres[0].IndicationsRemboursement)
	}
	if len(pres[0].TauxRemboursement) != 2 {
		t.Errorf("TauxRemboursement = %v, attendu deux valeurs", pres[0].TauxRemboursement)
	}
}

// TestParsePresentations_UnknownValue_Reported couvre le signalement des
// valeurs d'énumération inconnues sur statut_administratif et
// etat_commercialisation.
func TestParsePresentations_UnknownValue_Reported(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Statut extraterrestre", "État extraterrestre", "16/03/2011",
		"3400949497294", "oui", "65%", "24,34", "25,36", "1,02", "",
	}, "\t") + "\n"

	var unknowns []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line), nil,
		func(file, field, value string) { unknowns = append(unknowns, field+"="+value) })
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 1 {
		t.Fatalf("len(pres) = %d, attendu 1", len(pres))
	}
	if len(unknowns) != 2 {
		t.Fatalf("unknowns = %v, attendu deux entrées", unknowns)
	}
}

func TestParsePresentations_InvalidPrice_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "4949729", "libellé",
		"Présentation active", "Déclaration de commercialisation", "16/03/2011",
		"3400949497294", "oui", "65%", "pas-un-prix", "25,36", "1,02", "",
	}, "\t") + "\n"

	var reasons []string
	pres, err := ParsePresentations("CIS_CIP_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParsePresentations() erreur inattendue : %v", err)
	}
	if len(pres) != 0 {
		t.Fatalf("len(pres) = %d, attendu 0", len(pres))
	}
	if len(reasons) != 1 || reasons[0] != ReasonPrixInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonPrixInvalide)
	}
}
