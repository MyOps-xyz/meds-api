package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

const testKey = "mk_test_kR3nP8xW2vQ7yT5mB9cF4hJ6dL1sA0zE"
const testAdminKey = "mk_admin_9wX4tR7bN2kM5vC8pQ1jH6yF3dZ0sLuG"

func intPtr(n int) *int { return &n }

func testDataset() *bdpm.Dataset {
	prix := int32(178)
	public := int32(280)
	honoraires := int32(102)
	agrement := true
	d := &bdpm.Dataset{
		Specialites: []bdpm.Specialite{
			{CIS: "60234100", Denomination: "DOLIPRANE 1000 mg, comprimé",
				FormePharmaceutique: "comprimé", VoiesAdministration: []string{"orale"},
				StatutAMM: "Autorisation active", ProcedureAMM: "Procédure nationale",
				EtatCommercialisation: "Commercialisée", DateAMM: "1995-06-12",
				Titulaires: []string{"OPELLA HEALTHCARE FRANCE"}},
			{CIS: "60002284", Denomination: "DOLIPRANE CODEINE, comprimé",
				FormePharmaceutique: "comprimé", StatutAMM: "Autorisation active",
				EtatCommercialisation: "Non commercialisée", DateAMM: "2001-01-01",
				Titulaires: []string{"OPELLA HEALTHCARE FRANCE"}, SurveillanceRenforcee: true},
			{CIS: "60002285", Denomination: "AMLOR 5 mg, gélule",
				FormePharmaceutique: "gélule", StatutAMM: "Autorisation active",
				EtatCommercialisation: "Commercialisée", DateAMM: "1990-06-01",
				Titulaires: []string{"VIATRIS SANTE"}},
		},
		Presentations: []bdpm.Presentation{
			{CIP13: "3400936030497", CIP7: "3603049", CIS: "60234100",
				Libelle:               "plaquette(s) thermoformée(s) de 8 comprimé(s)",
				StatutAdministratif:   "Présentation active",
				EtatCommercialisation: "Déclaration de commercialisation",
				DateDeclaration:       "2011-03-16", AgrementCollectivites: &agrement,
				TauxRemboursement: []int{65}, PrixMedicamentCents: &prix,
				PrixPublicCents: &public, HonorairesDispensationCents: &honoraires},
			{CIP13: "3400930000028", CIP7: "3000002", CIS: "60234100",
				Libelle: "plaquette de 16", StatutAdministratif: "Présentation active",
				EtatCommercialisation: "Déclaration de commercialisation"},
		},
		Composants: []bdpm.Composant{
			{CIS: "60234100", ElementPharmaceutique: "comprimé", CodeSubstance: "2202",
				DenominationSubstance: "PARACÉTAMOL", Dosage: "1000,00 mg",
				ReferenceDosage: "un comprimé", Nature: "SA", NumeroLiaison: 1},
			{CIS: "60002284", CodeSubstance: "2202", DenominationSubstance: "PARACÉTAMOL",
				Dosage: "400 mg", Nature: "SA", NumeroLiaison: 1},
			{CIS: "60002285", CodeSubstance: "12345",
				DenominationSubstance: "BÉSILATE D'AMLODIPINE", Dosage: "6,94 mg",
				Nature: "SA", NumeroLiaison: 1},
		},
		AvisSMR: []bdpm.AvisSMR{
			{CIS: "60234100", CodeDossierHAS: "CT-1234", DateAvis: "2015-06-10",
				MotifEvaluation: "Renouvellement", Valeur: "Important",
				Libelle: "Le SMR reste important."},
		},
		AvisASMR: []bdpm.AvisASMR{
			// Un avis chiffré et un avis textuel : le second doit porter
			// `niveau: null`, pas une clé absente.
			{CIS: "60234100", CodeDossierHAS: "CT-1234", DateAvis: "2015-06-10",
				MotifEvaluation: "Inscription", Valeur: "IV", Niveau: intPtr(4),
				Libelle: "Amélioration mineure."},
			{CIS: "60002284", CodeDossierHAS: "CT-9999", DateAvis: "2020-01-15",
				MotifEvaluation: "Inscription", Valeur: "Commentaires sans chiffrage de l'ASMR",
				Libelle: "Sans objet."},
		},
		Conditions: []bdpm.Condition{{CIS: "60002285", Condition: "liste I"}},
		Ruptures: []bdpm.Rupture{
			{CIS: "60234100", CodeStatut: 2, Statut: "tension", Libelle: "Tension d'approvisionnement",
				LibelleSource: "Tension", DateDebut: "2026-01-05", DateMiseAJour: "2026-02-01",
				LienANSM: "https://ansm.sante.fr/x"},
		},
		MITM: []bdpm.InfoMITM{{CIS: "60002285", CodeATC: "C08CA01", Denomination: "AMLODIPINE",
			LienBDPM: "https://base-donnees-publique.medicaments.gouv.fr/x"}},
		Appartenances: []bdpm.GroupeAppartenance{
			{GroupeID: "1", Libelle: "PARACETAMOL 1000 mg", CIS: "60234100", Type: 0,
				TypeLibelle: "princeps", Ordre: 1},
			{GroupeID: "1", Libelle: "PARACETAMOL 1000 mg", CIS: "60002284", Type: 1,
				TypeLibelle: "générique", Ordre: 2},
		},
	}
	d.Derive()
	return d
}

