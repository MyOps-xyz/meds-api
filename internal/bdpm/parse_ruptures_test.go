package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

func ruptureLine(cip13, codeStatut, libelleSource, dateDebut string) string {
	return strings.Join([]string{
		"62000612", cip13, codeStatut, libelleSource, dateDebut, "31/07/2026", "",
		"http://ansm.sante.fr/…",
	}, "\t") + "\n"
}

func TestParseRuptures_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	line := ruptureLine("", "2", "Tension dapprovisionnement", "30/07/2026")
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 1 {
		t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
	}

	got, err := json.Marshal(ruptures[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	want := `{
		"cis": "62000612",
		"cip13": null,
		"code_statut": 2,
		"statut": "tension_approvisionnement",
		"libelle": "Tension d'approvisionnement",
		"libelle_source": "Tension dapprovisionnement",
		"date_debut": "2026-07-30",
		"date_debut_approximative": false,
		"date_mise_a_jour": "2026-07-31",
		"date_remise_disposition": null,
		"lien_ansm": "https://ansm.sante.fr/…"
	}`
	assertJSONEqual(t, got, []byte(want))
}

// TestParseRuptures_P5_LibelleDerivedFromCode prouve que Statut et Libelle
// sont dérivés du code, jamais du libellé source incohérent, tout en
// conservant ce dernier dans LibelleSource (piège P5,
// docs/01-analyse-source-bdpm.md §5). Les deux graphies mesurées du même
// code doivent produire le même libellé dérivé.
func TestParseRuptures_P5_LibelleDerivedFromCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		libelleSource string
	}{
		{"apostrophe présente", "2  Tension d'approvisionnement"},
		{"apostrophe perdue", "2  Tension dapprovisionnement"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line := ruptureLine("", "2", tt.libelleSource, "30/07/2026")
			ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
			if err != nil {
				t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
			}
			if len(ruptures) != 1 {
				t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
			}
			got := ruptures[0]
			if got.Statut != "tension_approvisionnement" || got.Libelle != "Tension d'approvisionnement" {
				t.Errorf("Statut/Libelle = %q/%q, attendu dérivés du code, pas du libellé source",
					got.Statut, got.Libelle)
			}
			if got.LibelleSource != tt.libelleSource {
				t.Errorf("LibelleSource = %q, attendu %q (valeur brute conservée)", got.LibelleSource, tt.libelleSource)
			}
		})
	}
}

// TestParseRuptures_CIP13Vide_ToutesLaSpecialite prouve qu'un CIP13 vide
// devient nil, jamais une chaîne vide : toute la spécialité est concernée.
func TestParseRuptures_CIP13Vide_ToutesLaSpecialite(t *testing.T) {
	t.Parallel()

	line := ruptureLine("", "1", "1  Rupture de stock", "30/07/2026")
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 1 {
		t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
	}
	if ruptures[0].CIP13 != nil {
		t.Errorf("CIP13 = %q, attendu nil", *ruptures[0].CIP13)
	}
	b, err := json.Marshal(ruptures[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	if strings.Contains(string(b), `"cip13":""`) {
		t.Errorf("le JSON contient cip13 en chaîne vide au lieu de null : %s", b)
	}
}

// TestParseRuptures_DateDebutApproximative prouve que le seuil du
// 06/10/2023 déclenche correctement l'indicateur.
func TestParseRuptures_DateDebutApproximative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dateDebut string
		want      bool
	}{
		{"05/10/2023", true},
		{"06/10/2023", false},
		{"30/07/2026", false},
	}
	for _, tt := range tests {
		t.Run(tt.dateDebut, func(t *testing.T) {
			t.Parallel()
			line := ruptureLine("", "1", "1  Rupture de stock", tt.dateDebut)
			ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
			if err != nil {
				t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
			}
			if len(ruptures) != 1 {
				t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
			}
			if ruptures[0].DateDebutApproximative != tt.want {
				t.Errorf("DateDebutApproximative = %v, attendu %v", ruptures[0].DateDebutApproximative, tt.want)
			}
		})
	}
}

