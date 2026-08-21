package bdpm

import (
	"slices"
	"testing"
)

// TestParsePriceCents_P4_RoundingNotTruncation prouve que 24,34 devient
// 2434 centimes et non 2433 : 24.34 * 100 vaut 2433.9999… en virgule
// flottante binaire, et une troncature perdrait un centime (piège P4,
// docs/01-analyse-source-bdpm.md §5).
func TestParsePriceCents_P4_RoundingNotTruncation(t *testing.T) {
	t.Parallel()

	got, err := parsePriceCents("24,34")
	if err != nil {
		t.Fatalf("parsePriceCents() erreur inattendue : %v", err)
	}
	if got == nil {
		t.Fatal("parsePriceCents() = nil, attendu un pointeur non nil")
	}
	if *got != 2434 {
		t.Errorf("parsePriceCents(\"24,34\") = %d, attendu 2434 (une troncature donnerait 2433)", *got)
	}
}

func TestParsePriceCents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    *int32
		wantErr bool
	}{
		{"absent devient nil, pas zéro", "", nil, false},
		{"espaces seuls", "   ", nil, false},
		{"virgule décimale", "24,34", int32Ptr(2434), false},
		{"point décimal", "24.34", int32Ptr(2434), false},
		{"séparateur de milliers espace", "1 234,56", int32Ptr(123456), false},
		{"entier sans décimale", "25", int32Ptr(2500), false},
		{"non convertible", "abc", nil, true},
		{"virgule milliers et décimale (piège non documenté)", "1,466,29", int32Ptr(146629), false},
		{"virgule milliers, entier sans décimale explicite", "7,518,58", int32Ptr(751858), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePriceCents(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePriceCents(%q) erreur = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			assertInt32PtrEqual(t, got, tt.want)
		})
	}
}

// TestParsePercentages_P4_SpaceInstability prouve que « 65% » et « 65 % »
// produisent tous deux [65] (piège P4).
func TestParsePercentages_P4_SpaceInstability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []int
	}{
		{"sans espace", "65%", []int{65}},
		{"avec espace", "65 %", []int{65}},
		{"multi-valué", "65%;30%", []int{65, 30}},
		{"vide", "", []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePercentages(tt.in)
			if err != nil {
				t.Fatalf("parsePercentages(%q) erreur inattendue : %v", tt.in, err)
			}
			assertIntSliceEqual(t, got, tt.want)
		})
	}
}

func TestParsePercentages_Invalid(t *testing.T) {
	t.Parallel()
	if _, err := parsePercentages("abc%"); err == nil {
		t.Error("parsePercentages(\"abc%\") : erreur attendue, aucune obtenue")
	}
}

// TestSplitMulti_P4_TrimsLeadingSpaces prouve que les espaces de tête des
// titulaires (« "  PHARMA DEVELOPPEMENT" ») sont retirés (piège P4).
func TestSplitMulti_P4_TrimsLeadingSpaces(t *testing.T) {
	t.Parallel()

	got := splitMulti("  PHARMA DEVELOPPEMENT")
	want := []string{"PHARMA DEVELOPPEMENT"}
	assertStringSliceEqual(t, got, want)
}

func TestSplitMulti(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "cutanée", []string{"cutanée"}},
		{"multi", "cutanée;orale;sublinguale", []string{"cutanée", "orale", "sublinguale"}},
		{"éléments vides retirés", "a;;b; ;c", []string{"a", "b", "c"}},
		{"vide", "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertStringSliceEqual(t, splitMulti(tt.in), tt.want)
		})
	}
}

func TestTrimOrNil(t *testing.T) {
	t.Parallel()

	if got := trimOrNil(""); got != nil {
		t.Errorf("trimOrNil(\"\") = %q, attendu nil", *got)
	}
	if got := trimOrNil("   "); got != nil {
		t.Errorf("trimOrNil(\"   \") = %q, attendu nil", *got)
	}
	got := trimOrNil("  texte  ")
	if got == nil || *got != "texte" {
		t.Errorf("trimOrNil(\"  texte  \") = %v, attendu \"texte\"", got)
	}
}

func TestParseDateFR(t *testing.T) {
	t.Parallel()

	got, err := parseDateFR("12/03/1998")
	if err != nil {
		t.Fatalf("parseDateFR() erreur inattendue : %v", err)
	}
	if got != "1998-03-12" {
		t.Errorf("parseDateFR(\"12/03/1998\") = %q, attendu \"1998-03-12\"", got)
	}
}

