package store

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
)

// Critère d'acceptation T-13 : unsafe.Sizeof(Spec{}) ≤ 128 octets.
func TestSpec_TailleMemoire(t *testing.T) {
	if got := unsafe.Sizeof(Spec{}); got > 128 {
		t.Errorf("unsafe.Sizeof(Spec{}) = %d octets, budget 128", got)
	}
	t.Logf("Spec = %d o, Pres = %d o, Compo = %d o, Avis = %d o",
		unsafe.Sizeof(Spec{}), unsafe.Sizeof(Pres{}), unsafe.Sizeof(Compo{}), unsafe.Sizeof(Avis{}))
}

// Critère d'acceptation T-14 : Resolve(Intern(s)) == s pour toute chaîne.
func TestInterner_AllerRetour(t *testing.T) {
	in := NewInterner("test")
	for _, s := range []string{
		"comprimé pelliculé", "gélule", "solution injectable", "",
		"crème", "comprimé pelliculé", "ÉLIXIR",
	} {
		if got := in.Resolve(in.Intern(s)); got != s {
			t.Errorf("Resolve(Intern(%q)) = %q", s, got)
		}
	}
	// Une valeur répétée ne consomme pas de place supplémentaire.
	if in.Len() != 6 { // 5 distinctes + la chaîne vide en position 0
		t.Errorf("Len = %d, veut 6", in.Len())
	}
	if id := in.Intern(""); id != 0 {
		t.Errorf("la chaîne vide doit avoir l'identifiant 0, pas %d", id)
	}
}

func TestInterner_ResolveHorsBornes(t *testing.T) {
	in := NewInterner("test")
	in.Intern("a")
	if got := in.Resolve(9999); got != "" {
		t.Errorf("Resolve hors bornes = %q, veut une chaîne vide sans panique", got)
	}
}

func TestEnumTable_Debordement(t *testing.T) {
	e := newEnumTable("test")
	// 255 modalités tiennent dans un uint8 (le code 0 étant réservé à la
	// chaîne vide) ; la 256ᵉ ne tient plus.
	for i := 0; i < 255; i++ {
		if _, err := e.intern(string(rune('a'+i%26)) + strings.Repeat("x", i)); err != nil {
			t.Fatalf("intern %d : %v", i, err)
		}
	}
	if _, err := e.intern("une-modalite-de-trop"); err == nil {
		t.Fatal("le débordement doit être une erreur de construction explicite")
	}
}

// Critère d'acceptation T-15 : Get renvoie exactement les éléments du
// parent, un parent sans enfant renvoie une slice vide non nulle, et Get
// n'alloue pas.
func TestCSR_Get(t *testing.T) {
	type kv struct {
		parent int32
		v      string
	}
	vals := []kv{{2, "c"}, {0, "a"}, {2, "d"}, {0, "b"}, {4, "e"}}
	c := BuildCSR(5, vals, func(k kv) int32 { return k.parent })

	cases := map[int32][]string{
		0: {"a", "b"},
		1: {},
		2: {"c", "d"},
		3: {},
		4: {"e"},
	}
	for parent, want := range cases {
		got := c.Get(parent)
		if len(got) != len(want) {
			t.Errorf("Get(%d) = %d éléments, veut %d", parent, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i].v != want[i] {
				t.Errorf("Get(%d)[%d] = %q, veut %q", parent, i, got[i].v, want[i])
			}
		}
		if got == nil {
			t.Errorf("Get(%d) = nil : l'API sérialiserait null au lieu de []", parent)
		}
	}

	// Hors bornes : pas de panique, une slice vide.
	if got := c.Get(99); got == nil || len(got) != 0 {
		t.Errorf("Get(99) = %v, veut une slice vide non nulle", got)
	}
	if got := c.Get(-1); got == nil || len(got) != 0 {
		t.Errorf("Get(-1) = %v, veut une slice vide non nulle", got)
	}
}

func TestCSR_RelationVide(t *testing.T) {
	type kv struct{ parent int32 }
	c := BuildCSR(3, []kv(nil), func(k kv) int32 { return k.parent })
	got := c.Get(0)
	if got == nil {
		t.Fatal("une relation entièrement vide doit tout de même renvoyer une slice non nulle")
	}
}

