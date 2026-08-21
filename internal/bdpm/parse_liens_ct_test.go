package bdpm

import (
	"strings"
	"testing"
)

// TestParseLiensCT_P4_RewritesHTTPS prouve que les liens vers les avis
// complets sont réécrits en HTTPS (piège P4).
func TestParseLiensCT_P4_RewritesHTTPS(t *testing.T) {
	t.Parallel()

	content := "CT-12345\thttp://www.has-sante.fr/avis/CT-12345\n"
	liens, err := ParseLiensCT("HAS_LiensPageCT_bdpm.txt", strings.NewReader(content), nil)
	if err != nil {
		t.Fatalf("ParseLiensCT() erreur inattendue : %v", err)
	}
	if len(liens) != 1 {
		t.Fatalf("len(liens) = %d, attendu 1", len(liens))
	}
	if liens[0].CodeDossierHAS != "CT-12345" {
		t.Errorf("CodeDossierHAS = %q, attendu \"CT-12345\"", liens[0].CodeDossierHAS)
	}
	want := "https://www.has-sante.fr/avis/CT-12345"
	if liens[0].URL != want {
		t.Errorf("URL = %q, attendu %q", liens[0].URL, want)
	}
}

func TestParseLiensCT_CodeVide_Quarantine(t *testing.T) {
	t.Parallel()

	content := "\thttp://www.has-sante.fr/avis/CT-12345\n"
	var reasons []string
	liens, err := ParseLiensCT("HAS_LiensPageCT_bdpm.txt", strings.NewReader(content),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseLiensCT() erreur inattendue : %v", err)
	}
	if len(liens) != 0 {
		t.Fatalf("len(liens) = %d, attendu 0", len(liens))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCodeDossierHASVide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCodeDossierHASVide)
	}
}