type fakeSyncer struct {
	busy bool
	id   string
}

func (f *fakeSyncer) Trigger() (string, error) {
	if f.busy {
		return "", ErrSyncInProgress
	}
	return f.id, nil
}

func testServer(t *testing.T, withStore bool) (http.Handler, *store.Holder) {
	t.Helper()

	holder := store.NewHolder()
	if withStore {
		s, err := store.Build(testDataset(), &snapshot.Manifest{
			Hash:        "3f2a9cdeadbeef",
			Version:     "2026-08-15T04:12:33Z",
			GeneratedAt: "2026-08-15T04:12:33Z",
			Counts:      map[string]int{"specialites": 3},
		})
		if err != nil {
			t.Fatalf("Build : %v", err)
		}
		s.BuildSearchIndex()
		holder.Store(s)
	}

	srv := NewServer(Options{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Holder:      holder,
		Keys:        NewKeyStore([]string{testKey}, testAdminKey),
		RateLimiter: NewRateLimiter(1000, 2000, nil),
		Rank:        store.DefaultRankParams(),
		Syncer:      &fakeSyncer{id: "01J8Z9K2M3N4P5Q6R7S8T9V0W1"},
	})
	return srv.Handler(), holder
}

func do(t *testing.T, h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// discardLogger et mustManifest servent aux tests et au fuzzing.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustManifest() *snapshot.Manifest {
	return &snapshot.Manifest{
		Hash:        "3f2a9cdeadbeef",
		Version:     "2026-08-15T04:12:33Z",
		GeneratedAt: "2026-08-15T04:12:33Z",
		Counts:      map[string]int{"specialites": 3},
	}
}

// doNoAuth émet une requête sans en-tête Authorization.
func doNoAuth(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("réponse illisible (%d) : %v — %s", rec.Code, err, rec.Body.String())
	}
	return out
}

// --- Authentification (T-34) ------------------------------------------------

func TestAuth_ClesAbsenteInvalideValide(t *testing.T) {
	h, _ := testServer(t, true)

	req := httptest.NewRequest("GET", "/v1/medicaments", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("sans clé : %d, veut 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("un 401 doit porter WWW-Authenticate")
	}

	rec = do(t, h, "GET", "/v1/medicaments", map[string]string{"Authorization": "Bearer mauvaise"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("clé invalide : %d, veut 401", rec.Code)
	}

	rec = do(t, h, "GET", "/v1/medicaments", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("clé valide : %d, veut 200 — %s", rec.Code, rec.Body.String())
	}
}

func TestAuth_EndpointsExemptes(t *testing.T) {
	h, _ := testServer(t, true)
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s sans clé : %d, veut 200", path, rec.Code)
		}
	}
}

func TestKeyStore_ComparaisonNeSortPasTot(t *testing.T) {
	ks := NewKeyStore([]string{"a", "b", "c"}, "")
	for _, k := range []string{"a", "b", "c"} {
		if !ks.Valid(k) {
			t.Errorf("clé %q refusée", k)
		}
	}
	if ks.Valid("d") {
		t.Error("clé inconnue acceptée")
	}
	if ks.Valid("") {
		t.Error("clé vide acceptée")
	}
}

// --- Format d'erreur (T-27) -------------------------------------------------

func TestProblem_FormatRFC9457(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/medicaments/99999999", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, veut 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != TypeBaseURL+ErrNotFound || p.Status != 404 {
		t.Errorf("problem = %+v", p)
	}
	if p.RequestID == "" {
		t.Error("request_id absent")
	}
	if p.RequestID != rec.Header().Get("X-Request-Id") {
		t.Error("le request_id du corps et celui de l'en-tête diffèrent")
	}
	if p.Instance != "/v1/medicaments/99999999" {
		t.Errorf("instance = %q", p.Instance)
	}
}

