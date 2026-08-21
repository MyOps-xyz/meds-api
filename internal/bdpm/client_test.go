package bdpm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noJitter supprime tout aléa de repli, pour des tests rapides et
// déterministes (docs/09-pipeline-mise-a-jour.md §3.1 : « l'aléa du jitter
// doit être injectable »).
func noJitter(time.Duration) time.Duration { return 0 }

// testClientOptions renvoie des options de Client adaptées aux tests :
// délais courts, backoff quasi nul, aucun jitter.
func testClientOptions(serverURL string) []Option {
	return []Option{
		WithBaseURL(serverURL),
		WithUserAgent("meds-api/test (+https://github.com/MyOps-xyz/meds-api)"),
		WithBackoff(5*time.Millisecond, 4),
		WithJitterFunc(noJitter),
		WithPerFileTimeout(5 * time.Second),
		WithGlobalTimeout(20 * time.Second),
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestFetchAll_Success télécharge les 10 fichiers par défaut depuis un
// serveur simulé et vérifie le contenu, l'empreinte par fichier et le hash
// global du jeu de données.
func TestFetchAll_Success(t *testing.T) {
	t.Parallel()

	files := DefaultFiles()
	if len(files) != 10 {
		t.Fatalf("DefaultFiles() = %d fichiers, attendu 10", len(files))
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "contenu de %s", strings.TrimPrefix(r.URL.Path, "/file/"))
	}))
	defer server.Close()

	client := NewClient("1.0.0", testClientOptions(server.URL)...)

	start := time.Now()
	result, err := client.FetchAll(context.Background(), files)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchAll() erreur inattendue : %v", err)
	}
	if elapsed > 60*time.Second {
		t.Errorf("FetchAll() a pris %v, attendu < 60s", elapsed)
	}

	if len(result.Files) != 10 {
		t.Fatalf("len(result.Files) = %d, attendu 10", len(result.Files))
	}

	var hashes []string
	for i, fr := range result.Files {
		want := fmt.Sprintf("contenu de %s", files[i].Name)
		if string(fr.Bytes) != want {
			t.Errorf("fichier %s: contenu = %q, attendu %q", fr.Name, fr.Bytes, want)
		}
		if fr.Name != files[i].Name {
			t.Errorf("résultat %d: Name = %q, attendu %q (ordre non préservé)", i, fr.Name, files[i].Name)
		}
		if fr.SHA256 != sha256Hex([]byte(want)) {
			t.Errorf("fichier %s: SHA256 = %q, attendu %q", fr.Name, fr.SHA256, sha256Hex([]byte(want)))
		}
		if fr.Attempts != 1 {
			t.Errorf("fichier %s: Attempts = %d, attendu 1", fr.Name, fr.Attempts)
		}
		hashes = append(hashes, fr.SHA256)
	}

	// Le hash global doit être stable, indépendant de l'ordre de calcul,
	// et calculé sur les hashs triés par nom de fichier.
	sortedNames := make([]string, len(files))
	for i, f := range files {
		sortedNames[i] = f.Name
	}
	sort.Strings(sortedNames)

	nameToHash := make(map[string]string, len(result.Files))
	for _, fr := range result.Files {
		nameToHash[fr.Name] = fr.SHA256
	}
	var concat strings.Builder
	for _, name := range sortedNames {
		concat.WriteString(nameToHash[name])
	}
	wantGlobal := sha256Hex([]byte(concat.String()))
	if result.Hash != wantGlobal {
		t.Errorf("Hash = %q, attendu %q", result.Hash, wantGlobal)
	}
	_ = hashes
}

// TestFetchFile_PersistentServerError vérifie qu'une source renvoyant
// systématiquement 500 déclenche exactement 3 tentatives puis un abandon
// propre (pas de panique, une erreur claire).
func TestFetchFile_PersistentServerError(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient("1.0.0", testClientOptions(server.URL)...)

	_, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err == nil {
		t.Fatal("FetchAll() aurait dû échouer")
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("nombre de requêtes = %d, attendu 3 (tentatives épuisées)", got)
	}
}

// TestFetchFile_RetrySucceedsOnSecondAttempt vérifie qu'un 500 suivi d'un
// 200 réussit à la deuxième tentative.
func TestFetchFile_RetrySucceedsOnSecondAttempt(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "contenu final")
	}))
	defer server.Close()

	client := NewClient("1.0.0", testClientOptions(server.URL)...)

	result, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err != nil {
		t.Fatalf("FetchAll() erreur inattendue : %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("nombre de requêtes = %d, attendu 2", requests.Load())
	}
	if len(result.Files) != 1 || string(result.Files[0].Bytes) != "contenu final" {
		t.Fatalf("résultat inattendu : %+v", result.Files)
	}
	if result.Files[0].Attempts != 2 {
		t.Errorf("Attempts = %d, attendu 2", result.Files[0].Attempts)
	}
}

