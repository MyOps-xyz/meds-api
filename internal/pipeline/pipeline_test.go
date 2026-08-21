package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

// fakeANSM sert un jeu BDPM minimal mais structurellement fidèle : dix
// fichiers, tabulations, encodages hétérogènes, terminaisons variables.
//
// Un vrai serveur HTTP plutôt qu'un client simulé : c'est le seul moyen de
// couvrir la chaîne complète, du téléchargement à la publication du Store,
// y compris le calcul du hash global sur lequel repose toute la détection de
// changement.
type fakeANSM struct {
	mu       sync.Mutex
	files    map[string]string
	requests int
	status   int
}

func newFakeANSM() *fakeANSM {
	return &fakeANSM{files: defaultFiles(), status: http.StatusOK}
}

func (f *fakeANSM) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++

		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		name := filepath.Base(r.URL.Path)
		body, ok := f.files[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeANSM) set(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = content
}

// tsv assemble des lignes de champs séparés par des tabulations.
func tsv(rows ...[]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteString("\r\n")
	}
	return b.String()
}

// spec produit une ligne de CIS_bdpm.txt (12 colonnes).
func specRow(cis, denom string) []string {
	return []string{cis, denom, "comprimé", "orale", "Autorisation active",
		"Procédure nationale", "Commercialisée", "19/03/1997", "", "", "OPELLA", "Non"}
}

func defaultFiles() map[string]string {
	specs := make([][]string, 0, 12)
	for i := 0; i < 12; i++ {
		specs = append(specs, specRow(cisOf(i), "DOLIPRANE "+cisOf(i)+" mg, comprimé"))
	}

	return map[string]string{
		bdpm.FileSpecialites: tsv(specs...),
		bdpm.FilePresentations: tsv(
			[]string{"60000000", "3000001", "plaquette de 8", "Présentation active",
				"Déclaration de commercialisation", "16/03/2011", "3400930000011", "oui",
				"65%", "1,78", "2,80", "1,02", ""},
			[]string{"60000001", "3000002", "plaquette de 16", "Présentation active",
				"Déclaration de commercialisation", "16/03/2011", "3400930000028", "non",
				"", "", "", "", ""},
		),
		bdpm.FileComposants: tsv(
			[]string{"60000000", "comprimé", "42215", "PARACÉTAMOL", "1000,00 mg", "un comprimé", "SA", "1"},
			[]string{"60000001", "comprimé", "42215", "PARACÉTAMOL", "500,00 mg", "un comprimé", "SA", "1"},
		),
		bdpm.FileAvisSMR: tsv(
			[]string{"60000000", "CT-1234", "Renouvellement", "20150610", "Important", "Le SMR reste important."},
		),
		bdpm.FileAvisASMR: tsv(
			[]string{"60000000", "CT-1234", "Inscription", "20150610", "IV", "ASMR mineure."},
		),
		bdpm.FileLiensCT: tsv(
			[]string{"CT-1234", "http://has-sante.fr/portail/ct-1234"},
		),
		bdpm.FileGroupes: tsv(
			[]string{"1", "PARACETAMOL 1000 mg", "60000000", "0", "1"},
			[]string{"1", "PARACETAMOL 1000 mg", "60000001", "1", "2"},
		),
		bdpm.FileConditions: tsv(
			[]string{"60000000", "liste I"},
		),
		bdpm.FileRuptures: tsv(
			[]string{"60000000", "3400930000011", "2", "Tension", "05/01/2026", "01/02/2026", "",
				"https://ansm.sante.fr/x"},
		),
		// Sans saut de ligne final : c'est le piège P7 tel qu'il se présente
		// réellement dans CIS_MITM.txt.
		bdpm.FileMITM: strings.TrimSuffix(tsv(
			[]string{"60000000", "N02BE01", "PARACETAMOL", "http://base-donnees-publique.medicaments.gouv.fr/x"},
		), "\r\n"),
	}
}