// Critère d'acceptation T-15 : aucune allocation dans Get.
func BenchmarkCSR_Get(b *testing.B) {
	type kv struct {
		parent int32
		v      int
	}
	vals := make([]kv, 100000)
	for i := range vals {
		vals[i] = kv{parent: int32(i % 15857), v: i}
	}
	c := BuildCSR(15857, vals, func(k kv) int32 { return k.parent })

	b.ReportAllocs()
	b.ResetTimer()
	var n int
	for i := 0; i < b.N; i++ {
		n += len(c.Get(int32(i % 15857)))
	}
	_ = n
}

func TestCSR_GetNAllouePas(t *testing.T) {
	type kv struct {
		parent int32
		v      int
	}
	vals := make([]kv, 1000)
	for i := range vals {
		vals[i] = kv{parent: int32(i % 100), v: i}
	}
	c := BuildCSR(100, vals, func(k kv) int32 { return k.parent })

	allocs := testingAllocsPerRun(100, func() {
		for i := int32(0); i < 100; i++ {
			_ = c.Get(i)
		}
	})
	if allocs != 0 {
		t.Errorf("Get alloue %v fois par appel, veut 0", allocs)
	}
}

// Critère d'acceptation T-19 : l'index et la requête empruntent le même
// code de normalisation.
func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"PARACÉTAMOL":                          "paracetamol",
		"BÉSILATE D'AMLODIPINE 5 mg, comprimé": "besilate d amlodipine 5 mg comprime",
		"DOLIPRANE 1000 mg":                    "doliprane 1000 mg",
		"":                                     "",
		"   ":                                  "",
		"<script>alert(1)</script>":            "script alert 1 script",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, veut %q", in, got, want)
		}
	}
}

func TestNormalizeSymmetry(t *testing.T) {
	// La propriété qui compte : indexer une chaîne puis la rechercher
	// telle quelle doit produire les mêmes tokens des deux côtés.
	for _, s := range []string{
		"PARACÉTAMOL", "BÉSILATE D'AMLODIPINE", "DOLIPRANE 1000 mg, comprimé",
		"Sanofi Aventis", "A 313 50 000 U.I.",
	} {
		indexTokens := Tokenize(s)
		queryTokens := Tokenize(strings.ToLower(s))
		if len(indexTokens) != len(queryTokens) {
			t.Fatalf("asymétrie sur %q : index %v, requête %v", s, indexTokens, queryTokens)
		}
		for i := range indexTokens {
			if indexTokens[i] != queryTokens[i] {
				t.Errorf("asymétrie sur %q : %q vs %q", s, indexTokens[i], queryTokens[i])
			}
		}
	}
}

func TestTokenize_ApostropheEtTokensCourts(t *testing.T) {
	got := Tokenize("D'AMLODIPINE à 5 mg")
	want := []string{"amlodipine", "mg"}
	if len(got) != len(want) {
		t.Fatalf("Tokenize = %v, veut %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, veut %q", i, got[i], want[i])
		}
	}
}

// --- Store bâti sur un jeu réduit mais représentatif -----------------------