func TestProblem_TousLesTypes(t *testing.T) {
	h, holder := testServer(t, true)

	cases := []struct {
		name    string
		path    string
		headers map[string]string
		status  int
		errType string
	}{
		{"paramètre invalide", "/v1/medicaments?limit=99999", nil, 400, ErrInvalidParameter},
		{"CIS non numérique", "/v1/medicaments/abcdefgh", nil, 400, ErrInvalidParameter},
		{"curseur forgé", "/v1/medicaments?cursor=!!!", nil, 400, ErrInvalidParameter},
		{"non trouvé", "/v1/medicaments/99999999", nil, 404, ErrNotFound},
		{"route inconnue", "/v1/inexistant", nil, 404, ErrNotFound},
		{"non authentifié", "/v1/medicaments", map[string]string{"Authorization": "Bearer x"}, 401, ErrUnauthorized},
		{"format refusé", "/v1/medicaments", map[string]string{"Accept": "text/csv"}, 406, ErrNotAcceptable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, h, "GET", c.path, c.headers)
			if rec.Code != c.status {
				t.Fatalf("code = %d, veut %d — %s", rec.Code, c.status, rec.Body.String())
			}
			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.Type != TypeBaseURL+c.errType {
				t.Errorf("type = %q, veut %q", p.Type, TypeBaseURL+c.errType)
			}
		})
	}

	// 503 quand aucun jeu de données n'est publié.
	holder.Store(nil)
	rec := do(t, h, "GET", "/v1/medicaments", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("sans dataset : %d, veut 503", rec.Code)
	}
}

func TestProblem_MethodeNonAutorisee(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "DELETE", "/v1/medicaments", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, veut 405", rec.Code)
	}
}

// --- Handlers médicaments (T-30) --------------------------------------------

func TestListMedicaments_EtFiltres(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/v1/medicaments", nil)
	body := decodeEnvelope(t, rec)
	meta := body["meta"].(map[string]any)
	if meta["total"].(float64) != 3 {
		t.Errorf("total = %v, veut 3", meta["total"])
	}
	if meta["dataset_version"] != "2026-08-15T04:12:33Z" {
		t.Errorf("dataset_version = %v", meta["dataset_version"])
	}

	// Les filtres se combinent en ET.
	cases := map[string]int{
		"/v1/medicaments?commercialise=true":                       2,
		"/v1/medicaments?commercialise=false":                      1,
		"/v1/medicaments?surveillance=true":                        1,
		"/v1/medicaments?forme=gélule":                             1,
		"/v1/medicaments?voie=orale":                               1,
		"/v1/medicaments?titulaire=viatris":                        1,
		"/v1/medicaments?substance=PARACETAMOL":                    2,
		"/v1/medicaments?substance=2202":                           2,
		"/v1/medicaments?mitm=true":                                1,
		"/v1/medicaments?remboursable=true":                        1,
		"/v1/medicaments?rupture=true":                             1,
		"/v1/medicaments?generique=princeps":                       1,
		"/v1/medicaments?generique=generique":                      1,
		"/v1/medicaments?statut_amm=active":                        3,
		"/v1/medicaments?procedure_amm=nationale":                  1,
		"/v1/medicaments?q=doliprane&commercialise=true":           1,
		"/v1/medicaments?substance=PARACETAMOL&commercialise=true": 1,
		"/v1/medicaments?commercialise=true&surveillance=true":     0,
	}
	for path, want := range cases {
		rec := do(t, h, "GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s : code %d — %s", path, rec.Code, rec.Body.String())
			continue
		}
		body := decodeEnvelope(t, rec)
		got := int(body["meta"].(map[string]any)["total"].(float64))
		if got != want {
			t.Errorf("%s : total = %d, veut %d", path, got, want)
		}
	}
}

func TestListMedicaments_ScoreSeulementAvecQ(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/v1/medicaments?q=doliprane", nil)
	data := decodeEnvelope(t, rec)["data"].([]any)
	if len(data) == 0 {
		t.Fatal("aucun résultat")
	}
	if _, ok := data[0].(map[string]any)["score"]; !ok {
		t.Error("score absent alors que q est fourni")
	}

	rec = do(t, h, "GET", "/v1/medicaments", nil)
	data = decodeEnvelope(t, rec)["data"].([]any)
	if _, ok := data[0].(map[string]any)["score"]; ok {
		t.Error("score présent alors que q est absent")
	}
}

