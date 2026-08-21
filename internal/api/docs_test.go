package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAPI_ServiEnJSON(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/openapi.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d — %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("le contrat servi n'est pas du JSON valide : %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, veut 3.1.0", doc["openapi"])
	}

	// L'avertissement obligatoire figure dans la description (T-31, T-33).
	info := doc["info"].(map[string]any)
	if !strings.Contains(info["description"].(string), "ne se substituent") {
		t.Error("l'avertissement obligatoire est absent de la description OpenAPI")
	}
	if !strings.Contains(info["description"].(string), "Licence Ouverte") {
		t.Error("la mention de licence est absente de la description OpenAPI")
	}
}

// Chaque route servie doit être décrite, et réciproquement : c'est la seule
// façon d'éviter qu'un endpoint dérive silencieusement de son contrat.
func TestOpenAPI_CouvreToutesLesRoutes(t *testing.T) {
	doc, err := specAsJSON()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}

	served := []string{
		"/v1/medicaments", "/v1/medicaments/{cis}",
		"/v1/medicaments/{cis}/presentations", "/v1/medicaments/{cis}/composition",
		"/v1/medicaments/{cis}/generiques", "/v1/medicaments/{cis}/avis",
		"/v1/medicaments/{cis}/conditions", "/v1/medicaments/{cis}/ruptures",
		"/v1/presentations/{cip}", "/v1/substances", "/v1/substances/{code}",
		"/v1/substances/{code}/medicaments", "/v1/groupes-generiques",
		"/v1/groupes-generiques/{id}", "/v1/ruptures", "/v1/mitm",
		"/v1/suggest", "/v1/dataset",
		"/healthz", "/readyz", "/metrics", "/openapi.json", "/docs", "/admin/sync",
	}
	for _, path := range served {
		if _, ok := parsed.Paths[path]; !ok {
			t.Errorf("route servie mais absente du contrat : %s", path)
		}
	}
	for path := range parsed.Paths {
		found := false
		for _, s := range served {
			if s == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("route décrite au contrat mais non servie : %s", path)
		}
	}
}

// Critère d'acceptation T-33 : /docs fonctionne **hors ligne**, sans aucune
// ressource externe.
func TestDocs_AutonomeHorsLigne(t *testing.T) {
	h, _ := testServer(t, true)

	rec := do(t, h, "GET", "/docs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d — %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rec.Body.String()

	// Aucune ressource distante : ni script, ni feuille de style, ni police,
	// ni image chargée depuis un autre hôte.
	for _, forbidden := range []string{
		"https://cdn", "http://cdn", "unpkg.com", "jsdelivr", "googleapis.com",
		"<script src=", "<script type=\"module\"", "@import url(http",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("la page charge une ressource externe : %q", forbidden)
		}
	}
	// Aucun script du tout, en réalité : la page est statique.
	if strings.Contains(body, "<script") {
		t.Error("la page de documentation ne doit contenir aucun script")
	}

	// La politique de sécurité interdit explicitement toute ressource
	// distante.
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}

func TestDocs_ContenuAttendu(t *testing.T) {
	h, _ := testServer(t, true)
	body := do(t, h, "GET", "/docs", nil).Body.String()

	// L'avertissement obligatoire est rendu, et mis en évidence.
	if !strings.Contains(body, "ne se substituent") {
		t.Error("l'avertissement obligatoire n'apparaît pas sur /docs")
	}
	if !strings.Contains(body, `class="warn"`) {
		t.Error("l'avertissement n'est pas mis en évidence")
	}

	// Les routes principales sont listées.
	for _, path := range []string{
		"/v1/medicaments", "/v1/presentations/{cip}", "/v1/dataset", "/admin/sync",
	} {
		if !strings.Contains(body, path) {
			t.Errorf("route absente de /docs : %s", path)
		}
	}
	// Et le lien vers le contrat brut.
	if !strings.Contains(body, "/openapi.json") {
		t.Error("le lien vers /openapi.json est absent")
	}
}

// La documentation ne doit pas devenir un vecteur d'injection : tout ce qui
// vient du contrat est échappé, seul le balisage que nous produisons passe.
func TestDocs_EchappementDuContrat(t *testing.T) {
	got := paragraphs("Un <script>alert(1)</script> et du `code` et du **gras**.")
	if strings.Contains(got, "<script>") {
		t.Errorf("le balisage du contrat n'est pas échappé : %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("forme échappée attendue : %q", got)
	}
	if !strings.Contains(got, "<code>code</code>") {
		t.Errorf("le code inline n'est pas rendu : %q", got)
	}
	if !strings.Contains(got, "<strong>gras</strong>") {
		t.Errorf("le gras n'est pas rendu : %q", got)
	}
}

func TestOpenAPIEtDocs_SansAuthentification(t *testing.T) {
	h, _ := testServer(t, true)
	for _, path := range []string{"/openapi.json", "/docs"} {
		rec := doNoAuth(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s sans clé : %d, veut 200 (endpoint exempté)", path, rec.Code)
		}
	}
}

func TestOpenAPI_304(t *testing.T) {
	h, _ := testServer(t, true)
	etag := do(t, h, "GET", "/openapi.json", nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag absent")
	}
	rec := do(t, h, "GET", "/openapi.json", map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Errorf("code = %d, veut 304", rec.Code)
	}
}
