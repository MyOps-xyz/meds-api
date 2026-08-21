package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

func composantLine(nature string) string {
	return strings.Join([]string{
		"60002283", "comprimé", "42215", "ANASTROZOLE", "1,00 mg", "un comprimé", nature, "1",
	}, "\t") + "\n"
}

func TestParseComposants_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(composantLine("SA")), nil)
	if err != nil {
		t.Fatalf("ParseComposants() erreur inattendue : %v", err)
	}
	if len(compos) != 1 {
		t.Fatalf("len(compos) = %d, attendu 1", len(compos))
	}

	got, err := json.Marshal(compos[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}

	want := `{
		"cis": "60002283",
		"element_pharmaceutique": "comprimé",
		"code_substance": "42215",
		"denomination_substance": "ANASTROZOLE",
		"dosage": "1,00 mg",
		"reference_dosage": "un comprimé",
		"nature": "SA",
		"numero_liaison": 1
	}`
	assertJSONEqual(t, got, []byte(want))
}

// TestParseComposants_P3_STAcceptedAsFT prouve que ST — annoncé par la
// documentation officielle mais absent de la donnée mesurée — est accepté
// et converti en FT, et que FT est accepté tel quel (piège P3).
func TestParseComposants_P3_STAcceptedAsFT(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		source string
		want   string
	}{
		{"SA", "SA"},
		{"FT", "FT"},
		{"ST", "FT"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			t.Parallel()
			compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(composantLine(tt.source)), nil)
			if err != nil {
				t.Fatalf("ParseComposants() erreur inattendue : %v", err)
			}
			if len(compos) != 1 {
				t.Fatalf("len(compos) = %d, attendu 1", len(compos))
			}
			if compos[0].Nature != tt.want {
				t.Errorf("Nature = %q, attendu %q", compos[0].Nature, tt.want)
			}
		})
	}
}

// TestParseComposants_DosageNotParsed prouve que le dosage homéopathique
// est conservé tel quel, sans tentative de normalisation.
func TestParseComposants_DosageNotParsed(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "granules", "99999", "SUBSTANCE HOMEO", "2CH à 30CH et 4DH à 60DH", "un tube", "SA", "0",
	}, "\t") + "\n"

	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(line), nil)
	if err != nil {
		t.Fatalf("ParseComposants() erreur inattendue : %v", err)
	}
	if len(compos) != 1 {
		t.Fatalf("len(compos) = %d, attendu 1", len(compos))
	}
	if compos[0].Dosage != "2CH à 30CH et 4DH à 60DH" {
		t.Errorf("Dosage = %q, attendu conservé tel quel", compos[0].Dosage)
	}
}

func TestParseComposants_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"6000228", "comprimé", "42215", "ANASTROZOLE", "1,00 mg", "un comprimé", "SA", "1",
	}, "\t") + "\n"

	var reasons []string
	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseComposants() erreur inattendue : %v", err)
	}
	if len(compos) != 0 {
		t.Fatalf("len(compos) = %d, attendu 0", len(compos))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

func TestParseComposants_NumeroLiaisonInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"60002283", "comprimé", "42215", "ANASTROZOLE", "1,00 mg", "un comprimé", "SA", "un",
	}, "\t") + "\n"

	var reasons []string
	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseComposants() erreur inattendue : %v", err)
	}
	if len(compos) != 0 {
		t.Fatalf("len(compos) = %d, attendu 0", len(compos))
	}
	if len(reasons) != 1 || reasons[0] != ReasonNumeroLiaisonInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonNumeroLiaisonInvalide)
	}
}

func TestParseComposants_NatureInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	var reasons []string
	compos, err := ParseComposants("CIS_COMPO_bdpm.txt", strings.NewReader(composantLine("XX")),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseComposants() erreur inattendue : %v", err)
	}
	if len(compos) != 0 {
		t.Fatalf("len(compos) = %d, attendu 0", len(compos))
	}
	if len(reasons) != 1 || reasons[0] != ReasonNatureInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonNatureInvalide)
	}
}
