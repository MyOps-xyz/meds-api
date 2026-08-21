package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseMITM_JSONMatchesDoc et P4 : le lien BDPM est réécrit en HTTPS.
func TestParseMITM_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	content := "60003620\tR03BA01\tBECLOSPIN 800 microgrammes/2ml suspension pour inhalation par nébuliseur\t" +
		"http://base-donnees-publique.medicaments.gouv.fr/extrait.php?specid=60003620\n"

	mitm, err := ParseMITM("CIS_MITM.txt", strings.NewReader(content), nil)
	if err != nil {
		t.Fatalf("ParseMITM() erreur inattendue : %v", err)
	}
	if len(mitm) != 1 {
		t.Fatalf("len(mitm) = %d, attendu 1", len(mitm))
	}

	got, err := json.Marshal(mitm[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	want := `{
		"cis": "60003620",
		"code_atc": "R03BA01",
		"denomination": "BECLOSPIN 800 microgrammes/2ml suspension pour inhalation par nébuliseur",
		"lien_bdpm": "https://base-donnees-publique.medicaments.gouv.fr/extrait.php?specid=60003620"
	}`
	assertJSONEqual(t, got, []byte(want))
}

func TestParseMITM_NoTrailingNewline(t *testing.T) {
	t.Parallel()

	// P7 : CIS_MITM.txt n'a pas de saut de ligne final ; le Scanner
	// l'absorbe déjà (voir scanner_test.go), ce test prouve que le
	// parseur n'y ajoute pas de régression.
	content := "60003620\tR03BA01\tDénomination\thttp://lien"
	mitm, err := ParseMITM("CIS_MITM.txt", strings.NewReader(content), nil)
	if err != nil {
		t.Fatalf("ParseMITM() erreur inattendue : %v", err)
	}
	if len(mitm) != 1 {
		t.Fatalf("len(mitm) = %d, attendu 1", len(mitm))
	}
}

func TestParseMITM_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	content := "abc\tR03BA01\tDénomination\thttp://lien\n"
	var reasons []string
	mitm, err := ParseMITM("CIS_MITM.txt", strings.NewReader(content),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseMITM() erreur inattendue : %v", err)
	}
	if len(mitm) != 0 {
		t.Fatalf("len(mitm) = %d, attendu 0", len(mitm))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}