func TestGetMedicament_Include(t *testing.T) {
	h, _ := testServer(t, true)

	// Sans include : seule la spécialité.
	rec := do(t, h, "GET", "/v1/medicaments/60234100", nil)
	data := decodeEnvelope(t, rec)["data"].(map[string]any)
	if _, ok := data["presentations"]; ok {
		t.Error("presentations présentes sans include")
	}
	if data["denomination"] != "DOLIPRANE 1000 mg, comprimé" {
		t.Errorf("denomination = %v", data["denomination"])
	}

	rec = do(t, h, "GET", "/v1/medicaments/60234100?include=presentations,composition", nil)
	data = decodeEnvelope(t, rec)["data"].(map[string]any)
	if len(data["presentations"].([]any)) != 2 {
		t.Errorf("presentations = %v", data["presentations"])
	}
	if len(data["composition"].([]any)) != 1 {
		t.Errorf("composition = %v", data["composition"])
	}

	// include=all renvoie toutes les sections.
	rec = do(t, h, "GET", "/v1/medicaments/60234100?include=all", nil)
	data = decodeEnvelope(t, rec)["data"].(map[string]any)
	for _, section := range []string{"presentations", "composition", "generiques", "avis", "ruptures"} {
		if _, ok := data[section]; !ok {
			t.Errorf("include=all : section %q absente", section)
		}
	}

	// Section inconnue : 400 explicite.
	rec = do(t, h, "GET", "/v1/medicaments/60234100?include=inexistant", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("include inconnu : %d, veut 400", rec.Code)
	}
}

// Un CIS sans présentation renvoie 200 et un tableau vide, jamais 404.
func TestGetSousRessource_TableauVide(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/medicaments/60002285/presentations", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, veut 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("le corps doit contenir un tableau vide, pas null : %s", rec.Body.String())
	}
}

func TestGetPresentation_CIP7EtCIP13(t *testing.T) {
	h, _ := testServer(t, true)

	for _, cip := range []string{"3400936030497", "3603049"} {
		rec := do(t, h, "GET", "/v1/presentations/"+cip, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s : code %d — %s", cip, rec.Code, rec.Body.String())
		}
		data := decodeEnvelope(t, rec)["data"].(map[string]any)
		if data["cip13"] != "3400936030497" {
			t.Errorf("%s mène à cip13 = %v", cip, data["cip13"])
		}
		// Le médicament parent est inclus d'office.
		med, ok := data["medicament"].(map[string]any)
		if !ok || med["cis"] != "60234100" {
			t.Errorf("%s : médicament parent absent ou incorrect : %v", cip, data["medicament"])
		}
	}

	// Un code d'une longueur invalide donne 400, pas 404.
	rec := do(t, h, "GET", "/v1/presentations/12345", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("CIP de 5 chiffres : %d, veut 400", rec.Code)
	}
}

func TestPrix_AbsentEstNull(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/presentations/3400930000028", nil)
	data := decodeEnvelope(t, rec)["data"].(map[string]any)

	if v, ok := data["prix_medicament_cents"]; !ok || v != nil {
		t.Errorf("prix absent = %v, veut null", v)
	}
	if v, ok := data["agrement_collectivites"]; !ok || v != nil {
		t.Errorf("agrément absent = %v, veut null", v)
	}

	rec = do(t, h, "GET", "/v1/presentations/3400936030497", nil)
	data = decodeEnvelope(t, rec)["data"].(map[string]any)
	if data["prix_medicament_cents"].(float64) != 178 {
		t.Errorf("prix = %v, veut 178", data["prix_medicament_cents"])
	}
}

// --- Référentiels (T-31) ----------------------------------------------------

func TestReferentiels(t *testing.T) {
	h, _ := testServer(t, true)

	cases := []struct {
		path   string
		status int
	}{
		{"/v1/substances", 200},
		{"/v1/substances?q=paracetamol", 200},
		{"/v1/substances/2202", 200},
		{"/v1/substances/2202/medicaments", 200},
		{"/v1/substances/99999", 404},
		{"/v1/groupes-generiques", 200},
		{"/v1/groupes-generiques/1", 200},
		{"/v1/groupes-generiques/999", 404},
		{"/v1/ruptures", 200},
		{"/v1/ruptures?statut=tension", 200},
		{"/v1/ruptures?statut=inexistant", 400},
		{"/v1/ruptures?cis=60234100", 200},
		{"/v1/ruptures?depuis=2026-01-01", 200},
		{"/v1/ruptures?depuis=pas-une-date", 400},
		{"/v1/mitm", 200},
		{"/v1/suggest?q=dolip", 200},
		{"/v1/suggest", 400},
		{"/v1/dataset", 200},
	}
	for _, c := range cases {
		rec := do(t, h, "GET", c.path, nil)
		if rec.Code != c.status {
			t.Errorf("%s : %d, veut %d — %s", c.path, rec.Code, c.status, rec.Body.String())
		}
	}
}