func cisOf(i int) string {
	s := "6000000" + string(rune('0'+i%10))
	if i >= 10 {
		s = "600000" + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	return s
}

func testPipeline(t *testing.T, srv *httptest.Server, mutate func(*Options)) (*Pipeline, string, *store.Holder) {
	t.Helper()

	dir := t.TempDir()
	holder := store.NewHolder()
	validation := bdpm.DefaultValidationOptions()
	// Le jeu de test compte douze spécialités : le plancher réel de 10 000
	// n'a pas de sens ici, les autres garde-fous restent actifs.
	validation.MinSpecialites = 1

	opts := Options{
		DataDir:    dir,
		Generator:  "test/1.0.0",
		Keep:       3,
		Holder:     holder,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client:     bdpm.NewClient("test", bdpm.WithBaseURL(srv.URL), bdpm.WithMaxAttempts(1)),
		Validation: validation,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return New(opts), dir, holder
}

// Le chemin nominal complet : téléchargement, parsing, validation,
// dérivation, écriture, construction et publication.
func TestPipeline_CheminNominal(t *testing.T) {
	fake := newFakeANSM()
	p, dir, holder := testPipeline(t, fake.server(t), nil)

	rep, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run : %v", err)
	}

	if rep.Status != "success" || !rep.Changed {
		t.Errorf("rapport = %+v", rep)
	}
	if rep.Counts["specialites"] != 12 || rep.Counts["substances"] != 1 {
		t.Errorf("compteurs = %+v", rep.Counts)
	}
	if rep.Quarantine != 0 {
		t.Errorf("%d rejets sur un jeu sain", rep.Quarantine)
	}
	if rep.SyncID == "" || !strings.HasPrefix(rep.SyncID, "sync_") {
		t.Errorf("sync_id = %q", rep.SyncID)
	}

	// Le snapshot est publié et relisible.
	cur, err := snapshot.Current(dir)
	if err != nil {
		t.Fatalf("Current : %v", err)
	}
	m, err := snapshot.ReadManifest(cur)
	if err != nil {
		t.Fatalf("ReadManifest : %v", err)
	}
	if m.Generator != "test/1.0.0" || m.Hash != rep.Hash {
		t.Errorf("manifest = %+v", m)
	}

	// Le Store est publié et cohérent.
	s := holder.Load()
	if s == nil {
		t.Fatal("aucun Store publié")
	}
	if s.NbSpecialites() != 12 {
		t.Errorf("Store : %d spécialités", s.NbSpecialites())
	}
	if rep.StoreBuildMS < 0 {
		t.Errorf("durée de construction = %d ms", rep.StoreBuildMS)
	}

	// La jointure des liens HAS a bien eu lieu (T-10).
	id, ok := s.SpecByCIS("60000000")
	if !ok {
		t.Fatal("CIS 60000000 introuvable")
	}
	avis := s.AvisSMR(id)
	if len(avis) != 1 || avis[0].Lien == "" {
		t.Errorf("avis SMR = %+v : le lien CT devrait être résolu", avis)
	}
	// Et l'URL a été réécrite en HTTPS.
	if !strings.HasPrefix(avis[0].Lien, "https://") {
		t.Errorf("lien CT = %q, veut du HTTPS", avis[0].Lien)
	}
}

// Critère de vérification §7.1 : deux synchronisations consécutives sur une
// source inchangée — la seconde s'arrête à la détection de changement, sans
// écriture ni reconstruction.
func TestPipeline_SourceInchangee(t *testing.T) {
	fake := newFakeANSM()
	p, dir, holder := testPipeline(t, fake.server(t), nil)

	first, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstStore := holder.Load()

	second, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if second.Changed {
		t.Errorf("la seconde synchronisation se croit modifiée")
	}
	if second.Hash != first.Hash {
		t.Errorf("hash instable : %s puis %s", first.Hash, second.Hash)
	}
	if second.StoreBuildMS != 0 {
		t.Errorf("le Store a été reconstruit inutilement (%d ms)", second.StoreBuildMS)
	}
	if holder.Load() != firstStore {
		t.Errorf("le Store a été republié alors que la source n'a pas changé")
	}

	// Un seul répertoire de snapshot : rien n'a été réécrit.
	entries, _ := os.ReadDir(filepath.Join(dir, snapshot.SnapshotsDir))
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Errorf("%d répertoires de snapshot, veut 1", dirs)
	}
}

// --force réingère malgré une source inchangée.
func TestPipeline_Force(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), func(o *Options) { o.Force = true })

	if _, err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed {
		t.Error("--force doit forcer la réingestion")
	}
}

// Une source modifiée déclenche une nouvelle ingestion, et le rapport porte
// le delta — la donnée la plus utile en exploitation.
func TestPipeline_SourceModifiee_Delta(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)

	first, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	rows := make([][]string, 0, 14)
	for i := 0; i < 14; i++ {
		rows = append(rows, specRow(cisOf(i), "DOLIPRANE "+cisOf(i)+" mg, comprimé"))
	}
	fake.set(bdpm.FileSpecialites, tsv(rows...))

	second, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed {
		t.Fatal("la modification n'a pas été détectée")
	}
	if second.PreviousHash != first.Hash {
		t.Errorf("previous_hash = %q, veut %q", second.PreviousHash, first.Hash)
	}
	if got := second.Delta["specialites"]; got != 2 {
		t.Errorf("delta specialites = %d, veut +2", got)
	}
}

