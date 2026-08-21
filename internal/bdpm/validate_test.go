package bdpm

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func spec(cis string) Specialite { return Specialite{CIS: cis, Denomination: "X"} }
func pres(cip13, cis string) Presentation {
	return Presentation{CIP13: cip13, CIP7: cip13[3:10], CIS: cis}
}

func TestQuarantine_CountsAndCopies(t *testing.T) {
	q := NewQuarantine()
	q.Add("a.txt", 3, ReasonCISInvalide, "ligne")
	q.Add("a.txt", 4, ReasonCISInvalide, "ligne")
	q.Add("b.txt", 1, ReasonPrixInvalide, "ligne")

	if got := q.Total(); got != 3 {
		t.Fatalf("Total = %d, veut 3", got)
	}
	if got := q.ByFile()["a.txt"]; got != 2 {
		t.Errorf("ByFile[a.txt] = %d, veut 2", got)
	}
	if got := q.ByReason()[ReasonCISInvalide]; got != 2 {
		t.Errorf("ByReason = %d, veut 2", got)
	}

	// Les cartes retournées sont des copies : les muter ne doit pas
	// contaminer le collecteur.
	q.ByFile()["a.txt"] = 999
	if got := q.ByFile()["a.txt"]; got != 2 {
		t.Errorf("la carte retournée n'est pas une copie : %d", got)
	}
}

func TestQuarantine_RawTruncated(t *testing.T) {
	q := NewQuarantine()
	q.Add("a.txt", 1, ReasonCISInvalide, strings.Repeat("x", MaxQuarantineRawLen*2))
	if got := len(q.Entries()[0].Raw); got != MaxQuarantineRawLen {
		t.Fatalf("Raw = %d octets, veut %d", got, MaxQuarantineRawLen)
	}
}

func TestQuarantine_EntriesCappedButCountsContinue(t *testing.T) {
	q := NewQuarantine()
	for i := 0; i < MaxQuarantineEntries+50; i++ {
		q.Add("a.txt", i, ReasonCISInvalide, "x")
	}
	if got := len(q.Entries()); got != MaxQuarantineEntries {
		t.Errorf("Entries = %d, veut %d", got, MaxQuarantineEntries)
	}
	if got := q.Total(); got != MaxQuarantineEntries+50 {
		t.Errorf("Total = %d : les compteurs doivent continuer au-delà du plafond", got)
	}
}

func TestQuarantine_ConcurrentAdd(t *testing.T) {
	q := NewQuarantine()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				q.Add("f.txt", j, ReasonCISInvalide, "x")
				q.AddUnknown("f.txt", "statut", "inconnu")
			}
		}(i)
	}
	wg.Wait()
	if got := q.Total(); got != 1000 {
		t.Fatalf("Total = %d, veut 1000", got)
	}
	if u := q.UnknownValues(); len(u) != 1 || u[0].Count != 1000 {
		t.Fatalf("UnknownValues = %+v", u)
	}
}

// Critère d'acceptation T-09 : les CIS orphelins sont mis en quarantaine
// sans faire échouer l'ingestion.
func TestReferentialIntegrity_CISOrphelin(t *testing.T) {
	d := &Dataset{
		Specialites:   []Specialite{spec("60002283"), spec("60002284")},
		Presentations: []Presentation{pres("3400930000011", "60002283"), pres("3400930000028", "99999999")},
		Composants:    []Composant{{CIS: "60002283"}, {CIS: "99999999"}},
		Conditions:    []Condition{{CIS: "99999999", Condition: "liste I"}},
	}
	q := NewQuarantine()
	CheckReferentialIntegrity(d, q)

	if len(d.Presentations) != 1 || d.Presentations[0].CIS != "60002283" {
		t.Errorf("présentations = %+v, veut la seule non orpheline", d.Presentations)
	}
	if len(d.Composants) != 1 {
		t.Errorf("composants = %d, veut 1", len(d.Composants))
	}
	if len(d.Conditions) != 0 {
		t.Errorf("conditions = %d, veut 0", len(d.Conditions))
	}
	// Les orphelins sont hors périmètre, pas des rejets : le taux surveillé
	// ne doit pas bouger (ADR 0007).
	if got := q.OutOfScopeByReason()[ReasonCISOrphelin]; got != 3 {
		t.Errorf("cis_orphelin hors périmètre = %d, veut 3", got)
	}
	if got := q.Total(); got != 0 {
		t.Errorf("rejets = %d, veut 0 : un orphelin n'est pas une malformation", got)
	}
	// Les spécialités elles-mêmes sont intactes.
	if len(d.Specialites) != 2 {
		t.Errorf("spécialités = %d, veut 2", len(d.Specialites))
	}
}

// Une rupture dont le CIP13 est inconnu conserve son rattachement à la
// spécialité : l'information de santé prime sur la précision du
// conditionnement.
func TestReferentialIntegrity_RuptureCIP13OrphelinNeutralise(t *testing.T) {
	inconnu := "3400999999999"
	d := &Dataset{
		Specialites:   []Specialite{spec("60002283")},
		Presentations: []Presentation{pres("3400930000011", "60002283")},
		Ruptures:      []Rupture{{CIS: "60002283", CIP13: &inconnu}},
	}
	q := NewQuarantine()
	CheckReferentialIntegrity(d, q)

	if len(d.Ruptures) != 1 {
		t.Fatalf("la rupture ne doit pas être écartée : %d restantes", len(d.Ruptures))
	}
	if d.Ruptures[0].CIP13 != nil {
		t.Errorf("CIP13 = %q, veut nil (neutralisé)", *d.Ruptures[0].CIP13)
	}
	if got := q.OutOfScopeByReason()[ReasonCIP13Orphelin]; got != 1 {
		t.Errorf("cip13_orphelin hors périmètre = %d, veut 1", got)
	}
}