func TestDataset_LicenceEtAvertissement(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/dataset", nil)
	data := decodeEnvelope(t, rec)["data"].(map[string]any)

	src := data["source"].(map[string]any)
	if !strings.Contains(src["licence"].(string), "Licence Ouverte") {
		t.Errorf("licence = %v", src["licence"])
	}
	if !strings.Contains(src["publisher"].(string), "ANSM") {
		t.Errorf("publisher = %v", src["publisher"])
	}
	if !strings.Contains(data["disclaimer"].(string), "ne se substituent") {
		t.Errorf("avertissement absent ou incomplet")
	}
	if _, ok := data["out_of_scope"]; !ok {
		t.Error("out_of_scope absent de /v1/dataset")
	}
}

func TestSuggest_HighlightEchappe(t *testing.T) {
	holder := store.NewHolder()
	d := testDataset()
	d.Specialites = append(d.Specialites, bdpm.Specialite{
		CIS: "60000001", Denomination: "<script>& compagnie",
		StatutAMM: "Autorisation active", EtatCommercialisation: "Commercialisée",
	})
	s, err := store.Build(d, &snapshot.Manifest{Hash: "h", Version: "v"})
	if err != nil {
		t.Fatal(err)
	}
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Holder: holder,
		Keys: NewKeyStore([]string{testKey}, ""), Rank: store.DefaultRankParams(),
	})
	rec := do(t, srv.Handler(), "GET", "/v1/suggest?q=%3Cscript%3E", nil)
	data := decodeEnvelope(t, rec)["data"].([]any)
	if len(data) == 0 {
		t.Fatal("aucune suggestion")
	}
	item := data[0].(map[string]any)

	// `label` est de la donnée brute : le JSON n'est pas du HTML, et c'est à
	// l'appelant d'échapper au rendu. `highlight`, lui, porte des balises et
	// est donc destiné à être inséré tel quel : c'est le seul champ qui doit
	// être échappé, et il doit l'être intégralement.
	hl := item["highlight"].(string)
	if strings.Contains(hl, "<script>") {
		t.Errorf("le highlight n'est pas échappé : %q", hl)
	}
	if !strings.Contains(hl, "&lt;script&gt;") {
		t.Errorf("le highlight devrait contenir la forme échappée : %q", hl)
	}
	if !strings.Contains(hl, "&amp;") {
		t.Errorf("l'esperluette n'est pas échappée dans le highlight : %q", hl)
	}
	if item["label"] != "<script>& compagnie" {
		t.Errorf("label = %q : la donnée brute ne doit pas être altérée", item["label"])
	}
}

// --- Pagination (T-28) ------------------------------------------------------

func TestPagination_ParcoursSansDoublonNiOmission(t *testing.T) {
	h, _ := testServer(t, true)

	seen := map[string]bool{}
	path := "/v1/medicaments?limit=1"
	for pages := 0; pages < 10; pages++ {
		rec := do(t, h, "GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d — %s", rec.Code, rec.Body.String())
		}
		body := decodeEnvelope(t, rec)
		for _, item := range body["data"].([]any) {
			cis := item.(map[string]any)["cis"].(string)
			if seen[cis] {
				t.Fatalf("doublon dans la pagination : %s", cis)
			}
			seen[cis] = true
		}
		meta := body["meta"].(map[string]any)
		if !meta["has_more"].(bool) {
			break
		}
		path = "/v1/medicaments?limit=1&cursor=" + meta["next_cursor"].(string)
	}
	if len(seen) != 3 {
		t.Errorf("%d éléments parcourus, veut 3", len(seen))
	}
}