func testDataset() *bdpm.Dataset {
	prix := int32(2434)
	agrement := true
	statutBdm := "Alerte"
	d := &bdpm.Dataset{
		Specialites: []bdpm.Specialite{
			{CIS: "60002283", Denomination: "DOLIPRANE 1000 mg, comprimé",
				FormePharmaceutique: "comprimé", VoiesAdministration: []string{"orale"},
				StatutAMM: "Autorisation active", EtatCommercialisation: "Commercialisée",
				DateAMM: "1997-03-19", Titulaires: []string{"OPELLA HEALTHCARE FRANCE"},
				StatutBdm: &statutBdm},
			{CIS: "60002284", Denomination: "DOLIPRANE CODEINE, comprimé",
				FormePharmaceutique: "comprimé", StatutAMM: "Autorisation active",
				EtatCommercialisation: "Non commercialisée", DateAMM: "2001-01-01",
				Titulaires: []string{"OPELLA HEALTHCARE FRANCE"}},
			{CIS: "60002285", Denomination: "AMLOR 5 mg, gélule",
				FormePharmaceutique: "gélule", StatutAMM: "Autorisation active",
				EtatCommercialisation: "Commercialisée", DateAMM: "1990-06-01",
				Titulaires: []string{"VIATRIS SANTE"}},
		},
		Presentations: []bdpm.Presentation{
			{CIP13: "3400930000011", CIP7: "3000001", CIS: "60002283",
				Libelle: "plaquette de 8", StatutAdministratif: "Présentation active",
				EtatCommercialisation: "Déclaration de commercialisation",
				DateDeclaration:       "1998-01-01", PrixMedicamentCents: &prix,
				AgrementCollectivites: &agrement, TauxRemboursement: []int{65}},
			{CIP13: "3400930000028", CIP7: "3000002", CIS: "60002283",
				Libelle: "plaquette de 16", StatutAdministratif: "Présentation active",
				EtatCommercialisation: "Déclaration de commercialisation"},
		},
		Composants: []bdpm.Composant{
			{CIS: "60002283", CodeSubstance: "42215", DenominationSubstance: "PARACÉTAMOL",
				Dosage: "1000 mg", Nature: "SA", NumeroLiaison: 1},
			{CIS: "60002284", CodeSubstance: "42215", DenominationSubstance: "PARACÉTAMOL",
				Dosage: "400 mg", Nature: "SA", NumeroLiaison: 1},
			{CIS: "60002285", CodeSubstance: "12345", DenominationSubstance: "BÉSILATE D'AMLODIPINE",
				Dosage: "6,94 mg", Nature: "SA", NumeroLiaison: 1},
		},
		AvisSMR: []bdpm.AvisSMR{
			{CIS: "60002283", CodeDossierHAS: "CT-1234", DateAvis: "2015-06-10",
				Valeur: "Important", Libelle: "Le SMR reste important."},
		},
		Conditions: []bdpm.Condition{{CIS: "60002285", Condition: "liste I"}},
		Ruptures: []bdpm.Rupture{
			{CIS: "60002283", CodeStatut: 2, Statut: "tension", Libelle: "Tension",
				DateDebut: "2026-01-05", DateMiseAJour: "2026-02-01"},
		},
		MITM: []bdpm.InfoMITM{{CIS: "60002285", CodeATC: "C08CA01", Denomination: "AMLODIPINE"}},
		Appartenances: []bdpm.GroupeAppartenance{
			{GroupeID: "1", Libelle: "PARACETAMOL 1000 mg", CIS: "60002283", Type: 0,
				TypeLibelle: "princeps", Ordre: 1},
			{GroupeID: "1", Libelle: "PARACETAMOL 1000 mg", CIS: "60002284", Type: 1,
				TypeLibelle: "générique", Ordre: 2},
		},
	}
	d.Derive()
	return d
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Build(testDataset(), &snapshot.Manifest{Hash: "test", Version: "2026-08-15T00:00:00Z"})
	if err != nil {
		t.Fatalf("Build : %v", err)
	}
	s.BuildSearchIndex()
	return s
}

// Critère d'acceptation T-16 : les codes sont résolus, CIP7 et CIP13 mènent
// à la même présentation, un code inconnu ne panique pas.
func TestStore_IndexAccesDirect(t *testing.T) {
	s := testStore(t)

	id, ok := s.SpecByCIS("60002283")
	if !ok || s.Spec(id).Denom != "DOLIPRANE 1000 mg, comprimé" {
		t.Fatalf("SpecByCIS = %v, %v", id, ok)
	}

	p13, ok13 := s.PresByCIP("3400930000011")
	p7, ok7 := s.PresByCIP("3000001")
	if !ok13 || !ok7 || p13 != p7 {
		t.Errorf("CIP7 (%v, %v) et CIP13 (%v, %v) doivent mener à la même présentation", p7, ok7, p13, ok13)
	}

	if _, ok := s.SubByCode("42215"); !ok {
		t.Error("SubByCode(42215) introuvable")
	}
	if _, ok := s.GrpByID("1"); !ok {
		t.Error("GrpByID(1) introuvable")
	}

	// Codes inconnus ou hostiles : pas de panique, pas de résultat.
	for _, bad := range []string{"", "99999999", "abcdefgh", "-1", "999999999999999999999999"} {
		if _, ok := s.SpecByCIS(bad); ok {
			t.Errorf("SpecByCIS(%q) ne devrait rien trouver", bad)
		}
		if _, ok := s.PresByCIP(bad); ok {
			t.Errorf("PresByCIP(%q) ne devrait rien trouver", bad)
		}
	}
}

