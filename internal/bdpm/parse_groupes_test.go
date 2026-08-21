package bdpm

import (
	"strings"
	"testing"
)

func groupeLine(typeGenerique string) string {
	return strings.Join([]string{
		"1", "CIMETIDINE 200 mg - TAGAMET 200 mg, comprimé pelliculé", "65383183", typeGenerique, "1",
	}, "\t") + "\n"
}

func TestParseGroupes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typeGenerique string
		wantType      int
		wantLibelle   string
	}{
		{"0", 0, "princeps"},
		{"1", 1, "générique"},
		{"2", 2, "par complémentarité posologique"},
		{"4", 4, "substituable"},
	}
	for _, tt := range tests {
		t.Run(tt.typeGenerique, func(t *testing.T) {
			t.Parallel()
			groupes, err := ParseGroupes("CIS_GENER_bdpm.txt", strings.NewReader(groupeLine(tt.typeGenerique)), nil)
			if err != nil {
				t.Fatalf("ParseGroupes() erreur inattendue : %v", err)
			}
			if len(groupes) != 1 {
				t.Fatalf("len(groupes) = %d, attendu 1", len(groupes))
			}
			got := groupes[0]
			if got.Type != tt.wantType || got.TypeLibelle != tt.wantLibelle {
				t.Errorf("Type/TypeLibelle = %d/%q, attendu %d/%q", got.Type, got.TypeLibelle, tt.wantType, tt.wantLibelle)
			}
			if got.GroupeID != "1" || got.CIS != "65383183" || got.Ordre != 1 {
				t.Errorf("groupes[0] = %+v, champs de base inattendus", got)
			}
		})
	}
}

func TestParseGroupes_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"1", "libellé de groupe", "6538318", "0", "1",
	}, "\t") + "\n"
	var reasons []string
	groupes, err := ParseGroupes("CIS_GENER_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseGroupes() erreur inattendue : %v", err)
	}
	if len(groupes) != 0 {
		t.Fatalf("len(groupes) = %d, attendu 0", len(groupes))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}

func TestParseGroupes_TypeNonEntier_Quarantine(t *testing.T) {
	t.Parallel()

	var reasons []string
	groupes, err := ParseGroupes("CIS_GENER_bdpm.txt", strings.NewReader(groupeLine("princeps")),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseGroupes() erreur inattendue : %v", err)
	}
	if len(groupes) != 0 {
		t.Fatalf("len(groupes) = %d, attendu 0", len(groupes))
	}
	if len(reasons) != 1 || reasons[0] != ReasonTypeGeneriqueInconnu {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonTypeGeneriqueInconnu)
	}
}

func TestParseGroupes_OrdreInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"1", "libellé de groupe", "65383183", "0", "premier",
	}, "\t") + "\n"
	var reasons []string
	groupes, err := ParseGroupes("CIS_GENER_bdpm.txt", strings.NewReader(line),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseGroupes() erreur inattendue : %v", err)
	}
	if len(groupes) != 0 {
		t.Fatalf("len(groupes) = %d, attendu 0", len(groupes))
	}
	if len(reasons) != 1 || reasons[0] != ReasonOrdreInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonOrdreInvalide)
	}
}

// TestParseGroupes_Type3Inconnu_Quarantine prouve que le type 3 — absent
// de l'énumération de la source, discontinue — est rejeté plutôt que
// supposé appartenir à un intervalle 0-4 continu.
func TestParseGroupes_Type3Inconnu_Quarantine(t *testing.T) {
	t.Parallel()

	var reasons []string
	groupes, err := ParseGroupes("CIS_GENER_bdpm.txt", strings.NewReader(groupeLine("3")),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseGroupes() erreur inattendue : %v", err)
	}
	if len(groupes) != 0 {
		t.Fatalf("len(groupes) = %d, attendu 0", len(groupes))
	}
	if len(reasons) != 1 || reasons[0] != ReasonTypeGeneriqueInconnu {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonTypeGeneriqueInconnu)
	}
}
