//go:build live

// Vérification de bout en bout du lot 1 sur la vraie source de l'ANSM :
// téléchargement → décodage → parsing → validation → dérivation → écriture
// atomique → relecture.
//
//	go test -tags live -v ./internal/snapshot/
package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

func TestLive_PipelineComplet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := bdpm.NewClient("test").FetchAll(ctx, bdpm.DefaultFiles())
	if err != nil {
		t.Fatalf("FetchAll : %v", err)
	}

	q := bdpm.NewQuarantine()
	start := time.Now()
	ds, err := bdpm.ParseAll(res, q)
	if err != nil {
		t.Fatalf("ParseAll : %v", err)
	}
	parseMS := time.Since(start).Milliseconds()

	bdpm.CheckReferentialIntegrity(ds, q)
	ds.Derive()

	if err := bdpm.CheckPlausibility(ds, q, bdpm.DefaultValidationOptions()); err != nil {
		t.Fatalf("validation de vraisemblance sur données réelles : %v", err)
	}

	counts := ds.Counts()
	t.Logf("parsing+validation en %d ms — compteurs : %+v", parseMS, counts)
	t.Logf("quarantaine : %d lignes, motifs %+v, taux %.6f",
		q.Total(), q.ByReason(), bdpm.RejectRatio(ds, q))
	for _, w := range bdpm.Warnings(q) {
		t.Logf("avertissement : %s", w)
	}

	// Volumétrie de docs/01-analyse-source-bdpm.md §2, avec une tolérance :
	// la BDPM bouge tous les jours.
	checkRange(t, "specialites", counts["specialites"], 15000, 17000)
	checkRange(t, "presentations", counts["presentations"], 19000, 23000)
	checkRange(t, "substances", counts["substances"], 3500, 4300)
	checkRange(t, "groupes_generiques", counts["groupes_generiques"], 1500, 1900)

	// Critère T-10 : chaque avis dont le dossier HAS a un lien le porte.
	linked := 0
	for _, a := range ds.AvisSMR {
		if a.LienAvisCT != nil {
			linked++
		}
	}
	if linked == 0 {
		t.Errorf("aucun avis SMR ne porte de lien HAS : la jointure T-10 est cassée")
	}
	t.Logf("avis SMR portant un lien CT : %d / %d", linked, len(ds.AvisSMR))

	// Écriture, puis relecture intégrale.
	dir := t.TempDir()
	out, err := Write(ds, q, WriteOptions{
		DataDir: dir, Generator: "test/live", Hash: res.Hash, Keep: 3,
	})
	if err != nil {
		t.Fatalf("Write : %v", err)
	}

	cur, err := Current(dir)
	if err != nil {
		t.Fatalf("Current : %v", err)
	}
	if cur != out.Dir {
		t.Errorf("current pointe vers %s, veut %s", cur, out.Dir)
	}

	reloaded, m, err := Load(cur)
	if err != nil {
		t.Fatalf("Load : %v", err)
	}
	for entity, n := range counts {
		if got := reloaded.Counts()[entity]; got != n {
			t.Errorf("%s : %d écrits, %d relus", entity, n, got)
		}
	}
	if m.Hash != res.Hash {
		t.Errorf("hash du manifest = %s, veut %s", m.Hash, res.Hash)
	}

	var total int64
	entries, _ := os.ReadDir(cur)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil {
			total += info.Size()
		}
	}
	t.Logf("snapshot écrit dans %s : %d fichiers, %.1f Mio",
		filepath.Base(cur), len(entries), float64(total)/(1<<20))
}

func checkRange(t *testing.T, name string, got, min, max int) {
	t.Helper()
	if got < min || got > max {
		t.Errorf("%s = %d, hors de la plage attendue [%d, %d]", name, got, min, max)
	}
}
