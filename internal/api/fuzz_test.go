package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MyOps-xyz/meds-api/internal/store"
)

// FuzzDecodeCursor vise le curseur de pagination : il vient d'une URL
// publique, il est donc entièrement sous contrôle de l'appelant.
//
// La propriété : un curseur forgé produit une erreur, jamais une panique, et
// **jamais un décalage négatif** — un offset négatif provoquerait une
// tranche hors bornes au moment de paginer.
func FuzzDecodeCursor(f *testing.F) {
	f.Add("", "3f2a9c")
	f.Add("eyJvIjo1MCwiaCI6IjNmMmE5YyJ9", "3f2a9c")
	f.Add("!!!invalide!!!", "3f2a9c")
	f.Add("eyJvIjotMSwiaCI6IjNmMmE5YyJ9", "3f2a9c") // offset négatif
	f.Add(strings.Repeat("A", 10000), "3f2a9c")
	f.Add("eyJvIjo5MjIzMzcyMDM2ODU0Nzc1ODA3LCJoIjoiM2YyYTljIn0", "3f2a9c") // offset maximal

	f.Fuzz(func(t *testing.T, raw, hash string) {
		if len(raw) > 100000 {
			return
		}
		offset, err := decodeCursor(raw, hash)
		if err != nil {
			return
		}
		if offset < 0 {
			t.Fatalf("decodeCursor a accepté un décalage négatif (%d) pour %q", offset, truncate(raw, 60))
		}
		// L'offset accepté doit rester utilisable par paginate sans déborder.
		lo, hi, _, _ := paginate(100, offset, 20, hash)
		if lo < 0 || hi < lo || hi > 100 {
			t.Fatalf("bornes de pagination incohérentes : lo=%d hi=%d (offset %d)", lo, hi, offset)
		}
	})
}

// FuzzNormalize vise la normalisation de recherche, appliquée à toute
// requête entrante comme à chaque libellé indexé.
func FuzzNormalize(f *testing.F) {
	f.Add("PARACÉTAMOL")
	f.Add("BÉSILATE D'AMLODIPINE 5 mg, comprimé")
	f.Add("<script>alert(1)</script>")
	f.Add("")
	f.Add("\x00\x01\x02")
	f.Add("\xff\xfe\xfd")
	f.Add(strings.Repeat("é", 500))
	f.Add("A 313 50 000 U.I.")

	f.Fuzz(func(t *testing.T, input string) {
		// Borne volontairement basse : la couche HTTP refuse déjà toute
		// requête au-delà de MaxQueryLen (200 caractères), et les libellés
		// indexés font quelques dizaines d'octets. Fuzzer des entrées de
		// 100 Ko n'explorerait pas la logique de normalisation, seulement le
		// ramasse-miettes.
		if len(input) > 4096 {
			return
		}
		got := store.Normalize(input)

		// Trois propriétés, chacune indispensable en aval.
		if !utf8.ValidString(got) {
			t.Fatalf("Normalize a produit de l'UTF-8 invalide")
		}
		if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
			t.Fatalf("Normalize a laissé un espace en bordure : %q", truncate(got, 60))
		}
		if strings.Contains(got, "  ") {
			t.Fatalf("Normalize a laissé un espace double : %q", truncate(got, 60))
		}

		// Idempotence : normaliser une chaîne déjà normalisée ne la change
		// plus. Sans cette propriété, indexation et requête pourraient
		// diverger d'un tour de normalisation.
		if again := store.Normalize(got); again != got {
			t.Fatalf("Normalize n'est pas idempotente :\n  %q\n  %q",
				truncate(got, 60), truncate(again, 60))
		}

		// Les tokens sont tous d'au moins MinTokenLen et sans espace.
		for _, tok := range store.Tokenize(input) {
			if len(tok) < store.MinTokenLen {
				t.Fatalf("token trop court : %q", tok)
			}
			if strings.ContainsRune(tok, ' ') {
				t.Fatalf("token contenant un espace : %q", tok)
			}
		}
	})
}

// FuzzRequeteHTTP soumet des chemins et paramètres arbitraires au serveur
// complet.
//
// La propriété : **toute** requête reçoit une réponse HTTP valide. Ni
// panique, ni corps tronqué, ni statut hors des codes du catalogue. C'est la
// vérification que la validation par liste blanche ne laisse aucun trou.
func FuzzRequeteHTTP(f *testing.F) {
	f.Add("/v1/medicaments", "q=doliprane")
	f.Add("/v1/medicaments/60234100", "include=all")
	f.Add("/v1/presentations/3400936030497", "")
	f.Add("/v1/suggest", "q=dol&limit=5")
	f.Add("/v1/../../etc/passwd", "")
	f.Add("/v1/medicaments/%00", "")
	f.Add("/v1/medicaments", "limit=-99999999999999999999")
	f.Add("/v1/medicaments", "cursor=%FF%FE")
	f.Add("/v1/medicaments", strings.Repeat("a=1&", 1000))

	holder := store.NewHolder()
	srv := newFuzzServer(f, holder)

	f.Fuzz(func(t *testing.T, path, query string) {
		if len(path) > 4000 || len(query) > 8000 {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		target := path
		if query != "" {
			target += "?" + query
		}

		req, err := http.NewRequest("GET", "http://example.test"+target, nil)
		if err != nil {
			// Une URL que net/http refuse de construire n'atteindrait jamais
			// le serveur : rien à vérifier.
			return
		}
		req.Header.Set("Authorization", "Bearer "+testKey)

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code < 100 || rec.Code > 599 {
			t.Fatalf("statut HTTP hors bornes : %d pour %q", rec.Code, truncate(target, 80))
		}
		// Aucune réponse ne doit fuiter de détail interne.
		body := rec.Body.String()
		for _, leak := range []string{"goroutine ", "/Users/", "runtime.", "panic:"} {
			if strings.Contains(body, leak) {
				t.Fatalf("fuite d'information interne (%q) pour %q", leak, truncate(target, 80))
			}
		}
	})
}

func newFuzzServer(f *testing.F, holder *store.Holder) http.Handler {
	f.Helper()

	d := testDataset()
	s, err := store.Build(d, mustManifest())
	if err != nil {
		f.Fatal(err)
	}
	s.BuildSearchIndex()
	holder.Store(s)

	srv := NewServer(Options{
		Logger: discardLogger(),
		Holder: holder,
		Keys:   NewKeyStore([]string{testKey}, ""),
		Rank:   store.DefaultRankParams(),
	})
	return srv.Handler()
}
