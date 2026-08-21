package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

func avisLine(valeur string) string {
	return strings.Join([]string{
		"60002283", "CT-12345", "Inscription (CT)", "20110518", valeur,
		"Le service médical rendu par …",
	}, "\t") + "\n"
}

func TestParseAvisSMR_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	avis, err := ParseAvisSMR("CIS_HAS_SMR_bdpm.txt", strings.NewReader(avisLine("Important")), nil)
	if err != nil {
		t.Fatalf("ParseAvisSMR() erreur inattendue : %v", err)
	}
	if len(avis) != 1 {
		t.Fatalf("len(avis) = %d, attendu 1", len(avis))
	}

	got, err := json.Marshal(avis[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	want := `{
		"cis": "60002283",
		"code_dossier_has": "CT-12345",
		"motif_evaluation": "Inscription (CT)",
		"date_avis": "2011-05-18",
		"valeur": "Important",
		"libelle": "Le service médical rendu par …",
		"lien_avis_ct": null
	}`
	assertJSONEqual(t, got, []byte(want))
}

func TestParseAvisSMR_DateAAAAMMJJ(t *testing.T) {
	t.Parallel()

	avis, err := ParseAvisSMR("CIS_HAS_SMR_bdpm.txt", strings.NewReader(avisLine("Important")), nil)
	if err != nil {
		t.Fatalf("ParseAvisSMR() erreur inattendue : %v", err)
	}
	if len(avis) != 1 || avis[0].DateAvis != "2011-05-18" {
		t.Fatalf("DateAvis = %v, attendu \"2011-05-18\"", avis)
	}
}

func TestParseAvisSMR_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"6000228", "CT-12345", "Inscription (CT)", "20110518", "Important", "libellé",
	}, "\t") + "\n"
	var reasons []string
	avis, err := ParseAvisSMR("CIS_HAS_SMR_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseAvisSMR() erreur inattendue : %v", err)
	}
	if len(avis) != 0 {
		t.Fatalf("len(avis) = %d, attendu 0", len(avis))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

func TestParseAvisSMR_DateInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "CT-12345", "Inscription (CT)", "18/05/2011", "Important", "libellé",
	}, "\t") + "\n"

	var reasons []string
	avis, err := ParseAvisSMR("CIS_HAS_SMR_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseAvisSMR() erreur inattendue : %v", err)
	}
	if len(avis) != 0 {
		t.Fatalf("len(avis) = %d, attendu 0", len(avis))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

// TestParseAvisASMR_NiveauRomain prouve que Niveau est renseigné pour un
// chiffre romain pur.
func TestParseAvisASMR_NiveauRomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		valeur string
		niveau int
	}{
		{"I", 1}, {"II", 2}, {"III", 3}, {"IV", 4}, {"V", 5},
	}
	for _, tt := range tests {
		t.Run(tt.valeur, func(t *testing.T) {
			t.Parallel()
			avis, err := ParseAvisASMR("CIS_HAS_ASMR_bdpm.txt", strings.NewReader(avisLine(tt.valeur)), nil)
			if err != nil {
				t.Fatalf("ParseAvisASMR() erreur inattendue : %v", err)
			}
			if len(avis) != 1 || avis[0].Niveau == nil || *avis[0].Niveau != tt.niveau {
				t.Fatalf("Niveau = %v, attendu %d", avis, tt.niveau)
			}
		})
	}
}

// TestParseAvisASMR_NiveauNilForTextualValues prouve que les trois valeurs
// textuelles observées dans la source laissent Niveau à nil
// (docs/03-modele-de-donnees.md §3.6).
func TestParseAvisASMR_NiveauNilForTextualValues(t *testing.T) {
	t.Parallel()

	tests := []string{
		"Commentaires sans chiffrage de l'ASMR",
		"V dans l'attente de données",
		"Commentaires",
	}
	for _, valeur := range tests {
		t.Run(valeur, func(t *testing.T) {
			t.Parallel()
			avis, err := ParseAvisASMR("CIS_HAS_ASMR_bdpm.txt", strings.NewReader(avisLine(valeur)), nil)
			if err != nil {
				t.Fatalf("ParseAvisASMR() erreur inattendue : %v", err)
			}
			if len(avis) != 1 {
				t.Fatalf("len(avis) = %d, attendu 1", len(avis))
			}
			if avis[0].Niveau != nil {
				t.Errorf("Niveau = %d, attendu nil pour %q", *avis[0].Niveau, valeur)
			}
			b, err := json.Marshal(avis[0])
			if err != nil {
				t.Fatalf("json.Marshal() erreur inattendue : %v", err)
			}
			if !strings.Contains(string(b), `"niveau":null`) {
				t.Errorf("JSON sans niveau:null : %s", b)
			}
		})
	}
}

func TestParseAvisASMR_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	avis, err := ParseAvisASMR("CIS_HAS_ASMR_bdpm.txt", strings.NewReader(avisLine("III")), nil)
	if err != nil {
		t.Fatalf("ParseAvisASMR() erreur inattendue : %v", err)
	}
	if len(avis) != 1 {
		t.Fatalf("len(avis) = %d, attendu 1", len(avis))
	}
	if avis[0].Niveau == nil || *avis[0].Niveau != 3 {
		t.Fatalf("Niveau = %v, attendu 3", avis[0].Niveau)
	}
}
