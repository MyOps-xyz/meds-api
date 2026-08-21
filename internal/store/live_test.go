//go:build live

// Vérification des budgets des lots 2 et 3 sur le jeu de données réel.
//
//	go test -tags live -v ./internal/store/
package store

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
)

func liveStore(t *testing.T) *Store {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := bdpm.NewClient("test").FetchAll(ctx, bdpm.DefaultFiles())
	if err != nil {
		t.Fatalf("FetchAll : %v", err)
	}
	q := bdpm.NewQuarantine()
	ds, err := bdpm.ParseAll(res, q)
	if err != nil {
		t.Fatalf("ParseAll : %v", err)
	}
	bdpm.CheckReferentialIntegrity(ds, q)
	ds.Derive()
	if err := bdpm.CheckPlausibility(ds, q, bdpm.DefaultValidationOptions()); err != nil {
		t.Fatalf("validation : %v", err)
	}

	dir := t.TempDir()
	out, err := snapshot.Write(ds, q, snapshot.WriteOptions{
		DataDir: dir, Generator: "test/live", Hash: res.Hash, Keep: 1,
	})
	if err != nil {
		t.Fatalf("Write : %v", err)
	}

	s, err := LoadStore(out.Dir)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}
	return s
}

// Critère d'acceptation T-17 : construction en moins de 500 ms, heap
// ≤ 100 Mio, compteurs égaux au manifest.
func TestLive_ConstructionDuStore(t *testing.T) {
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	s := liveStore(t)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	heapMiB := float64(after.HeapAlloc) / (1 << 20)

	t.Logf("construction en %v — heap %.1f Mio", s.BuildDuration, heapMiB)
	t.Logf("spécialités %d, présentations %d, substances %d, groupes %d",
		s.NbSpecialites(), s.NbPresentations(), s.NbSubstances(), s.NbGroupes())
	t.Logf("internement : %d formes, %d voies, %d titulaires — %d tokens indexés",
		s.FormeCount(), s.VoieCount(), s.TitulaireCount(), s.TokenCount())

	if s.BuildDuration > 500*time.Millisecond {
		t.Errorf("construction en %v, budget 500 ms", s.BuildDuration)
	}
	if heapMiB > 100 {
		t.Errorf("heap %.1f Mio après construction, budget 100 Mio", heapMiB)
	}

	// Les compteurs du Store correspondent au manifest.
	if got, want := s.NbSpecialites(), s.Manifest.Counts["specialites"]; got != want {
		t.Errorf("spécialités : %d dans le Store, %d au manifest", got, want)
	}
	if got, want := s.NbPresentations(), s.Manifest.Counts["presentations"]; got != want {
		t.Errorf("présentations : %d dans le Store, %d au manifest", got, want)
	}
	if got, want := s.NbSubstances(), s.Manifest.Counts["substances"]; got != want {
		t.Errorf("substances : %d dans le Store, %d au manifest", got, want)
	}

	// Tous les CIS et CIP13 sont résolus.
	for i := range s.AllSpecs() {
		sp := &s.AllSpecs()[i]
		if _, ok := s.byCIS[sp.CIS]; !ok {
			t.Fatalf("CIS %d absent de l'index", sp.CIS)
		}
	}
}

// Critères d'acceptation T-20 à T-23 sur le jeu réel.
func TestLive_RechercheSurDonneesReelles(t *testing.T) {
	s := liveStore(t)
	params := DefaultRankParams()

	// 8 959 mesurés le 15/08/2026. docs/05 §3 annonçait « ~25 000 » : c'était
	// une estimation, contredite par la mesure et par un décompte
	// indépendant en shell (9 410 tokens distincts avant repli des accents,
	// que le repli ramène sous 9 000). Les dénominations de la BDPM sont bien
	// plus répétitives que l'estimation ne le supposait.
	t.Logf("%d tokens distincts indexés", s.TokenCount())
	if s.TokenCount() < 7000 || s.TokenCount() > 15000 {
		t.Errorf("%d tokens indexés, hors de la plage mesurée [7000, 15000]", s.TokenCount())
	}

	// `doliprane` place DOLIPRANE en première position.
	res, stats := s.Search("doliprane", params)
	if len(res) == 0 {
		t.Fatal("aucun résultat pour « doliprane »")
	}
	t.Logf("doliprane : %d résultats, tête = %q", len(res), s.Spec(res[0].Spec).Denom)
	if !hasPrefixFold(s.Spec(res[0].Spec).Denom, "DOLIPRANE") {
		t.Errorf("tête de liste = %q, veut un DOLIPRANE", s.Spec(res[0].Spec).Denom)
	}
	// Le repli ne se déclenche pas quand la recherche exacte suffit.
	if stats.RepliDeclenche {
		t.Errorf("repli déclenché alors que la recherche exacte donne %d résultats", len(res))
	}

	// Volumétrie attendue du jeu de référence (docs/05 §7).
	para, _ := s.Search("paracetamol", params)
	if len(para) < 200 {
		t.Errorf("paracetamol = %d résultats, veut ≥ 200", len(para))
	}
	amlo, _ := s.Search("amlodipine", params)
	if len(amlo) < 157 {
		t.Errorf("amlodipine = %d résultats, veut ≥ 157 (découpage sur l'apostrophe)", len(amlo))
	}

	// Équivalence accentuée / non accentuée.
	accent, _ := s.Search("paracétamol", params)
	if len(accent) != len(para) {
		t.Errorf("paracétamol = %d résultats, paracetamol = %d : doivent être identiques",
			len(accent), len(para))
	}

	// Tolérance aux fautes : même tête de liste.
	for _, q := range []string{"paracetamo", "paracetamoll"} {
		got, st := s.Search(q, params)
		if len(got) == 0 {
			t.Errorf("%q ne retrouve rien", q)
			continue
		}
		if got[0].Spec != para[0].Spec {
			t.Errorf("%q donne %q en tête, veut %q (repli=%v)",
				q, s.Spec(got[0].Spec).Denom, s.Spec(para[0].Spec).Denom, st.RepliDeclenche)
		}
	}

	// Conjonction.
	conj, _ := s.Search("doliprane 1000", params)
	for _, r := range conj {
		if !containsFold(s.Spec(r.Spec).Denom, "1000") {
			t.Errorf("« doliprane 1000 » remonte %q, sans le dosage", s.Spec(r.Spec).Denom)
			break
		}
	}

	// Absence de faux positifs.
	if got, _ := s.Search("xyzzyqwerty", params); len(got) != 0 {
		t.Errorf("xyzzyqwerty = %d résultats, veut 0", len(got))
	}

	// Budgets de latence (docs/07-performance.md).
	measure := func(name string, f func()) time.Duration {
		start := time.Now()
		for i := 0; i < 200; i++ {
			f()
		}
		d := time.Since(start) / 200
		t.Logf("%s : %v en moyenne", name, d)
		return d
	}
	if d := measure("recherche exacte", func() { s.Search("doliprane", params) }); d > 5*time.Millisecond {
		t.Errorf("recherche exacte à %v, budget large de 5 ms dépassé", d)
	}
	if d := measure("autocomplétion", func() { s.Suggest("dolip", 20) }); d > time.Millisecond {
		t.Errorf("autocomplétion à %v, budget 1 ms", d)
	}
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && equalFold(s[:len(prefix)], prefix)
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