// TestFetchFile_NotFoundFailsImmediately vérifie qu'un 404 ne déclenche
// qu'une seule tentative : c'est une erreur définitive de l'ANSM (fichier
// renommé, URL erronée), pas un incident transitoire.
func TestFetchFile_NotFoundFailsImmediately(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("1.0.0", testClientOptions(server.URL)...)

	_, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err == nil {
		t.Fatal("FetchAll() aurait dû échouer")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("nombre de requêtes = %d, attendu 1 (pas de nouvelle tentative sur 404)", got)
	}
}

// TestFetchAll_OneFailureCancelsOthers prouve qu'un fichier en échec annule
// la synchronisation dans son ensemble : le contexte transmis aux autres
// téléchargements en cours est bien annulé (docs/09-pipeline-mise-a-jour.md
// §3.1, exigence E3).
func TestFetchAll_OneFailureCancelsOthers(t *testing.T) {
	t.Parallel()

	var slowCanceled atomic.Bool
	slowStarted := make(chan struct{})
	var slowStartedOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "bad"):
			// N'échoue qu'une fois certain que la requête lente est bien en
			// vol côté serveur : sans cette synchronisation explicite,
			// l'ordre de démarrage des deux requêtes concurrentes n'est pas
			// garanti, et le test deviendrait intermittent (flaky) selon
			// l'ordonnancement des goroutines.
			select {
			case <-slowStarted:
			case <-time.After(5 * time.Second):
			}
			w.WriteHeader(http.StatusNotFound) // échec définitif et immédiat
		case strings.Contains(r.URL.Path, "slow"):
			slowStartedOnce.Do(func() { close(slowStarted) })
			select {
			case <-r.Context().Done():
				slowCanceled.Store(true)
				return
			case <-time.After(10 * time.Second):
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, "trop tard")
			}
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "ok")
		}
	}))
	defer server.Close()

	client := NewClient("1.0.0", append(testClientOptions(server.URL), WithConcurrency(4))...)

	files := []FileDescriptor{
		{Name: "bad.txt", Path: "file/bad.txt"},
		{Name: "slow.txt", Path: "file/slow.txt"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.FetchAll(ctx, files)
	if err == nil {
		t.Fatal("FetchAll() aurait dû échouer")
	}

	// Attendre un court instant que la goroutine lente réagisse à
	// l'annulation de son contexte (le serveur HTTP test propage le
	// contexte de la requête sur r.Context()).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if slowCanceled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-slowStarted:
	default:
		t.Fatal("le fichier lent n'a jamais démarré : le test ne prouve rien")
	}
	if !slowCanceled.Load() {
		t.Error("le contexte du fichier lent aurait dû être annulé suite à l'échec du fichier 'bad'")
	}
}

// TestFetchFile_MaxFileBytesExceeded vérifie que le dépassement de la borne
// de taille par fichier produit une erreur explicite, sans troncature
// silencieuse.
func TestFetchFile_MaxFileBytesExceeded(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer server.Close()

	opts := append(testClientOptions(server.URL), WithMaxFileBytes(1024), WithMaxAttempts(1))
	client := NewClient("1.0.0", opts...)

	_, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err == nil {
		t.Fatal("FetchAll() aurait dû échouer (fichier trop volumineux)")
	}
	if !strings.Contains(err.Error(), "taille maximale") {
		t.Errorf("message d'erreur = %q, devrait mentionner la taille maximale", err.Error())
	}
}

// TestFetchFile_RetryAfterHonored vérifie que l'en-tête Retry-After est pris
// en compte : la seconde tentative n'intervient pas avant le délai indiqué.
func TestFetchFile_RetryAfterHonored(t *testing.T) {
	t.Parallel()

	var firstRequestAt, secondRequestAt time.Time
	var requests atomic.Int32
	const retryAfterSeconds = 1

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			firstRequestAt = time.Now()
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondRequestAt = time.Now()
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	// Backoff par défaut très long pour prouver que c'est bien Retry-After
	// qui gouverne l'attente, pas le repli exponentiel habituel.
	opts := append(testClientOptions(server.URL), WithBackoff(time.Minute, 4))
	client := NewClient("1.0.0", opts...)

	_, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err != nil {
		t.Fatalf("FetchAll() erreur inattendue : %v", err)
	}

	waited := secondRequestAt.Sub(firstRequestAt)
	if waited < retryAfterSeconds*time.Second {
		t.Errorf("attente = %v, attendu au moins %ds (Retry-After)", waited, retryAfterSeconds)
	}
	if waited > 10*time.Second {
		t.Errorf("attente = %v, largement supérieure à Retry-After : le backoff par défaut (1 min) semble avoir été utilisé à la place", waited)
	}
}

// TestFetchAll_RespectsCallerContext vérifie qu'un contexte déjà annulé par
// l'appelant interrompt immédiatement la synchronisation.
func TestFetchAll_RespectsCallerContext(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient("1.0.0", testClientOptions(server.URL)...)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.FetchAll(ctx, DefaultFiles())
	if err == nil {
		t.Fatal("FetchAll() aurait dû échouer avec un contexte déjà annulé")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("erreur = %v, devrait envelopper context.Canceled", err)
	}
}