// Exigence E3 : une ingestion invalide ne touche pas le Store en service.
func TestPipeline_ValidationEchoue_StoreIntact(t *testing.T) {
	fake := newFakeANSM()
	p, _, holder := testPipeline(t, fake.server(t), nil)

	if _, err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	servedBefore := holder.Load()
	if servedBefore == nil {
		t.Fatal("aucun Store publié")
	}

	// La source s'effondre : deux spécialités au lieu de douze, soit −83 %.
	fake.set(bdpm.FileSpecialites, tsv(
		specRow("60000000", "DOLIPRANE 1000 mg, comprimé"),
		specRow("60000001", "DOLIPRANE 500 mg, comprimé"),
	))

	rep, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("une chute de 83 % doit faire échouer l'ingestion")
	}
	if !strings.Contains(err.Error(), "variation") {
		t.Errorf("err = %v, veut un dépassement de variation", err)
	}
	if rep.Status != "failure" || rep.Error == "" {
		t.Errorf("le rapport doit porter l'échec : %+v", rep)
	}

	// Le Store en service est **exactement le même objet** : ni remplacé,
	// ni reconstruit.
	if holder.Load() != servedBefore {
		t.Error("le Store en service a été touché par une ingestion en échec")
	}
	if holder.Load().NbSpecialites() != 12 {
		t.Errorf("le Store sert %d spécialités au lieu des 12 d'origine",
			holder.Load().NbSpecialites())
	}
}

// Une source en panne ne touche pas non plus le Store.
func TestPipeline_TelechargementEnEchec(t *testing.T) {
	fake := newFakeANSM()
	p, _, holder := testPipeline(t, fake.server(t), nil)

	if _, err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := holder.Load()

	fake.mu.Lock()
	fake.status = http.StatusInternalServerError
	fake.mu.Unlock()

	if _, err := p.Run(context.Background()); err == nil {
		t.Fatal("une source en 500 doit faire échouer la synchronisation")
	}
	if holder.Load() != before {
		t.Error("le Store a été touché malgré l'échec du téléchargement")
	}
}

// --dry-run exécute tout sauf l'écriture et la publication.
func TestPipeline_DryRun(t *testing.T) {
	fake := newFakeANSM()
	p, dir, holder := testPipeline(t, fake.server(t), func(o *Options) { o.DryRun = true })

	rep, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "success" {
		t.Errorf("statut = %q", rep.Status)
	}
	// Le rapport est complet : c'est tout l'intérêt du mode.
	if rep.Counts["specialites"] != 12 {
		t.Errorf("compteurs = %+v", rep.Counts)
	}
	// Mais rien n'a été écrit ni publié.
	if _, err := snapshot.Current(dir); err == nil {
		t.Error("--dry-run a publié un snapshot")
	}
	if holder.Load() != nil {
		t.Error("--dry-run a publié un Store")
	}
}

// Critère T-40 : deux déclenchements simultanés n'exécutent qu'une seule
// synchronisation.
func TestPipeline_UneSeuleSyncALaFois(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, busyCount int

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := p.Run(context.Background())
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, ErrInProgress) {
				busyCount++
			} else if err == nil {
				okCount++
			}
		}()
	}
	close(start)
	wg.Wait()

	if okCount+busyCount != 8 {
		t.Fatalf("%d succès + %d refus ≠ 8 : une erreur inattendue s'est produite", okCount, busyCount)
	}
	if busyCount == 0 {
		t.Error("aucun déclenchement concurrent n'a été refusé")
	}
}

func TestPipeline_TriggerAsynchrone(t *testing.T) {
	fake := newFakeANSM()
	p, dir, _ := testPipeline(t, fake.server(t), nil)

	id, err := p.Trigger()
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("sync_id vide")
	}

	deadline := time.Now().Add(10 * time.Second)
	for p.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.Running() {
		t.Fatal("la synchronisation ne s'est pas terminée")
	}

	rep, ok := p.LastReport()
	if !ok || rep.Status != "success" {
		t.Errorf("dernier rapport = %+v (ok=%v)", rep, ok)
	}
	if _, err := snapshot.Current(dir); err != nil {
		t.Errorf("aucun snapshot publié : %v", err)
	}
}

func TestPipeline_TriggerRefuseSiEnCours(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)

	if _, err := p.Trigger(); err != nil {
		t.Fatal(err)
	}
	// La synchronisation déclenchée s'exécute en arrière-plan et continue
	// d'écrire dans le DataDir après le retour du test. Sans cette attente,
	// le t.TempDir() est supprimé pendant l'écriture et le nettoyage échoue
	// par intermittence sur « directory not empty ». Enregistré après
	// testPipeline, donc exécuté AVANT la suppression du répertoire (les
	// nettoyages se déroulent en ordre inverse d'enregistrement).
	t.Cleanup(func() {
		for i := 0; i < 500 && p.Running(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if p.Running() {
			t.Error("la synchronisation déclenchée ne s'est pas terminée en 5 s")
		}
	})

	// Le second appel doit être refusé tant que le premier tourne. La
	// synchronisation dure quelques millisecondes : on tente jusqu'à
	// observer soit le refus, soit la fin.
	refused := false
	for i := 0; i < 100; i++ {
		if _, err := p.Trigger(); errors.Is(err, ErrInProgress) {
			refused = true
			break
		}
		if !p.Running() {
			break
		}
	}
	if !refused && p.Running() {
		t.Error("Trigger n'a pas refusé un déclenchement concurrent")
	}
}

