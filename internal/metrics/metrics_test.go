package metrics

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/api"
	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape : code %d", rec.Code)
	}
	return rec.Body.String()
}

// countSeries compte les séries temporelles d'une métrique dans l'exposition.
func countSeries(body, metric string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, metric+"{") || line == metric || strings.HasPrefix(line, metric+" ") {
			n++
		}
	}
	return n
}

// Critère d'acceptation T-43 : l'étiquette `route` est le **patron** et non
// le chemin réel — 15 857 requêtes sur des CIS distincts ne créent qu'une
// seule série.
//
// Le test passe par le vrai serveur HTTP, et non par un appel direct à
// ObserveRequest : c'est précisément le câblage entre le routage et
// l'instrumentation qui peut se casser, pas la fonction elle-même.
func TestCardinalite_UnePatronUneSerie(t *testing.T) {
	m := New()
	h := serverWithMetrics(t, m)

	// Mille codes CIS distincts, tous inexistants ou non : peu importe, seul
	// le patron de route compte.
	for i := 0; i < 1000; i++ {
		cis := strconv.Itoa(60000000 + i)
		req := httptest.NewRequest("GET", "/v1/medicaments/"+cis, nil)
		req.Header.Set("Authorization", "Bearer "+testKey)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	body := scrape(t, m)
	if !strings.Contains(body, `route="GET /v1/medicaments/{cis}"`) {
		t.Fatalf("le patron de route n'apparaît pas dans l'exposition :\n%s",
			extract(body, "meds_http_requests_total"))
	}
	for i := 0; i < 1000; i++ {
		if strings.Contains(body, `route="/v1/medicaments/`+strconv.Itoa(60000000+i)+`"`) {
			t.Fatalf("un chemin réel est utilisé comme étiquette (CIS %d)", 60000000+i)
		}
	}

	// Une seule série de durée pour ce patron, quel que soit le nombre de
	// codes interrogés.
	if got := countSeries(body, "meds_http_request_duration_seconds_count"); got > 4 {
		t.Errorf("%d séries de durée pour 1 000 CIS distincts :\n%s",
			got, extract(body, "meds_http_request_duration_seconds_count"))
	}
}

// Une route inconnue n'ouvre pas une série par URL tentée : un balayage
// d'URL aléatoires est un scénario d'attaque banal.
func TestCardinalite_RouteInconnueRegroupee(t *testing.T) {
	m := New()
	h := serverWithMetrics(t, m)

	for i := 0; i < 200; i++ {
		req := httptest.NewRequest("GET", "/v1/inexistant-"+strconv.Itoa(i), nil)
		req.Header.Set("Authorization", "Bearer "+testKey)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	body := scrape(t, m)
	if !strings.Contains(body, `route="other"`) {
		t.Errorf("les routes inconnues doivent être regroupées sous « other » :\n%s",
			extract(body, "meds_http_requests_total"))
	}
	if got := countSeries(body, "meds_http_requests_total"); got > 5 {
		t.Errorf("%d séries après 200 URL inconnues :\n%s",
			got, extract(body, "meds_http_requests_total"))
	}
}

// Le statut est réduit à sa classe, sauf les codes sur lesquels on alerte.
func TestStatusLabel(t *testing.T) {
	cases := map[int]string{
		200: "2xx", 201: "2xx", 304: "3xx", 400: "4xx",
		401: "401", 404: "404", 429: "429", 500: "5xx", 503: "503",
	}
	for status, want := range cases {
		if got := statusLabel(status); got != want {
			t.Errorf("statusLabel(%d) = %q, veut %q", status, got, want)
		}
	}
}

// Les quatre familles documentées sont exposées.
func TestExposition_FamillesDocumentees(t *testing.T) {
	m := New()
	m.SetAgeSource(func() (time.Time, bool) { return time.Now().Add(-2 * time.Hour), true })

	m.ObserveRequest("GET /v1/medicaments", "GET", 200, 3*time.Millisecond, 4096)
	m.ObserveSearch("exact", 200*time.Microsecond, 27, false)
	m.ObserveSearch("trigram", 900*time.Microsecond, 3, true)
	m.SyncStarted()
	m.SyncFinished("success", 32*time.Second, time.Now())
	m.PublishDataset(DatasetSnapshot{
		Version: "2026-08-15T04:12:33Z", Hash: "3f2a9c",
		Counts:     map[string]int{"specialites": 15857, "presentations": 20899},
		Quarantine: map[string]int{"CIS_bdpm.txt|prix_invalide": 2},
		OutOfScope: map[string]int{"CIS_HAS_SMR_bdpm.txt|cis_orphelin": 4185},
		BuildTime:  50 * time.Millisecond,
	})

	body := scrape(t, m)
	for _, metric := range []string{
		// Trafic HTTP
		"meds_http_requests_total", "meds_http_request_duration_seconds",
		"meds_http_response_size_bytes", "meds_http_in_flight_requests",
		// Jeu de données
		"meds_dataset_age_seconds", "meds_dataset_info", "meds_dataset_records",
		"meds_dataset_quarantine_records", "meds_dataset_out_of_scope_records",
		"meds_store_build_duration_seconds", "meds_store_swaps_total",
		// Synchronisation
		"meds_sync_attempts_total", "meds_sync_duration_seconds",
		"meds_sync_last_success_timestamp", "meds_sync_in_progress",
		// Recherche et limitation
		"meds_search_duration_seconds", "meds_search_results_count",
		"meds_search_fallback_total", "meds_ratelimit_rejected_total",
		"meds_auth_failures_total",
		// Runtime Go
		"go_goroutines", "go_memstats_heap_inuse_bytes",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("métrique absente de l'exposition : %s", metric)
		}
	}

	// La fraîcheur est calculée à la collecte : ~7 200 s pour un jeu de
	// deux heures.
	if !strings.Contains(body, "meds_dataset_age_seconds 72") {
		t.Errorf("âge du jeu de données incorrect :\n%s", extract(body, "meds_dataset_age_seconds"))
	}
	if !strings.Contains(body, `meds_dataset_records{entity="specialites"} 15857`) {
		t.Errorf("compteur d'entité absent :\n%s", extract(body, "meds_dataset_records"))
	}
	if !strings.Contains(body, `meds_dataset_out_of_scope_records{file="CIS_HAS_SMR_bdpm.txt",reason="cis_orphelin"} 4185`) {
		t.Errorf("hors périmètre absent :\n%s", extract(body, "meds_dataset_out_of_scope_records"))
	}
	// unchanged doit être distinct de success.
	if !strings.Contains(body, `meds_sync_attempts_total{result="success"} 1`) {
		t.Errorf("issue de synchronisation absente :\n%s", extract(body, "meds_sync_attempts_total"))
	}
}

// Sans jeu de données publié, l'âge vaut -1 et non 0 : confondre les deux
// ferait passer un service sans donnée pour un service parfaitement à jour.
func TestAge_MoinsUnSansDataset(t *testing.T) {
	m := New()
	m.SetAgeSource(func() (time.Time, bool) { return time.Time{}, false })
	if !strings.Contains(scrape(t, m), "meds_dataset_age_seconds -1") {
		t.Errorf("âge = %s, veut -1", extract(scrape(t, m), "meds_dataset_age_seconds"))
	}
}

// Une entité disparue de la source ne doit pas conserver sa dernière valeur.
func TestPublishDataset_RemiseAZeroDesJauges(t *testing.T) {
	m := New()
	m.PublishDataset(DatasetSnapshot{
		Version: "v1", Hash: "h1",
		Counts: map[string]int{"specialites": 100, "obsolete": 42},
	})
	m.PublishDataset(DatasetSnapshot{
		Version: "v2", Hash: "h2",
		Counts: map[string]int{"specialites": 110},
	})

	body := scrape(t, m)
	if strings.Contains(body, `entity="obsolete"`) {
		t.Errorf("une entité disparue conserve sa jauge :\n%s", extract(body, "meds_dataset_records"))
	}
	if strings.Contains(body, `version="v1"`) {
		t.Errorf("l'ancienne version reste exposée :\n%s", extract(body, "meds_dataset_info"))
	}
	if !strings.Contains(body, `meds_dataset_records{entity="specialites"} 110`) {
		t.Errorf("la nouvelle valeur n'est pas exposée :\n%s", extract(body, "meds_dataset_records"))
	}
	if !strings.Contains(body, "meds_store_swaps_total 2") {
		t.Errorf("compteur de bascules :\n%s", extract(body, "meds_store_swaps_total"))
	}
}

func TestObserveRequest_CompteursDerives(t *testing.T) {
	m := New()
	m.ObserveRequest("GET /v1/medicaments", "GET", 401, time.Millisecond, 100)
	m.ObserveRequest("GET /v1/medicaments", "GET", 429, time.Millisecond, 100)

	body := scrape(t, m)
	if !strings.Contains(body, "meds_auth_failures_total 1") {
		t.Errorf("échec d'authentification non compté :\n%s", extract(body, "meds_auth_failures_total"))
	}
	if !strings.Contains(body, `meds_ratelimit_rejected_total{scope="key"} 1`) {
		t.Errorf("refus de quota non compté :\n%s", extract(body, "meds_ratelimit_rejected_total"))
	}
}

func TestInFlight(t *testing.T) {
	m := New()
	m.IncInFlight()
	m.IncInFlight()
	if !strings.Contains(scrape(t, m), "meds_http_in_flight_requests 2") {
		t.Error("jauge de concurrence incorrecte après deux incréments")
	}
	m.DecInFlight()
	m.DecInFlight()
	if !strings.Contains(scrape(t, m), "meds_http_in_flight_requests 0") {
		t.Error("la jauge n'est pas revenue à zéro")
	}
}

func extract(body, metric string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, metric) {
			out = append(out, "  "+line)
		}
	}
	if len(out) == 0 {
		return "  (aucune ligne)"
	}
	return strings.Join(out, "\n")
}

