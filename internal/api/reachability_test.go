package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
)

// Objectif O1 de docs/00-vision-et-perimetre.md : **exposer 100 % des données
// des dix fichiers BDPM** — chaque champ de chaque fichier doit être
// atteignable par au moins un endpoint.
//
// Ce test est transversal et automatique : il énumère les champs des entités
// du snapshot par réflexion, puis vérifie que chacun apparaît dans au moins
// une réponse de l'API. Une liste écrite à la main dériverait au premier
// champ ajouté ; la réflexion, elle, échoue le jour où un champ est ingéré
// mais jamais servi.
//
// C'est exactement le défaut qu'il faut prévenir : un champ parsé, validé,
// écrit au snapshot, chargé en mémoire — et invisible de l'extérieur. Rien ne
// le signalerait sans ce test.
func TestO1_AtteignabiliteDesChamps(t *testing.T) {
	h, _ := testServer(t, true)

	// Les entités du snapshot, telles que définies par le paquet bdpm.
	entites := []any{
		bdpm.Specialite{}, bdpm.Presentation{}, bdpm.Composant{},
		bdpm.AvisSMR{}, bdpm.AvisASMR{}, bdpm.Condition{},
		bdpm.Rupture{}, bdpm.InfoMITM{}, bdpm.Substance{},
		bdpm.GroupeGenerique{}, bdpm.MembreGroupe{},
	}

	// Champs délibérément non exposés, chacun avec sa raison. Toute entrée
	// ici est une décision assumée, pas un oubli.
	nonExposes := map[string]string{
		// `condition` est aplati : /v1/medicaments/{cis}/conditions renvoie un
		// tableau de chaînes, la clé n'apporterait rien.
		"Condition.condition": "aplati en tableau de chaînes",
	}

	attendus := map[string]string{} // "Entité.champ" → nom JSON
	for _, e := range entites {
		typ := reflect.TypeOf(e)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" {
				// Les entités intermédiaires (LienAvisCT, GroupeAppartenance)
				// n'ont pas de tag : elles sont consommées à l'ingestion et
				// n'ont pas vocation à être servies telles quelles.
				continue
			}
			name := strings.SplitN(tag, ",", 2)[0]
			cle := typ.Name() + "." + name
			if _, ok := nonExposes[cle]; ok {
				continue
			}
			attendus[cle] = name
		}
	}
	if len(attendus) < 40 {
		t.Fatalf("seulement %d champs énumérés : la réflexion n'a pas fonctionné", len(attendus))
	}

	// Toutes les réponses de l'API, agrégées. Les clés JSON rencontrées, à
	// n'importe quelle profondeur, forment l'ensemble des champs atteignables.
	routes := []string{
		"/v1/medicaments?q=doliprane",
		"/v1/medicaments/60234100?include=all",
		"/v1/medicaments/60234100/presentations",
		"/v1/medicaments/60234100/composition",
		"/v1/medicaments/60234100/generiques",
		"/v1/medicaments/60234100/avis",
		"/v1/medicaments/60234100/conditions",
		"/v1/medicaments/60234100/ruptures",
		"/v1/medicaments/60002285/ruptures",
		"/v1/presentations/3400936030497",
		"/v1/substances",
		"/v1/substances/2202",
		"/v1/groupes-generiques/1",
		"/v1/ruptures?actives=false",
		"/v1/mitm",
		"/v1/suggest?q=dol",
		"/v1/dataset",
	}

	atteints := map[string]struct{}{}
	for _, route := range routes {
		rec := do(t, h, "GET", route, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s : HTTP %d — %s", route, rec.Code, rec.Body.String())
		}
		var doc any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("%s : réponse illisible — %v", route, err)
		}
		collecterCles(doc, atteints)
	}

	var manquants []string
	for cle, nom := range attendus {
		if _, ok := atteints[nom]; !ok {
			manquants = append(manquants, cle)
		}
	}
	sort.Strings(manquants)

	if len(manquants) > 0 {
		t.Errorf("objectif O1 non tenu — %d champ(s) ingéré(s) mais jamais servi(s) :\n  %s",
			len(manquants), strings.Join(manquants, "\n  "))
	}
	t.Logf("O1 : %d champs énumérés, %d clés distinctes atteintes sur %d routes",
		len(attendus), len(atteints), len(routes))
}

// collecterCles descend récursivement dans un document JSON et relève toutes
// les clés d'objet rencontrées.
func collecterCles(v any, out map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			out[k] = struct{}{}
			collecterCles(sub, out)
		}
	case []any:
		for _, sub := range t {
			collecterCles(sub, out)
		}
	}
}

// Objectif O6 : aucun accès anonyme aux données.
//
// Le test est exhaustif sur les routes `/v1/*` plutôt qu'échantillonné : une
// seule route oubliée dans la chaîne d'authentification suffirait à exposer le
// jeu de données, et rien ne le signalerait.
func TestO6_AucunAccesAnonymeAuxDonnees(t *testing.T) {
	h, _ := testServer(t, true)

	protegees := []string{
		"/v1/medicaments", "/v1/medicaments/60234100",
		"/v1/medicaments/60234100/presentations", "/v1/medicaments/60234100/composition",
		"/v1/medicaments/60234100/generiques", "/v1/medicaments/60234100/avis",
		"/v1/medicaments/60234100/conditions", "/v1/medicaments/60234100/ruptures",
		"/v1/presentations/3400936030497", "/v1/substances", "/v1/substances/2202",
		"/v1/substances/2202/medicaments", "/v1/groupes-generiques",
		"/v1/groupes-generiques/1", "/v1/ruptures", "/v1/mitm",
		"/v1/suggest?q=dol", "/v1/dataset",
	}
	for _, route := range protegees {
		rec := doNoAuth(t, h, route)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s accessible sans clé : HTTP %d", route, rec.Code)
		}
		// Et la réponse ne doit contenir aucune donnée métier.
		if strings.Contains(rec.Body.String(), "DOLIPRANE") {
			t.Errorf("%s : le refus laisse fuiter de la donnée", route)
		}
	}

	// Les endpoints d'exploitation restent ouverts, par conception.
	for _, route := range []string{"/healthz", "/readyz", "/openapi.json", "/docs"} {
		if rec := doNoAuth(t, h, route); rec.Code != http.StatusOK {
			t.Errorf("%s devrait rester ouvert : HTTP %d", route, rec.Code)
		}
	}
}
