package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSpecialites(t *testing.T) {
	t.Parallel()

	content := "61266250\tA 313 200 000 UI POUR CENT, pommade\tpommade\tcutanée\t" +
		"Autorisation active\tProcédure nationale\tCommercialisée\t12/03/1998\t\t\t" +
		"  PHARMA DEVELOPPEMENT\tNon\n"

	var quarantined []string
	var unknownValues []string
	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content),
		func(file string, line int, reason, raw string) { quarantined = append(quarantined, reason) },
		func(file, field, value string) { unknownValues = append(unknownValues, field+"="+value) })
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(quarantined) != 0 {
		t.Fatalf("quarantaine inattendue : %v", quarantined)
	}
	if len(unknownValues) != 0 {
		t.Fatalf("valeurs inconnues inattendues : %v", unknownValues)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, attendu 1", len(specs))
	}

	got := specs[0]
	if got.CIS != "61266250" {
		t.Errorf("CIS = %q, attendu \"61266250\"", got.CIS)
	}
	if got.DateAMM != "1998-03-12" {
		t.Errorf("DateAMM = %q, attendu \"1998-03-12\"", got.DateAMM)
	}
	if got.SurveillanceRenforcee {
		t.Error("SurveillanceRenforcee = true, attendu false")
	}
	if got.StatutBdm != nil {
		t.Errorf("StatutBdm = %v, attendu nil (chaîne vide → null)", *got.StatutBdm)
	}
	if got.NumeroAutorisationEuropeenne != nil {
		t.Errorf("NumeroAutorisationEuropeenne = %v, attendu nil", *got.NumeroAutorisationEuropeenne)
	}
	if len(got.Titulaires) != 1 || got.Titulaires[0] != "PHARMA DEVELOPPEMENT" {
		t.Errorf("Titulaires = %v, attendu [\"PHARMA DEVELOPPEMENT\"] (espaces de tête retirés)", got.Titulaires)
	}
	if len(got.VoiesAdministration) != 1 || got.VoiesAdministration[0] != "cutanée" {
		t.Errorf("VoiesAdministration = %v, attendu [\"cutanée\"]", got.VoiesAdministration)
	}
}

// TestParseSpecialites_JSONMatchesDoc compare une sérialisation JSON à
// l'exemple documenté dans docs/03-modele-de-donnees.md §3.1 — comparaison
// de JSON, pas de structures.
func TestParseSpecialites_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	content := "61266250\tA 313 200 000 UI POUR CENT, pommade\tpommade\tcutanée\t" +
		"Autorisation active\tProcédure nationale\tCommercialisée\t12/03/1998\t\t\t" +
		"PHARMA DEVELOPPEMENT\tNon\n"

	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content), nil, nil)
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, attendu 1", len(specs))
	}

	got, err := json.Marshal(specs[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}

	want := `{
		"cis": "61266250",
		"denomination": "A 313 200 000 UI POUR CENT, pommade",
		"forme_pharmaceutique": "pommade",
		"voies_administration": ["cutanée"],
		"statut_amm": "Autorisation active",
		"procedure_amm": "Procédure nationale",
		"etat_commercialisation": "Commercialisée",
		"date_amm": "1998-03-12",
		"statut_bdm": null,
		"numero_autorisation_europeenne": null,
		"titulaires": ["PHARMA DEVELOPPEMENT"],
		"surveillance_renforcee": false
	}`

	assertJSONEqual(t, got, []byte(want))
}

// TestParseSpecialites_CISInvalide_Quarantine met en quarantaine une ligne
// dont le CIS n'est pas huit chiffres.
func TestParseSpecialites_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	content := "612662\tDénomination\tforme\tcutanée\tAutorisation active\t" +
		"Procédure nationale\tCommercialisée\t12/03/1998\t\t\tTITULAIRE\tNon\n"

	var reasons []string
	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content),
		func(file string, line int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("len(specs) = %d, attendu 0", len(specs))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

// TestParseSpecialites_DateInvalide_Quarantine met en quarantaine une
// ligne dont la date d'AMM est invalide, sans faire échouer le parseur.
func TestParseSpecialites_DateInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	content := "61266250\tDénomination\tforme\tcutanée\tAutorisation active\t" +
		"Procédure nationale\tCommercialisée\t31/02/2020\t\t\tTITULAIRE\tNon\n"

	var reasons []string
	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content),
		func(file string, line int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("len(specs) = %d, attendu 0", len(specs))
	}
	if len(reasons) != 1 || reasons[0] != ReasonDateInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonDateInvalide)
	}
}

// TestParseSpecialites_UnknownValue_ReportedNotRejected prouve qu'une
// valeur d'énumération jamais observée est signalée sans mettre la ligne
// en quarantaine : le référentiel doit rester servi même si l'ANSM ajoute
// une modalité.
func TestParseSpecialites_UnknownValue_ReportedNotRejected(t *testing.T) {
	t.Parallel()

	content := "61266250\tDénomination\tforme\tcutanée\tAutorisation extraterrestre\t" +
		"Procédure nationale\tCommercialisée\t12/03/1998\t\t\tTITULAIRE\tNon\n"

	var reasons []string
	var unknowns []string
	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content),
		func(file string, line int, reason, raw string) { reasons = append(reasons, reason) },
		func(file, field, value string) { unknowns = append(unknowns, field+"="+value) })
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("quarantaine inattendue : %v", reasons)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, attendu 1 (la ligne doit rester servie)", len(specs))
	}
	if specs[0].StatutAMM != "Autorisation extraterrestre" {
		t.Errorf("StatutAMM = %q, la valeur inconnue doit être conservée telle quelle", specs[0].StatutAMM)
	}
	found := false
	for _, u := range unknowns {
		if u == "statut_amm=Autorisation extraterrestre" {
			found = true
		}
	}
	if !found {
		t.Errorf("unknowns = %v, attendu une entrée pour statut_amm", unknowns)
	}
}

func TestParseSpecialites_ScannerErrorPropagates(t *testing.T) {
	t.Parallel()

	// Trop de colonnes : la ligne est mise en quarantaine par Scanner lui-même
	// (nombre de colonnes inattendu), pas par ce parseur.
	content := "61266250\ttrop\tde\tcolonnes\n"
	var reasons []string
	specs, err := ParseSpecialites("CIS_bdpm.txt", strings.NewReader(content),
		func(file string, line int, reason, raw string) { reasons = append(reasons, reason) }, nil)
	if err != nil {
		t.Fatalf("ParseSpecialites() erreur inattendue : %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("len(specs) = %d, attendu 0", len(specs))
	}
	if len(reasons) != 1 {
		t.Fatalf("reasons = %v, attendu une seule entrée (motif du Scanner)", reasons)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("json.Unmarshal(got) : %v\ngot = %s", err, got)
	}
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("json.Unmarshal(want) : %v", err)
	}
	gotNorm, _ := json.Marshal(gotAny)
	wantNorm, _ := json.Marshal(wantAny)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("JSON différent :\ngot  = %s\nwant = %s", gotNorm, wantNorm)
	}
}