func TestStore_Relations(t *testing.T) {
	s := testStore(t)
	id, _ := s.SpecByCIS("60002283")

	if got := len(s.Presentations(id)); got != 2 {
		t.Errorf("présentations = %d, veut 2", got)
	}
	if got := len(s.Composants(id)); got != 1 {
		t.Errorf("composants = %d, veut 1", got)
	}
	if got := len(s.AvisSMR(id)); got != 1 {
		t.Errorf("avis SMR = %d, veut 1", got)
	}
	if got := len(s.Ruptures(id)); got != 1 {
		t.Errorf("ruptures = %d, veut 1", got)
	}

	// Une spécialité sans présentation renvoie une slice vide, pas nil.
	other, _ := s.SpecByCIS("60002285")
	if got := s.Presentations(other); got == nil || len(got) != 0 {
		t.Errorf("Presentations sans enfant = %v, veut une slice vide non nulle", got)
	}
}

// L'absence d'un prix ne doit jamais être confondue avec un prix nul.
func TestStore_PrixAbsentDistinctDeZero(t *testing.T) {
	s := testStore(t)
	id, _ := s.SpecByCIS("60002283")
	pres := s.Presentations(id)

	v, ok := pres[0].PrixMedicament()
	if !ok || v != 2434 {
		t.Errorf("prix renseigné = %d, %v ; veut 2434, true", v, ok)
	}
	if _, ok := pres[1].PrixMedicament(); ok {
		t.Errorf("prix absent rapporté comme présent")
	}
	if _, ok := pres[1].Agrement(); ok {
		t.Errorf("agrément absent rapporté comme présent")
	}
	if agr, ok := pres[0].Agrement(); !ok || !agr {
		t.Errorf("agrément = %v, %v ; veut true, true", agr, ok)
	}
}

func TestStore_ResolutionDesChampsInternes(t *testing.T) {
	s := testStore(t)
	id, _ := s.SpecByCIS("60002283")
	sp := s.Spec(id)

	if got := s.Forme(sp); got != "comprimé" {
		t.Errorf("Forme = %q", got)
	}
	if got := s.Voies(sp); len(got) != 1 || got[0] != "orale" {
		t.Errorf("Voies = %v", got)
	}
	if got := s.Titulaires(sp); len(got) != 1 || got[0] != "OPELLA HEALTHCARE FRANCE" {
		t.Errorf("Titulaires = %v", got)
	}
	if got := s.StatutAMM(sp); got != "Autorisation active" {
		t.Errorf("StatutAMM = %q", got)
	}
	if got := s.DateAMM(sp); got != "1997-03-19" {
		t.Errorf("DateAMM = %q, veut 1997-03-19", got)
	}
	if v, ok := s.StatutBdm(sp); !ok || v != "Alerte" {
		t.Errorf("StatutBdm = %q, %v", v, ok)
	}
	// Une spécialité sans statut BdM le rapporte comme absent.
	other, _ := s.SpecByCIS("60002285")
	if _, ok := s.StatutBdm(s.Spec(other)); ok {
		t.Errorf("un statut BdM absent ne doit pas être rapporté comme présent")
	}
}