func TestReferentialIntegrity_Doublons(t *testing.T) {
	d := &Dataset{
		Specialites: []Specialite{spec("60002283"), spec("60002283")},
		Presentations: []Presentation{
			pres("3400930000011", "60002283"),
			pres("3400930000011", "60002283"),
		},
	}
	q := NewQuarantine()
	CheckReferentialIntegrity(d, q)

	if len(d.Specialites) != 1 {
		t.Errorf("spécialités = %d, veut 1 après déduplication", len(d.Specialites))
	}
	if len(d.Presentations) != 1 {
		t.Errorf("présentations = %d, veut 1 après déduplication", len(d.Presentations))
	}
	if q.ByReason()[ReasonCISDuplique] != 1 || q.ByReason()[ReasonCIP13Duplique] != 1 {
		t.Errorf("motifs de doublon manquants : %+v", q.ByReason())
	}
}

func manySpecs(n int) []Specialite {
	out := make([]Specialite, n)
	for i := range out {
		out[i] = spec(pad8(i))
	}
	return out
}

func pad8(i int) string {
	s := make([]byte, 8)
	for k := 7; k >= 0; k-- {
		s[k] = byte('0' + i%10)
		i /= 10
	}
	return string(s)
}

func validDataset(nSpecs int) *Dataset {
	return &Dataset{
		Specialites:   manySpecs(nSpecs),
		Presentations: []Presentation{pres("3400930000011", "00000000")},
		Composants:    []Composant{{CIS: "00000000"}},
	}
}

// Critère d'acceptation T-09 : un taux de rejet simulé de 5 % fait échouer.
func TestPlausibility_TauxDeRejet(t *testing.T) {
	d := validDataset(12000)
	q := NewQuarantine()
	for i := 0; i < 700; i++ { // ~5,5 % de 12 002
		q.Add("CIS_bdpm.txt", i, ReasonCISInvalide, "x")
	}

	err := CheckPlausibility(d, q, DefaultValidationOptions())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Check != CheckTauxRejet {
		t.Fatalf("err = %v, veut un dépassement de taux de rejet", err)
	}
}

// Un taux sous le seuil ne fait pas échouer.
func TestPlausibility_TauxDeRejetSousSeuil(t *testing.T) {
	d := validDataset(12000)
	q := NewQuarantine()
	for i := 0; i < 4; i++ {
		q.Add("CIS_CIP_bdpm.txt", i, ReasonPrixInvalide, "x")
	}
	if err := CheckPlausibility(d, q, DefaultValidationOptions()); err != nil {
		t.Fatalf("4 rejets sur 12 002 ne doivent pas condamner l'ingestion : %v", err)
	}
}

// Critère d'acceptation T-09 : un fichier vide fait échouer.
func TestPlausibility_FichierVide(t *testing.T) {
	d := validDataset(12000)
	d.Composants = nil
	q := NewQuarantine()

	err := CheckPlausibility(d, q, DefaultValidationOptions())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Check != CheckFichierVide {
		t.Fatalf("err = %v, veut un échec pour fichier vide", err)
	}
	if !strings.Contains(err.Error(), FileComposants) {
		t.Errorf("le message doit nommer le fichier fautif : %v", err)
	}
}

func TestPlausibility_PlancherSpecialites(t *testing.T) {
	d := validDataset(9999)
	err := CheckPlausibility(d, NewQuarantine(), DefaultValidationOptions())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Check != CheckPlancherSpecs {
		t.Fatalf("err = %v, veut un échec de plancher", err)
	}
}

// Critère d'acceptation T-09 : une chute de 50 % du nombre de spécialités
// fait échouer.
func TestPlausibility_ChuteDe50Pourcent(t *testing.T) {
	d := validDataset(15000)
	opts := DefaultValidationOptions()
	opts.PreviousCounts = map[string]int{"specialites": 30000}

	err := CheckPlausibility(d, NewQuarantine(), opts)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Check != CheckVariationCompteur {
		t.Fatalf("err = %v, veut un échec de variation", err)
	}
}

// La toute première ingestion n'a pas de snapshot précédent : le contrôle
// de variation ne doit pas s'y appliquer.
func TestPlausibility_PremiereIngestionSansPrecedent(t *testing.T) {
	d := validDataset(15000)
	if err := CheckPlausibility(d, NewQuarantine(), DefaultValidationOptions()); err != nil {
		t.Fatalf("démarrage à froid refusé : %v", err)
	}
}

func TestPlausibility_VariationTolérée(t *testing.T) {
	d := validDataset(15000)
	opts := DefaultValidationOptions()
	opts.PreviousCounts = map[string]int{"specialites": 14000} // +7,1 %
	if err := CheckPlausibility(d, NewQuarantine(), opts); err != nil {
		t.Fatalf("une variation de 7 %% doit passer : %v", err)
	}
}

func TestWarnings_Deterministes(t *testing.T) {
	q := NewQuarantine()
	q.Add("b.txt", 1, ReasonPrixInvalide, "x")
	q.AddOutOfScope("a.txt", 1, ReasonCISOrphelin, "x")
	q.AddUnknown("a.txt", "statut_amm", "Nouveau statut")

	first := Warnings(q)
	for i := 0; i < 10; i++ {
		if got := Warnings(q); len(got) != len(first) || got[0] != first[0] {
			t.Fatalf("Warnings n'est pas déterministe : %v puis %v", first, got)
		}
	}
	if len(first) != 3 {
		t.Fatalf("Warnings = %v, veut 2 motifs + 1 valeur inconnue", first)
	}
}