func TestPagination_CurseurPerime(t *testing.T) {
	h, holder := testServer(t, true)

	rec := do(t, h, "GET", "/v1/medicaments?limit=1", nil)
	cursor := decodeEnvelope(t, rec)["meta"].(map[string]any)["next_cursor"].(string)

	// Rechargement du dataset sous un autre hash.
	s, err := store.Build(testDataset(), &snapshot.Manifest{Hash: "autre-hash", Version: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	s.BuildSearchIndex()
	holder.Store(s)

	rec = do(t, h, "GET", "/v1/medicaments?limit=1&cursor="+cursor, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, veut 400", rec.Code)
	}
	var p Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Type != TypeBaseURL+ErrCursorStale {
		t.Errorf("type = %q, veut cursor_stale", p.Type)
	}
}

func TestPagination_CurseurForge(t *testing.T) {
	h, _ := testServer(t, true)
	for _, bad := range []string{"!!!", "eyJvIjotMSwiaCI6IjNmMmE5YyJ9", strings.Repeat("A", 5000)} {
		rec := do(t, h, "GET", "/v1/medicaments?cursor="+bad, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("curseur %q : %d, veut 400 sans panique", truncate(bad, 20), rec.Code)
		}
	}
}

// --- ETag (T-29) ------------------------------------------------------------

func TestETag_IdentiquePourParametresEquivalents(t *testing.T) {
	h, _ := testServer(t, true)

	a := do(t, h, "GET", "/v1/medicaments?q=doliprane&limit=20", nil).Header().Get("ETag")
	b := do(t, h, "GET", "/v1/medicaments?q=doliprane", nil).Header().Get("ETag")
	if a == "" || a != b {
		t.Errorf("ETag différent pour des requêtes équivalentes : %q vs %q", a, b)
	}

	c := do(t, h, "GET", "/v1/medicaments?q=amlor", nil).Header().Get("ETag")
	if c == a {
		t.Error("ETag identique pour des requêtes différentes")
	}
}

func TestETag_304EtIndependanceDeAcceptEncoding(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/v1/medicaments", nil)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag absent")
	}

	rec = do(t, h, "GET", "/v1/medicaments", map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Errorf("code = %d, veut 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("un 304 ne doit pas avoir de corps : %d octets", rec.Body.Len())
	}

	// L'ETag ne dépend pas de Accept-Encoding.
	gz := do(t, h, "GET", "/v1/medicaments", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("ETag")
	if gz != etag {
		t.Errorf("ETag dépend de Accept-Encoding : %q vs %q", gz, etag)
	}
}

func TestETag_ChangeApresRechargement(t *testing.T) {
	h, holder := testServer(t, true)
	before := do(t, h, "GET", "/v1/medicaments", nil).Header().Get("ETag")

	s, err := store.Build(testDataset(), &snapshot.Manifest{Hash: "nouveau-hash", Version: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	s.BuildSearchIndex()
	holder.Store(s)

	after := do(t, h, "GET", "/v1/medicaments", nil).Header().Get("ETag")
	if before == after {
		t.Error("l'ETag n'a pas changé après rechargement du dataset")
	}
}

// --- En-têtes et durcissement (T-36, T-37) ----------------------------------

func TestSecurityHeaders(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/medicaments", nil)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, veut %q", k, got, v)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy absente")
	}
	// Refus d'indexation et d'entraînement (ADR 0008). L'assertion porte sur
	// `noindex` et `noai` uniquement : la liste complète est arbitraire et
	// destinée à évoluer, alors que ces deux jetons sont le fond de la
	// politique. Les frontaux posent le même en-tête ; l'application ne peut
	// pas s'en remettre à eux, elle est aussi jointe sur le réseau interne.
	if xrt := rec.Header().Get("X-Robots-Tag"); !strings.Contains(xrt, "noindex") ||
		!strings.Contains(xrt, "noai") {
		t.Errorf("X-Robots-Tag = %q, veut au moins noindex et noai", xrt)
	}
	if rec.Header().Get("Server") != "" {
		t.Error("l'en-tête Server ne doit pas être émis")
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id absent")
	}
}

func TestCORS_DesactiveParDefaut(t *testing.T) {
	h, _ := testServer(t, true)
	rec := do(t, h, "GET", "/v1/medicaments", map[string]string{"Origin": "https://evil.example"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS doit être désactivé par défaut")
	}
}

func TestCORS_ListeBlanche(t *testing.T) {
	holder := store.NewHolder()
	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Holder: holder,
		Keys: NewKeyStore([]string{testKey}, ""), Rank: store.DefaultRankParams(),
		CORSOrigins: []string{"https://app.example.fr"},
	})
	h := srv.Handler()

	rec := do(t, h, "GET", "/v1/medicaments", map[string]string{"Origin": "https://app.example.fr"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.fr" {
		t.Errorf("origine autorisée : Allow-Origin = %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("Allow-Credentials ne doit jamais être émis")
	}

	rec = do(t, h, "GET", "/v1/medicaments", map[string]string{"Origin": "https://evil.example"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("origine non listée : aucun en-tête CORS ne doit être émis")
	}
}

// --- Validation des entrées (T-32) ------------------------------------------

func TestValidation_EntreesHostiles(t *testing.T) {
	h, _ := testServer(t, true)

	cases := []struct {
		path   string
		status int
	}{
		{"/v1/medicaments?limit=99999", 400},
		{"/v1/medicaments?limit=0", 400},
		{"/v1/medicaments?limit=-1", 400},
		{"/v1/medicaments?limit=abc", 400},
		{"/v1/medicaments?q=" + strings.Repeat("a", 10000), 400},
		{"/v1/medicaments?statut_amm=inexistant", 400},
		{"/v1/medicaments?commercialise=peut-etre", 400},
		{"/v1/medicaments/1234567", 400},
		{"/v1/medicaments/123456789", 400},
		{"/v1/medicaments/%3Cscript%3E", 400},
		// Un paramètre inconnu est ignoré, pas rejeté.
		{"/v1/medicaments?utm_source=newsletter", 200},
		{"/v1/medicaments?inconnu=valeur", 200},
	}
	for _, c := range cases {
		rec := do(t, h, "GET", c.path, nil)
		if rec.Code != c.status {
			t.Errorf("%s : %d, veut %d — %s", c.path, rec.Code, c.status, rec.Body.String())
		}
	}
}

// --- Limitation de débit (T-35) ---------------------------------------------

func TestRateLimit_Depassement(t *testing.T) {
	holder := store.NewHolder()
	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Holder: holder,
		Keys: NewKeyStore([]string{testKey}, ""), Rank: store.DefaultRankParams(),
		RateLimiter: NewRateLimiter(1, 2, nil),
	})
	h := srv.Handler()

	var limited *httptest.ResponseRecorder
	for i := 0; i < 10; i++ {
		rec := do(t, h, "GET", "/v1/medicaments", nil)
		if rec.Code == http.StatusTooManyRequests {
			limited = rec
			break
		}
	}
	if limited == nil {
		t.Fatal("la limitation ne s'est jamais déclenchée")
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Error("Retry-After absent")
	}
	if limited.Header().Get("RateLimit-Limit") == "" {
		t.Error("RateLimit-Limit absent")
	}
}

func TestRateLimit_XForwardedForIgnoreSiProxyNonDeclare(t *testing.T) {
	rl := NewRateLimiter(10, 10, nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := rl.clientIP(req); got != "203.0.113.5" {
		t.Errorf("clientIP = %q : X-Forwarded-For ne doit pas être honoré sans proxy déclaré", got)
	}

	trusted := NewRateLimiter(10, 10, []string{"203.0.113.0/24"})
	if got := trusted.clientIP(req); got != "1.2.3.4" {
		t.Errorf("clientIP = %q : X-Forwarded-For doit être honoré depuis un proxy déclaré", got)
	}

	// Depuis une adresse non couverte par le CIDR de confiance, l'en-tête
	// redevient ignoré.
	req.RemoteAddr = "198.51.100.9:1234"
	if got := trusted.clientIP(req); got != "198.51.100.9" {
		t.Errorf("clientIP = %q : une adresse hors CIDR ne doit pas pouvoir usurper", got)
	}
}

// La table de seaux ne croît pas indéfiniment sous attaque distribuée.
func TestRateLimit_TableBornee(t *testing.T) {
	rl := NewRateLimiter(100, 100, nil)
	for i := 0; i < 200000; i++ {
		rl.Allow("i:" + strings.Repeat("x", i%7) + string(rune(i%256)) + string(rune(i/256%256)))
	}
	if got := rl.Size(); got > 200000 {
		t.Errorf("%d seaux vivants : la table doit être bornée", got)
	}
	t.Logf("%d seaux vivants après 200 000 clés distinctes", rl.Size())
}

// --- Journalisation (T-38) --------------------------------------------------

func TestRequestID_ClientReprisSiValide(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/v1/medicaments", map[string]string{"X-Request-Id": "01ABCDEF-valide_123"})
	if got := rec.Header().Get("X-Request-Id"); got != "01ABCDEF-valide_123" {
		t.Errorf("X-Request-Id = %q, veut celui du client", got)
	}

	// Un identifiant hostile est régénéré : sans quoi il finirait dans les
	// logs et pourrait y injecter une fausse entrée.
	for _, bad := range []string{"avec espace", "saut\nligne", strings.Repeat("a", 200), ""} {
		rec := do(t, h, "GET", "/v1/medicaments", map[string]string{"X-Request-Id": bad})
		got := rec.Header().Get("X-Request-Id")
		if got == bad && bad != "" {
			t.Errorf("X-Request-Id hostile %q repris tel quel", truncate(bad, 20))
		}
		if got == "" {
			t.Error("X-Request-Id absent")
		}
	}
}

func TestPanic_500GeneriqueSansTrace(t *testing.T) {
	holder := store.NewHolder()
	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	// Un handler qui panique, monté derrière la même chaîne de middlewares.
	panicking := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("secret interne : /etc/passwd")
	}), RequestID, Logger(slog.New(slog.NewTextHandler(io.Discard, nil)), nil), SecurityHeaders)

	rec := httptest.NewRecorder()
	panicking.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, veut 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "secret interne") || strings.Contains(body, "/etc/passwd") {
		t.Errorf("le corps du 500 fuit le détail de la panique : %s", body)
	}
	if strings.Contains(body, "goroutine") {
		t.Errorf("le corps du 500 contient une trace d'exécution")
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("le 500 doit rester un problem+json : %v", err)
	}
	if p.RequestID == "" {
		t.Error("request_id absent du 500")
	}
}