func TestStore_Recherche(t *testing.T) {
	s := testStore(t)
	params := DefaultRankParams()

	// Correspondance exacte en tête.
	res, _ := s.Search("doliprane", params)
	if len(res) == 0 {
		t.Fatal("aucun résultat pour « doliprane »")
	}
	if s.Spec(res[0].Spec).Denom != "DOLIPRANE 1000 mg, comprimé" {
		t.Errorf("premier résultat = %q", s.Spec(res[0].Spec).Denom)
	}

	// Le repli ne doit pas se déclencher quand la recherche exacte suffit.
	// Le seuil est abaissé ici parce que le jeu de test ne compte que trois
	// spécialités ; il est vérifié à sa valeur réelle sur données réelles
	// (TestLive_RechercheSurDonneesReelles).
	strict := params
	strict.SeuilRepli = 1
	if _, stats := s.Search("doliprane", strict); stats.RepliDeclenche {
		t.Errorf("le repli s'est déclenché alors que la recherche exacte suffisait")
	}

	// Insensibilité à la casse et aux accents.
	for _, q := range []string{"DOLIPRANE", "Doliprane"} {
		alt, _ := s.Search(q, params)
		if len(alt) == 0 || alt[0].Spec != res[0].Spec {
			t.Errorf("%q ne donne pas la même tête de liste", q)
		}
	}
	accented, _ := s.Search("paracétamol", params)
	plain, _ := s.Search("paracetamol", params)
	if len(accented) != len(plain) || len(plain) == 0 {
		t.Errorf("paracétamol (%d) et paracetamol (%d) doivent être équivalents", len(accented), len(plain))
	}

	// Découpage sur l'apostrophe : « amlodipine » trouve « D'AMLODIPINE ».
	if got, _ := s.Search("amlodipine", params); len(got) == 0 {
		t.Errorf("« amlodipine » ne trouve pas BÉSILATE D'AMLODIPINE")
	}

	// Conjonction : tous les tokens doivent être présents.
	if got, _ := s.Search("doliprane codeine", params); len(got) != 1 {
		t.Errorf("« doliprane codeine » = %d résultats, veut 1 (conjonction)", len(got))
	}

	// Préfixe pendant la frappe.
	if got, _ := s.Search("dolip", params); len(got) != 2 {
		t.Errorf("« dolip » = %d résultats, veut 2", len(got))
	}

	// Titulaire indexé.
	if got, _ := s.Search("viatris", params); len(got) != 1 {
		t.Errorf("recherche par titulaire = %d résultats, veut 1", len(got))
	}

	// Entrées sans résultat ou hostiles : zéro résultat, aucune panique.
	for _, q := range []string{"xyzzyqwerty", "<script>", "", "   ", strings.Repeat("a", 5000)} {
		if got, _ := s.Search(q, params); len(got) != 0 {
			t.Errorf("Search(%q) = %d résultats, veut 0", q, len(got))
		}
	}
}

// Critère d'acceptation T-21 : la tolérance aux fautes retrouve la cible,
// et ne se déclenche pas quand la recherche exacte suffit.
func TestStore_ToleranceAuxFautes(t *testing.T) {
	s := testStore(t)
	params := DefaultRankParams()

	ref, _ := s.Search("paracetamol", params)
	if len(ref) == 0 {
		t.Fatal("référence vide")
	}

	for _, q := range []string{"paracetamo", "paracetamoll"} {
		got, stats := s.Search(q, params)
		if len(got) == 0 {
			t.Errorf("%q ne retrouve rien", q)
			continue
		}
		if !stats.RepliDeclenche && q == "paracetamoll" {
			t.Errorf("%q aurait dû déclencher le repli", q)
		}
		if got[0].Spec != ref[0].Spec {
			t.Errorf("%q donne %q en tête, veut %q", q,
				s.Spec(got[0].Spec).Denom, s.Spec(ref[0].Spec).Denom)
		}
	}
}

// Critère d'acceptation T-22 : les spécialités commercialisées précèdent les
// non commercialisées à pertinence égale.
func TestStore_ClassementCommercialisees(t *testing.T) {
	s := testStore(t)
	res, _ := s.Search("doliprane", DefaultRankParams())
	if len(res) < 2 {
		t.Fatalf("%d résultats, veut au moins 2", len(res))
	}
	if s.EtatCommercialisation(s.Spec(res[0].Spec)) != "Commercialisée" {
		t.Errorf("la spécialité commercialisée doit précéder : tête = %q (%s)",
			s.Spec(res[0].Spec).Denom, s.EtatCommercialisation(s.Spec(res[0].Spec)))
	}
}

func TestStore_Suggest(t *testing.T) {
	s := testStore(t)
	sugg := s.Suggest("dolip", 10)
	if len(sugg) == 0 || sugg[0].Token != "doliprane" {
		t.Errorf("Suggest(dolip) = %+v", sugg)
	}
	if got := s.Suggest("zzzz", 10); len(got) != 0 {
		t.Errorf("Suggest sans correspondance = %+v", got)
	}
	if got := s.Suggest("", 10); got != nil {
		t.Errorf("Suggest(\"\") = %+v, veut nil", got)
	}
}