func TestParseRuptures_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"6200061", "", "1", "1  Rupture de stock", "30/07/2026", "31/07/2026", "", "http://ansm.sante.fr/…",
	}, "\t") + "\n"
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

func TestParseRuptures_CIP13Invalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := ruptureLine("34009494972", "1", "1  Rupture de stock", "30/07/2026")
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCIP13Invalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCIP13Invalide)
	}
}

func TestParseRuptures_CIP13Present(t *testing.T) {
	t.Parallel()

	line := ruptureLine("3400949497294", "1", "1  Rupture de stock", "30/07/2026")
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 1 {
		t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
	}
	if ruptures[0].CIP13 == nil || *ruptures[0].CIP13 != "3400949497294" {
		t.Errorf("CIP13 = %v, attendu \"3400949497294\"", ruptures[0].CIP13)
	}
}

func TestParseRuptures_DateDebutInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := ruptureLine("", "1", "1  Rupture de stock", "31/02/2026")
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

func TestParseRuptures_DateMiseAJourInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"62000612", "", "1", "1  Rupture de stock", "30/07/2026", "31/02/2026", "", "http://ansm.sante.fr/…",
	}, "\t") + "\n"
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

func TestParseRuptures_DateRemiseDispositionInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"62000612", "", "1", "1  Rupture de stock", "30/07/2026", "31/07/2026", "31/02/2026", "http://ansm.sante.fr/…",
	}, "\t") + "\n"
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

func TestParseRuptures_DateRemiseDispositionPresent(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"62000612", "", "4", "4  Remise à disposition", "30/07/2026", "31/07/2026", "01/08/2026", "http://ansm.sante.fr/…",
	}, "\t") + "\n"
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 1 {
		t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
	}
	if ruptures[0].DateRemiseDisposition == nil || *ruptures[0].DateRemiseDisposition != "2026-08-01" {
		t.Errorf("DateRemiseDisposition = %v, attendu \"2026-08-01\"", ruptures[0].DateRemiseDisposition)
	}
}

func TestParseRuptures_CodeStatutNonEntier_Quarantine(t *testing.T) {
	t.Parallel()

	line := ruptureLine("", "deux", "libellé", "30/07/2026")
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCodeStatutInconnu {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCodeStatutInconnu)
	}
}

func TestParseRuptures_CodeStatutInconnu_Quarantine(t *testing.T) {
	t.Parallel()

	line := ruptureLine("", "9", "libellé quelconque", "30/07/2026")
	var reasons []string
	ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
	}
	if len(ruptures) != 0 {
		t.Fatalf("len(ruptures) = %d, attendu 0", len(ruptures))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCodeStatutInconnu {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCodeStatutInconnu)
	}
}

func TestParseRuptures_AllFourStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code    string
		slug    string
		libelle string
	}{
		{"1", "rupture_stock", "Rupture de stock"},
		{"2", "tension_approvisionnement", "Tension d'approvisionnement"},
		{"3", "arret_commercialisation", "Arrêt de commercialisation"},
		{"4", "remise_disposition", "Remise à disposition"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()
			line := ruptureLine("", tt.code, "libellé source quelconque", "30/07/2026")
			ruptures, err := ParseRuptures("CIS_CIP_Dispo_Spec.txt", strings.NewReader(line), nil)
			if err != nil {
				t.Fatalf("ParseRuptures() erreur inattendue : %v", err)
			}
			if len(ruptures) != 1 {
				t.Fatalf("len(ruptures) = %d, attendu 1", len(ruptures))
			}
			if ruptures[0].Statut != tt.slug || ruptures[0].Libelle != tt.libelle {
				t.Errorf("Statut/Libelle = %q/%q, attendu %q/%q",
					ruptures[0].Statut, ruptures[0].Libelle, tt.slug, tt.libelle)
			}
		})
	}
}
