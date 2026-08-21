//go:build live

// Mesure des budgets de latence de docs/07-performance.md §2.1, sur le jeu de
// données réel de l'ANSM.
//
//	GOMAXPROCS=2 go test -tags live -run TestLive_BudgetsDeLatence -v ./internal/api/
//
// # Pourquoi httptest et non k6
//
// Le budget de docs/07 §2.1 porte sur le **temps de traitement**, réseau
// exclu — l'intitulé de la section le dit : « p99, hors réseau ». La mesure
// correcte est donc un appel direct au http.Handler, sans socket.
//
// Ce n'est pas un pis-aller : k6 ne peut structurellement pas mesurer une
// opération de 200 µs à travers la pile réseau d'une machine virtuelle. Sur
// le poste de test, il rapporte un p99 de ~207 ms **identique sur toutes les
// routes**, y compris les plus légères — une latence uniforme sur des
// opérations dont le coût varie d'un facteur vingt-cinq ne mesure pas le
// serveur. k6 reste l'outil du débit et de la résistance à la charge ; il
// n'est pas celui du budget de traitement.
//
// GOMAXPROCS=2 approche la machine de référence de docs/07 §2 (« 2 vCPU »).
// L'absence d'hôte de charge distant n'est pas un écart : le réseau est hors
// budget par définition.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

const liveKey = "mk_live_kR3nP8xW2vQ7yT5mB9cF4hJ6dL1sA0zE"

// liveHandler construit le serveur sur un snapshot réel.
//
// Un jeu réduit ne mesurerait rien : la recherche coûte ce qu'elle coûte
// parce qu'elle intersecte des postings sur 15 857 spécialités, et un index
// de trois entrées répondrait en nanosecondes sans rien prouver.
func liveHandler(t *testing.T) http.Handler {
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

	dir := t.TempDir()
	out, err := snapshot.Write(ds, q, snapshot.WriteOptions{
		DataDir: dir, Generator: "test/budgets", Hash: res.Hash, Keep: 1,
	})
	if err != nil {
		t.Fatalf("Write : %v", err)
	}
	s, err := store.LoadStore(out.Dir)
	if err != nil {
		t.Fatalf("LoadStore : %v", err)
	}

	holder := store.NewHolder()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Holder: holder,
		Keys:   NewKeyStore([]string{liveKey}, ""),
		Rank:   store.DefaultRankParams(),
		// Ni limiteur de débit, ni observateur : on mesure le coût de
		// traitement, pas celui de l'instrumentation.
	})
	return srv.Handler()
}

// operation décrit une opération budgétée.
type operation struct {
	nom     string
	cible   string
	budget  time.Duration
	entetes map[string]string
}

// percentiles calcule p50, p95 et p99 sur des durées triées.
func percentiles(d []time.Duration) (p50, p95, p99 time.Duration) {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	at := func(q float64) time.Duration {
		i := int(float64(len(d)-1) * q)
		return d[i]
	}
	return at(0.50), at(0.95), at(0.99)
}