// LoadCurrent republie le snapshot présent au démarrage, sans retélécharger.
func TestPipeline_LoadCurrent(t *testing.T) {
	fake := newFakeANSM()
	srv := fake.server(t)
	p, dir, _ := testPipeline(t, srv, nil)

	if _, err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	requestsAfterSync := fake.requests
	fake.mu.Unlock()

	// Un second pipeline sur le même répertoire, comme au redémarrage du
	// serveur.
	holder2 := store.NewHolder()
	p2 := New(Options{
		DataDir: dir, Generator: "test/1.0.0", Holder: holder2,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client: bdpm.NewClient("test", bdpm.WithBaseURL(srv.URL)),
	})
	if err := p2.LoadCurrent(); err != nil {
		t.Fatalf("LoadCurrent : %v", err)
	}
	if holder2.Load() == nil {
		t.Fatal("LoadCurrent n'a publié aucun Store")
	}
	if got := holder2.Load().NbSpecialites(); got != 12 {
		t.Errorf("%d spécialités relues", got)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.requests != requestsAfterSync {
		t.Errorf("LoadCurrent a retéléchargé %d fichiers", fake.requests-requestsAfterSync)
	}
}

// Un démarrage à froid n'est pas une erreur.
func TestPipeline_LoadCurrentSansSnapshot(t *testing.T) {
	fake := newFakeANSM()
	p, _, holder := testPipeline(t, fake.server(t), nil)

	if err := p.LoadCurrent(); err != nil {
		t.Fatalf("LoadCurrent sur un répertoire vierge : %v", err)
	}
	if holder.Load() != nil {
		t.Error("un Store a été publié alors qu'aucun snapshot n'existe")
	}
}

// LoadCurrent purge les résidus d'une écriture interrompue.
func TestPipeline_LoadCurrentPurgeLesResidus(t *testing.T) {
	fake := newFakeANSM()
	p, dir, _ := testPipeline(t, fake.server(t), nil)

	if _, err := p.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(dir, snapshot.SnapshotsDir, ".tmp-interrompu")
	if err := os.MkdirAll(residue, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := p.LoadCurrent(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(residue); !os.IsNotExist(err) {
		t.Error("le résidu d'écriture n'a pas été purgé au démarrage")
	}
}

// La purge conserve les Keep snapshots les plus récents.
func TestPipeline_PurgeDesAnciensSnapshots(t *testing.T) {
	fake := newFakeANSM()
	p, dir, _ := testPipeline(t, fake.server(t), func(o *Options) { o.Keep = 2 })

	for i := 12; i < 17; i++ {
		rows := make([][]string, 0, i)
		for j := 0; j < i; j++ {
			rows = append(rows, specRow(cisOf(j), "DOLIPRANE "+cisOf(j)+" mg, comprimé"))
		}
		fake.set(bdpm.FileSpecialites, tsv(rows...))
		if _, err := p.Run(context.Background()); err != nil {
			t.Fatalf("itération %d : %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, snapshot.SnapshotsDir))
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 2 {
		t.Errorf("%d snapshots conservés, veut 2", dirs)
	}
	if _, err := snapshot.Current(dir); err != nil {
		t.Errorf("le snapshot courant n'a pas survécu à la purge : %v", err)
	}
}

// Le contexte annulé interrompt proprement la synchronisation.
func TestPipeline_ContexteAnnule(t *testing.T) {
	fake := newFakeANSM()
	p, _, holder := testPipeline(t, fake.server(t), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Run(ctx); err == nil {
		t.Fatal("un contexte annulé doit faire échouer la synchronisation")
	}
	if holder.Load() != nil {
		t.Error("un Store a été publié malgré l'annulation")
	}
}

// Le hash global ne dépend pas de l'ordre de complétion des téléchargements :
// deux ingestions de la même source produisent le même hash.
func TestPipeline_HashDeterministe(t *testing.T) {
	fake := newFakeANSM()
	p1, _, _ := testPipeline(t, fake.server(t), func(o *Options) { o.Force = true })
	p2, _, _ := testPipeline(t, fake.server(t), func(o *Options) { o.Force = true })

	a, err := p1.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p2.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash {
		t.Errorf("hash instable entre deux ingestions : %s vs %s", a.Hash, b.Hash)
	}
}