// --- Compression (T-26) -----------------------------------------------------

func TestGzip_SeuilEtNegociation(t *testing.T) {
	h, _ := testServer(t, true)

	// Réponse volumineuse et client acceptant gzip.
	rec := do(t, h, "GET", "/v1/medicaments/60234100?include=all", map[string]string{
		"Accept-Encoding": "gzip",
	})
	if rec.Body.Len() > GzipThreshold && rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("réponse de %d octets non compressée alors que gzip est accepté", rec.Body.Len())
	}

	// Client n'acceptant pas gzip : jamais compressé.
	rec = do(t, h, "GET", "/v1/medicaments/60234100?include=all", nil)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("réponse compressée alors que le client ne l'accepte pas")
	}

	// Petite réponse : pas de compression même si acceptée.
	rec = do(t, h, "GET", "/healthz", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("une réponse sous le seuil ne doit pas être compressée")
	}
}

// --- Sondes et administration (T-41, T-44) ----------------------------------

func TestReadyz_503SansDatasetPuis200(t *testing.T) {
	h, holder := testServer(t, false)

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz sans dataset : %d, veut 503", rec.Code)
	}

	// /healthz répond 200 même sans dataset : le processus vit.
	req = httptest.NewRequest("GET", "/healthz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz sans dataset : %d, veut 200", rec.Code)
	}

	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	req = httptest.NewRequest("GET", "/readyz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("readyz avec dataset : %d, veut 200", rec.Code)
	}
}