// Critère d'acceptation T-18 : 100 lecteurs concurrents pendant 10 bascules,
// aucune course, aucune réponse incohérente.
func TestHolder_ConcurrentReadDuringSwap(t *testing.T) {
	h := NewHolder()
	h.Store(testStore(t))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := h.Load()
				if s == nil {
					t.Error("Load a renvoyé nil après publication")
					return
				}
				// Une réponse cohérente : le CIS résolu mène bien à la bonne
				// spécialité, dans la version chargée et pas une autre.
				id, ok := s.SpecByCIS("60002283")
				if !ok || s.Spec(id).CIS != 60002283 {
					t.Error("réponse incohérente pendant une bascule")
					return
				}
				_ = len(s.Presentations(id))
			}
		}()
	}

	for i := 0; i < 10; i++ {
		h.Store(testStore(t))
	}
	close(stop)
	wg.Wait()
}

func TestHolder_VideAvantPublication(t *testing.T) {
	h := NewHolder()
	if h.Ready() || h.Load() != nil {
		t.Fatal("un Holder vierge doit rapporter qu'aucun Store n'est publié")
	}
}

// testingAllocsPerRun encapsule testing.AllocsPerRun pour garder les tests
// lisibles.
func testingAllocsPerRun(runs int, f func()) float64 {
	return testing.AllocsPerRun(runs, f)
}

// Normalize est appelée depuis chaque requête de recherche et depuis la
// construction d'un Store : elle s'exécute en parallèle en permanence.
//
// Une première rédaction partageait un transform.Chain au niveau du paquet,
// dont transform.String mute les tampons internes. Le détecteur de course
// l'a signalé entre une construction de Store et une recherche concurrente.
// La défaillance n'aurait pas été un plantage mais des résultats de recherche
// silencieusement faux — d'où ce test, qui doit rester sous `-race`.
func TestNormalize_SansCourseNiCorruption(t *testing.T) {
	inputs := []string{
		"BÉSILATE D'AMLODIPINE 5 mg, comprimé",
		"PARACÉTAMOL",
		"ACIDE ACÉTYLSALICYLIQUE & VITAMINE C",
		"DOLIPRANE 1000 mg, comprimé pelliculé sécable",
		"A 313 50 000 U.I. pommade",
	}
	// Résultats de référence, calculés séquentiellement.
	want := make([]string, len(inputs))
	for i, in := range inputs {
		want[i] = Normalize(in)
	}

	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				for i, in := range inputs {
					if got := Normalize(in); got != want[i] {
						t.Errorf("normalisation corrompue en concurrence :\n  attendu %q\n  obtenu  %q", want[i], got)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// La même propriété au niveau du Store : construire pendant qu'on cherche.
func TestStore_ConstructionPendantRecherche(t *testing.T) {
	s := testStore(t)
	params := DefaultRankParams()

	ref, _ := s.Search("doliprane", params)
	if len(ref) == 0 {
		t.Fatal("référence vide")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, _ := s.Search("doliprane", params)
				if len(got) != len(ref) || got[0].Spec != ref[0].Spec {
					t.Error("résultat de recherche incohérent pendant une construction concurrente")
					return
				}
			}
		}()
	}
	// Constructions concurrentes, comme pendant une synchronisation.
	for i := 0; i < 10; i++ {
		testStore(t)
	}
	close(stop)
	wg.Wait()
}

// Le débordement de la table d'internement ne doit jamais être silencieux :
// la 65 536ᵉ valeur repartirait à zéro et tous les libellés du champ
// pointeraient vers la mauvaise chaîne.
func TestInterner_DebordementSignale(t *testing.T) {
	in := NewInterner("forme")
	for i := 0; i <= 65535; i++ {
		in.Intern("valeur-" + strconv.Itoa(i))
	}
	if err := in.Err(); err == nil {
		t.Fatal("un débordement doit être signalé, pas absorbé")
	}
	if !strings.Contains(in.Err().Error(), "forme") {
		t.Errorf("le message doit nommer le champ : %v", in.Err())
	}

	// En deçà, aucun signalement.
	sain := NewInterner("voie")
	for i := 0; i < 1000; i++ {
		sain.Intern("v" + strconv.Itoa(i))
	}
	if err := sain.Err(); err != nil {
		t.Errorf("1 000 valeurs ne doivent pas déborder : %v", err)
	}
}