// TestLive_BudgetsDeLatence tranche l'objectif O4.
func TestLive_BudgetsDeLatence(t *testing.T) {
	h := liveHandler(t)

	// Un CIS et un CIP13 réels, tirés de l'API elle-même : coder en dur un
	// code qui disparaîtrait de la BDPM ferait mesurer le chemin du 404.
	cis := premierCIS(t, h)
	cip := premierCIP(t, h, cis)

	// L'ETag de la recherche, pour mesurer le 304.
	etag := etagDe(t, h, "/v1/medicaments?q=doliprane")

	operations := []operation{
		{"GET /v1/medicaments/{cis}", "/v1/medicaments/" + cis, 200 * time.Microsecond, nil},
		{"GET /v1/presentations/{cip}", "/v1/presentations/" + cip, 200 * time.Microsecond, nil},
		{"GET /v1/medicaments/{cis}?include=all", "/v1/medicaments/" + cis + "?include=all", time.Millisecond, nil},
		{"GET /v1/medicaments?q=", "/v1/medicaments?q=doliprane", 3 * time.Millisecond, nil},
		{"recherche avec repli trigramme", "/v1/medicaments?q=paracetamoll", 3 * time.Millisecond, nil},
		{"GET /v1/suggest?q=", "/v1/suggest?q=dolip", time.Millisecond, nil},
		{"GET /v1/dataset", "/v1/dataset", 200 * time.Microsecond, nil},
		{"304 Not Modified", "/v1/medicaments?q=doliprane", 50 * time.Microsecond,
			map[string]string{"If-None-Match": etag}},
	}

	const (
		echauffement = 2000
		mesures      = 20000
	)

	t.Logf("GOMAXPROCS=%d — %d mesures par opération, réseau exclu",
		runtime.GOMAXPROCS(0), mesures)
	t.Logf("%-40s %10s %10s %10s %10s   %s",
		"opération", "p50", "p95", "p99", "budget", "verdict")

	var depassements []string

	for _, op := range operations {
		faire := func() int {
			req := httptest.NewRequest("GET", op.cible, nil)
			req.Header.Set("Authorization", "Bearer "+liveKey)
			for k, v := range op.entetes {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec.Code
		}

		// Échauffement : les premières requêtes paient l'initialisation des
		// pools de tampons et le remplissage des caches processeur. Les
		// inclure ferait mesurer un démarrage, pas un régime établi.
		attendu := 0
		for i := 0; i < echauffement; i++ {
			attendu = faire()
		}
		if attendu != http.StatusOK && attendu != http.StatusNotModified {
			t.Fatalf("%s : statut %d — la mesure porterait sur un chemin d'erreur", op.nom, attendu)
		}

		durees := make([]time.Duration, mesures)
		for i := 0; i < mesures; i++ {
			start := time.Now()
			faire()
			durees[i] = time.Since(start)
		}

		p50, p95, p99 := percentiles(durees)
		verdict := "tenu"
		if p99 > op.budget {
			verdict = "DÉPASSÉ"
			depassements = append(depassements,
				fmt.Sprintf("%s : p99 %v pour un budget de %v", op.nom, p99.Round(time.Microsecond), op.budget))
		}
		t.Logf("%-40s %10s %10s %10s %10s   %s",
			op.nom,
			p50.Round(100*time.Nanosecond), p95.Round(100*time.Nanosecond),
			p99.Round(100*time.Nanosecond), op.budget, verdict)
	}

	for _, d := range depassements {
		t.Errorf("budget dépassé — %s", d)
	}
}

func premierCIS(t *testing.T, h http.Handler) string {
	t.Helper()
	body := jsonDe(t, h, "/v1/medicaments?limit=1")
	data := body["data"].([]any)
	if len(data) == 0 {
		t.Fatal("aucun médicament servi")
	}
	return data[0].(map[string]any)["cis"].(string)
}

func premierCIP(t *testing.T, h http.Handler, cis string) string {
	t.Helper()
	// Un médicament sans présentation existe : on parcourt jusqu'à en
	// trouver un qui en a.
	body := jsonDe(t, h, "/v1/medicaments?limit=50")
	for _, item := range body["data"].([]any) {
		c := item.(map[string]any)["cis"].(string)
		pres := jsonDe(t, h, "/v1/medicaments/"+c+"/presentations")["data"].([]any)
		if len(pres) > 0 {
			return pres[0].(map[string]any)["cip13"].(string)
		}
	}
	t.Fatal("aucune présentation trouvée sur les 50 premiers médicaments")
	return ""
}

func etagDe(t *testing.T, h http.Handler, cible string) string {
	t.Helper()
	req := httptest.NewRequest("GET", cible, nil)
	req.Header.Set("Authorization", "Bearer "+liveKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("aucun ETag sur %s", cible)
	}
	return etag
}

func jsonDe(t *testing.T, h http.Handler, cible string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", cible, nil)
	req.Header.Set("Authorization", "Bearer "+liveKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s : HTTP %d — %s", cible, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s : réponse illisible — %v", cible, err)
	}
	return out
}