func TestAdminSync(t *testing.T) {
	h, _ := testServer(t, true)

	req := httptest.NewRequest("POST", "/admin/sync", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, veut 202 — %s", rec.Code, rec.Body.String())
	}
	data := decodeEnvelope(t, rec)["data"].(map[string]any)
	if data["sync_id"] == "" {
		t.Error("sync_id absent")
	}

	// Une clé API ordinaire est refusée.
	req = httptest.NewRequest("POST", "/admin/sync", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("clé API ordinaire : %d, veut 401", rec.Code)
	}
}

func TestAdminSync_404SansCleAdmin(t *testing.T) {
	holder := store.NewHolder()
	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Holder: holder,
		Keys: NewKeyStore([]string{testKey}, ""), Rank: store.DefaultRankParams(),
		Syncer: &fakeSyncer{id: "x"},
	})

	req := httptest.NewRequest("POST", "/admin/sync", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, veut 404 quand aucune clé admin n'est configurée", rec.Code)
	}
}

func TestAdminSync_409SiDejaEnCours(t *testing.T) {
	holder := store.NewHolder()
	s, _ := store.Build(testDataset(), &snapshot.Manifest{Hash: "h", Version: "v"})
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Holder: holder,
		Keys: NewKeyStore([]string{testKey}, testAdminKey), Rank: store.DefaultRankParams(),
		Syncer: &fakeSyncer{busy: true},
	})

	req := httptest.NewRequest("POST", "/admin/sync", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("code = %d, veut 409", rec.Code)
	}
}

// --- Immuabilité du Store (T-53 par anticipation) ---------------------------

// Une campagne de requêtes ne doit modifier ni les compteurs ni les données
// du Store : les slices retournées par le CSR sont partagées, et un handler
// qui les trierait en place corromprait un Store lu sans verrou.
func TestStore_NonMutePendantLesRequetes(t *testing.T) {
	h, holder := testServer(t, true)
	s := holder.Load()

	before := snapshotOfStore(s)
	for _, path := range []string{
		"/v1/medicaments", "/v1/medicaments?q=doliprane&sort=denomination",
		"/v1/medicaments/60234100?include=all", "/v1/substances", "/v1/ruptures",
		"/v1/groupes-generiques/1", "/v1/mitm", "/v1/suggest?q=dol",
	} {
		if rec := do(t, h, "GET", path, nil); rec.Code != http.StatusOK {
			t.Fatalf("%s : %d", path, rec.Code)
		}
	}
	if after := snapshotOfStore(s); after != before {
		t.Errorf("le Store a été muté par une campagne de requêtes :\navant %s\naprès %s", before, after)
	}
}

func snapshotOfStore(s *store.Store) string {
	var b strings.Builder
	for i := 0; i < s.NbSpecialites(); i++ {
		id := store.SpecID(i)
		sp := s.Spec(id)
		b.WriteString(sp.Denom)
		b.WriteByte('|')
		for _, p := range s.Presentations(id) {
			b.WriteString(p.Libelle)
			b.WriteByte(',')
		}
		for _, c := range s.Composants(id) {
			b.WriteString(c.Denom)
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	return b.String()
}