// TestFetchAll_ConcurrencyIsBounded vérifie que jamais plus de
// c.concurrency requêtes ne sont en vol simultanément, avec un compteur
// atomique mesuré côté serveur.
func TestFetchAll_ConcurrencyIsBounded(t *testing.T) {
	t.Parallel()

	const concurrency = 4
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	opts := append(testClientOptions(server.URL), WithConcurrency(concurrency))
	client := NewClient("1.0.0", opts...)

	_, err := client.FetchAll(context.Background(), DefaultFiles())
	if err != nil {
		t.Fatalf("FetchAll() erreur inattendue : %v", err)
	}

	if got := maxInFlight.Load(); got > concurrency {
		t.Errorf("concurrence maximale observée = %d, attendu <= %d", got, concurrency)
	}
	if got := maxInFlight.Load(); got < 2 {
		t.Errorf("concurrence maximale observée = %d, le test n'a probablement pas atteint de vrai parallélisme", got)
	}
}

// TestFileDescriptor_InfoImportantesException prouve que le fichier des
// informations importantes est représentable avec un chemin distinct des
// dix autres, sans faire partie du jeu par défaut.
func TestFileDescriptor_InfoImportantesException(t *testing.T) {
	t.Parallel()

	for _, f := range DefaultFiles() {
		if f.Name == InfoImportantesFile.Name {
			t.Fatalf("InfoImportantesFile ne doit pas figurer dans DefaultFiles(), trouvé : %+v", f)
		}
	}

	if InfoImportantesFile.URL("https://base-donnees-publique.medicaments.gouv.fr/download") !=
		"https://base-donnees-publique.medicaments.gouv.fr/download/CIS_InfoImportantes.txt" {
		t.Errorf("URL inattendue : %s", InfoImportantesFile.URL("https://base-donnees-publique.medicaments.gouv.fr/download"))
	}

	for _, f := range DefaultFiles() {
		if !strings.HasPrefix(f.Path, "file/") {
			t.Errorf("fichier %s: Path = %q, attendu un préfixe \"file/\"", f.Name, f.Path)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"absent", "", false},
		{"secondes", "5", true},
		{"secondes négatives refusées", "-5", false},
		{"non numérique et non date", "n'importe quoi", false},
		{"date HTTP future", time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat), true},
		{"date HTTP passée", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok := parseRetryAfter(tt.header)
			if ok != tt.want {
				t.Errorf("parseRetryAfter(%q) ok = %v, attendu %v", tt.header, ok, tt.want)
			}
		})
	}
}

// TestWithHTTPClient vérifie que WithHTTPClient remplace bien le client
// HTTP interne (utilisé par exemple pour instrumenter les requêtes dans les
// tests d'intégration d'un appelant).
func TestWithHTTPClient(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	custom := &http.Client{Timeout: 5 * time.Second}
	opts := append(testClientOptions(server.URL), WithHTTPClient(custom))
	client := NewClient("1.0.0", opts...)

	if client.httpClient != custom {
		t.Fatal("WithHTTPClient n'a pas remplacé le client HTTP interne")
	}

	result, err := client.FetchAll(context.Background(), []FileDescriptor{{Name: "X.txt", Path: "file/X.txt"}})
	if err != nil {
		t.Fatalf("FetchAll() erreur inattendue : %v", err)
	}
	if len(result.Files) != 1 || string(result.Files[0].Bytes) != "ok" {
		t.Fatalf("résultat inattendu : %+v", result.Files)
	}
}

// TestOptionGuards vérifie que les options refusent silencieusement les
// valeurs hors domaine en retombant sur un minimum de 1, plutôt que de
// produire un Client inutilisable (concurrence ou tentatives nulles).
func TestOptionGuards(t *testing.T) {
	t.Parallel()

	c := NewClient("1.0.0", WithMaxAttempts(0), WithConcurrency(-1), WithBackoff(time.Second, 0))
	if c.maxAttempts != 1 {
		t.Errorf("maxAttempts = %d, attendu 1", c.maxAttempts)
	}
	if c.concurrency != 1 {
		t.Errorf("concurrency = %d, attendu 1", c.concurrency)
	}
	if c.backoffFactor != 1 {
		t.Errorf("backoffFactor = %d, attendu 1", c.backoffFactor)
	}
}

// TestDefaultJitter vérifie que l'aléa par défaut reste borné à [0, base/2]
// et ne panique jamais, y compris pour une base nulle.
func TestDefaultJitter(t *testing.T) {
	t.Parallel()

	if got := defaultJitter(0); got != 0 {
		t.Errorf("defaultJitter(0) = %v, attendu 0", got)
	}

	base := 100 * time.Millisecond
	for i := 0; i < 100; i++ {
		got := defaultJitter(base)
		if got < 0 || got > base/2+1 {
			t.Fatalf("defaultJitter(%v) = %v, hors bornes [0, %v]", base, got, base/2+1)
		}
	}
}