// --- Serveur de test --------------------------------------------------------

const testKey = "mk_test_kR3nP8xW2vQ7yT5mB9cF4hJ6dL1sA0zE"

func serverWithMetrics(t *testing.T, m *Metrics) http.Handler {
	t.Helper()

	d := &bdpm.Dataset{
		Specialites: []bdpm.Specialite{
			{CIS: "60234100", Denomination: "DOLIPRANE 1000 mg, comprimé",
				StatutAMM: "Autorisation active", EtatCommercialisation: "Commercialisée"},
		},
	}
	d.Derive()
	s, err := store.Build(d, &snapshot.Manifest{Hash: "h", Version: "v"})
	if err != nil {
		t.Fatal(err)
	}
	s.BuildSearchIndex()

	holder := store.NewHolder()
	holder.Store(s)

	srv := api.NewServer(api.Options{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Holder:         holder,
		Keys:           api.NewKeyStore([]string{testKey}, ""),
		Rank:           store.DefaultRankParams(),
		MetricsHandler: m.Handler(),
		Observer:       m.ObserveRequest,
		OnPanic:        m.ObservePanic,
		InFlight: func() func() {
			m.IncInFlight()
			return m.DecInFlight
		},
	})
	return srv.Handler()
}

var _ = io.Discard

// Un refus — 401 ou 429 — doit porter la route qu'il refuse.
//
// Le marquage de route était initialement posé après l'authentification et la
// limitation : mesuré sous charge, 220 111 refus de quota étaient tous
// étiquetés « other ». En production, cela rend impossible de savoir quel
// endpoint sature, précisément au moment où l'on en a besoin.
func TestCardinalite_RefusPortentLeurRoute(t *testing.T) {
	m := New()
	h := serverWithMetrics(t, m)

	// Sans clé : 401 sur une route connue.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/v1/medicaments/60234100", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	body := scrape(t, m)
	if !strings.Contains(body, `route="GET /v1/medicaments/{cis}",status="401"`) {
		t.Errorf("un 401 doit porter sa route :\n%s", extract(body, "meds_http_requests_total"))
	}
	if strings.Contains(body, `route="other",status="401"`) {
		t.Errorf("un 401 sur une route connue ne doit pas être classé « other » :\n%s",
			extract(body, "meds_http_requests_total"))
	}
}