// TestParseDateFR_Invalid_Quarantine prouve qu'une date invalide est
// détectée par le primitif de normalisation (le parseur de fichier la
// met en quarantaine, voir parse_specialites_test.go).
func TestParseDateFR_Invalid(t *testing.T) {
	t.Parallel()

	tests := []string{"31/02/2020", "2020-03-12", "", "12/13/2020", "abc"}
	for _, in := range tests {
		if _, err := parseDateFR(in); err == nil {
			t.Errorf("parseDateFR(%q) : erreur attendue, aucune obtenue", in)
		}
	}
}

func TestParseDateFROptional(t *testing.T) {
	t.Parallel()

	got, err := parseDateFROptional("")
	if err != nil || got != nil {
		t.Errorf("parseDateFROptional(\"\") = (%v, %v), attendu (nil, nil)", got, err)
	}

	got, err = parseDateFROptional("30/07/2026")
	if err != nil {
		t.Fatalf("parseDateFROptional() erreur inattendue : %v", err)
	}
	if got == nil || *got != "2026-07-30" {
		t.Errorf("parseDateFROptional(\"30/07/2026\") = %v, attendu \"2026-07-30\"", got)
	}

	if _, err := parseDateFROptional("invalide"); err == nil {
		t.Error("parseDateFROptional(\"invalide\") : erreur attendue, aucune obtenue")
	}
}

// TestParseDateCompact prouve la conversion du format AAAAMMJJ, distinct
// du format JJ/MM/AAAA utilisé ailleurs, propre aux avis SMR et ASMR.
func TestParseDateCompact(t *testing.T) {
	t.Parallel()

	got, err := parseDateCompact("20110518")
	if err != nil {
		t.Fatalf("parseDateCompact() erreur inattendue : %v", err)
	}
	if got != "2011-05-18" {
		t.Errorf("parseDateCompact(\"20110518\") = %q, attendu \"2011-05-18\"", got)
	}

	if _, err := parseDateCompact("18/05/2011"); err == nil {
		t.Error("parseDateCompact(\"18/05/2011\") : erreur attendue (format JJ/MM/AAAA rejeté), aucune obtenue")
	}
}

func TestParseOuiNon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"Oui", true, false},
		{"Non", false, false},
		{"oui", false, true},
		{"", false, true},
	}
	for _, tt := range tests {
		got, err := parseOuiNon(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseOuiNon(%q) erreur = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseOuiNon(%q) = %v, attendu %v", tt.in, got, tt.want)
		}
	}
}

func TestParseTriBool(t *testing.T) {
	t.Parallel()

	got, err := parseTriBool("inconnu")
	if err != nil || got != nil {
		t.Errorf("parseTriBool(\"inconnu\") = (%v, %v), attendu (nil, nil) — null, pas false", got, err)
	}

	got, err = parseTriBool("oui")
	if err != nil || got == nil || !*got {
		t.Errorf("parseTriBool(\"oui\") = (%v, %v), attendu (true, nil)", got, err)
	}

	got, err = parseTriBool("non")
	if err != nil || got == nil || *got {
		t.Errorf("parseTriBool(\"non\") = (%v, %v), attendu (false, nil)", got, err)
	}

	if _, err := parseTriBool("peut-être"); err == nil {
		t.Error("parseTriBool(\"peut-être\") : erreur attendue, aucune obtenue")
	}
}

// TestHttpsify_P4_RewritesHTTPOnly prouve que http:// devient https://,
// et qu'une URL déjà en HTTPS n'est pas altérée (piège P4).
func TestHttpsify_P4_RewritesHTTPOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"http://base-donnees-publique.medicaments.gouv.fr/extrait.php?specid=60003620",
			"https://base-donnees-publique.medicaments.gouv.fr/extrait.php?specid=60003620"},
		{"https://deja-https.example/x", "https://deja-https.example/x"},
		{"  http://avec-espaces.example  ", "https://avec-espaces.example"},
	}
	for _, tt := range tests {
		if got := httpsify(tt.in); got != tt.want {
			t.Errorf("httpsify(%q) = %q, attendu %q", tt.in, got, tt.want)
		}
	}
}

