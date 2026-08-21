package bdpm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConditions_JSONMatchesDoc(t *testing.T) {
	t.Parallel()

	content := "63852237\tréservé à l'usage professionnel DENTAIRE\n"
	conditions, err := ParseConditions("CIS_CPD_bdpm.txt", strings.NewReader(content), nil)
	if err != nil {
		t.Fatalf("ParseConditions() erreur inattendue : %v", err)
	}
	if len(conditions) != 1 {
		t.Fatalf("len(conditions) = %d, attendu 1", len(conditions))
	}

	got, err := json.Marshal(conditions[0])
	if err != nil {
		t.Fatalf("json.Marshal() erreur inattendue : %v", err)
	}
	want := `{ "cis": "63852237", "condition": "réservé à l'usage professionnel DENTAIRE" }`
	assertJSONEqual(t, got, []byte(want))
}

func TestParseConditions_MultipleConditionsPerCIS(t *testing.T) {
	t.Parallel()

	content := "63852237\tliste I\n63852237\tprescription hospitalière\n"
	conditions, err := ParseConditions("CIS_CPD_bdpm.txt", strings.NewReader(content), nil)
	if err != nil {
		t.Fatalf("ParseConditions() erreur inattendue : %v", err)
	}
	if len(conditions) != 2 {
		t.Fatalf("len(conditions) = %d, attendu 2", len(conditions))
	}
}

func TestParseConditions_CISInvalide_Quarantine(t *testing.T) {
	t.Parallel()

	content := "abc\tliste I\n"
	var reasons []string
	conditions, err := ParseConditions("CIS_CPD_bdpm.txt", strings.NewReader(content),
		func(file string, l int, reason, raw string) { reasons = append(reasons, reason) })
	if err != nil {
		t.Fatalf("ParseConditions() erreur inattendue : %v", err)
	}
	if len(conditions) != 0 {
		t.Fatalf("len(conditions) = %d, attendu 0", len(conditions))
	}
	if len(reasons) != 1 || reasons[0] != ReasonCISInvalide {
		t.Fatalf("reasons = %v, attendu [%q]", reasons, ReasonCISInvalide)
	}
}