// TestNormalizeNature_P3 prouve que SA et FT sont acceptés tels quels, et
// que ST — annoncé par la documentation officielle mais absent de la
// donnée mesurée — est accepté et converti en FT (piège P3).
func TestNormalizeNature_P3(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"SA", "SA", true},
		{"FT", "FT", true},
		{"ST", "FT", true},
		{"XX", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeNature(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("normalizeNature(%q) = (%q, %v), attendu (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestRomanASMRToNiveau(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"I", 1, true},
		{"II", 2, true},
		{"III", 3, true},
		{"IV", 4, true},
		{"V", 5, true},
		{"Commentaires sans chiffrage de l'ASMR", 0, false},
		{"V dans l'attente de données", 0, false},
		{"Commentaires", 0, false},
	}
	for _, tt := range tests {
		got, ok := romanASMRToNiveau(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("romanASMRToNiveau(%q) = (%d, %v), attendu (%d, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestIsDigits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		n    int
		want bool
	}{
		{"61266250", 8, true},
		{"6126625", 8, false},
		{"6126625a", 8, false},
		{"", 8, false},
		{"0000001", 7, true},
	}
	for _, tt := range tests {
		if got := isDigits(tt.in, tt.n); got != tt.want {
			t.Errorf("isDigits(%q, %d) = %v, attendu %v", tt.in, tt.n, got, tt.want)
		}
	}
}

// TestRuptureStatus_P5_DerivedFromCode prouve que le slug et le libellé
// sont dérivés du code, avec le slug documenté pour le code 2, et que le
// code 0 (hors énumération) est rejeté (piège P5).
func TestRuptureStatus_P5_DerivedFromCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code        int
		wantSlug    string
		wantLibelle string
		wantOK      bool
	}{
		{1, "rupture_stock", "Rupture de stock", true},
		{2, "tension_approvisionnement", "Tension d'approvisionnement", true},
		{3, "arret_commercialisation", "Arrêt de commercialisation", true},
		{4, "remise_disposition", "Remise à disposition", true},
		{0, "", "", false},
		{5, "", "", false},
	}
	for _, tt := range tests {
		slug, libelle, ok := ruptureStatus(tt.code)
		if ok != tt.wantOK || slug != tt.wantSlug || libelle != tt.wantLibelle {
			t.Errorf("ruptureStatus(%d) = (%q, %q, %v), attendu (%q, %q, %v)",
				tt.code, slug, libelle, ok, tt.wantSlug, tt.wantLibelle, tt.wantOK)
		}
	}
}

func TestIsDateDebutApproximative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"antérieure au seuil", "05/10/2023", true},
		{"bien antérieure", "01/01/2020", true},
		{"jour du seuil (non antérieure)", "06/10/2023", false},
		{"postérieure au seuil", "30/07/2026", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := isDateDebutApproximative(tt.in)
			if err != nil {
				t.Fatalf("isDateDebutApproximative(%q) erreur inattendue : %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("isDateDebutApproximative(%q) = %v, attendu %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsDateDebutApproximative_Invalid(t *testing.T) {
	t.Parallel()

	if _, err := isDateDebutApproximative("31/02/2023"); err == nil {
		t.Error("isDateDebutApproximative(\"31/02/2023\") : erreur attendue, aucune obtenue")
	}
}

// TestGenericTypeLabel_DiscontinuousEnum prouve que 0, 1, 2 et 4 sont
// reconnus mais que 3 — absent de l'énumération de la source — est
// rejeté, plutôt que supposé appartenir à un intervalle continu.
func TestGenericTypeLabel_DiscontinuousEnum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in     int
		want   string
		wantOK bool
	}{
		{0, "princeps", true},
		{1, "générique", true},
		{2, "par complémentarité posologique", true},
		{4, "substituable", true},
		{3, "", false},
		{5, "", false},
	}
	for _, tt := range tests {
		got, ok := genericTypeLabel(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("genericTypeLabel(%d) = (%q, %v), attendu (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

// --- Aides de comparaison, table-driven ---

func int32Ptr(v int32) *int32 { return &v }

func assertInt32PtrEqual(t *testing.T, got, want *int32) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("got = %v, want %v (nilité différente)", ptrInt32String(got), ptrInt32String(want))
	}
	if got != nil && *got != *want {
		t.Fatalf("got = %d, want %d", *got, *want)
	}
}

func ptrInt32String(v *int32) string {
	if v == nil {
		return "nil"
	}
	return "non-nil"
}

func assertIntSliceEqual(t *testing.T, got, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
}
