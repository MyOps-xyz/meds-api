package api

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/store"
)

// Handlers porte les dépendances des handlers HTTP.
type Handlers struct {
	holder *store.Holder
	rank   store.RankParams
	sync   Syncer
	keys   *KeyStore

	// observeSearch reçoit les mesures de recherche. Peut être nil.
	observeSearch func(mode string, d time.Duration, results int, fallback bool)
}

// search exécute une recherche et l'instrumente.
//
// Le mode est déterminé par le déroulement réel — `trigram` si le repli s'est
// déclenché — et non par la requête : c'est ce qui permet de vérifier en
// production que le repli reste bien exceptionnel.
func (h *Handlers) search(s *store.Store, query string) []store.SearchResult {
	start := time.Now()
	results, stats := s.Search(query, h.rank)
	if h.observeSearch != nil {
		mode := "exact"
		if stats.RepliDeclenche {
			mode = "trigram"
		}
		h.observeSearch(mode, time.Since(start), len(results), stats.RepliDeclenche)
	}
	return results
}

// Syncer déclenche une synchronisation à la demande. L'interface est
// définie côté consommateur pour que la couche HTTP ne dépende pas de
// l'ordonnanceur.
type Syncer interface {
	// Trigger démarre une synchronisation et retourne son identifiant.
	// ErrSyncInProgress si une synchronisation est déjà en cours.
	Trigger() (string, error)
}

// ErrSyncInProgress signale qu'une synchronisation est déjà en cours.
var ErrSyncInProgress = errors.New("une synchronisation est déjà en cours")

// NewHandlers construit les handlers.
func NewHandlers(h *store.Holder, rank store.RankParams, syncer Syncer, keys *KeyStore) *Handlers {
	return &Handlers{holder: h, rank: rank, sync: syncer, keys: keys}
}

// current charge le Store en service.
//
// Appelée **une seule fois par requête** : deux appels successifs pourraient
// encadrer une bascule et produire une réponse mêlant deux versions du jeu
// de données.
func (h *Handlers) current(w http.ResponseWriter, r *http.Request) (*store.Store, bool) {
	s := h.holder.Load()
	if s == nil {
		WriteProblem(w, r, ErrNotReady,
			"Aucun jeu de données n'est chargé. Le service synchronise ; réessayez dans quelques instants.")
		return nil, false
	}
	return s, true
}

// fail traduit une erreur de validation en réponse RFC 9457.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	var stale *staleCursorError
	if errors.As(err, &stale) {
		WriteProblem(w, r, ErrCursorStale, stale.Error())
		return
	}
	WriteProblem(w, r, ErrInvalidParameter, err.Error())
}

// --- Énumérations acceptées en filtre --------------------------------------

// Les clés sont les valeurs publiques, stables et sans accent ; les valeurs
// sont les préfixes des libellés de la source. Cette indirection permet
// d'exposer une énumération propre sans réécrire les valeurs métier, que le
// contrat impose de conserver telles quelles.
var statutAMMFilter = map[string]string{
	"active":    "Autorisation active",
	"abrogee":   "Autorisation abrogée",
	"archivee":  "Autorisation archivée",
	"retiree":   "Autorisation retirée",
	"suspendue": "Autorisation suspendue",
}

var procedureAMMFilter = map[string]string{
	"nationale":               "Procédure nationale",
	"centralisee":             "Procédure centralisée",
	"decentralisee":           "Procédure décentralisée",
	"reconnaissance_mutuelle": "Procédure de reconnaissance mutuelle",
	"homeo":                   "Enregistrement homéopathique",
	"phyto":                   "Enregistrement de médicament traditionnel à base de plantes",
	"importation_parallele":   "Autorisation d'importation parallèle",
}

var generiqueFilter = map[string]string{
	"princeps":        "0",
	"generique":       "1",
	"complementarite": "2",
	"substituable":    "4",
}

var sortFilter = map[string]string{
	"pertinence":   "pertinence",
	"denomination": "denomination",
	"date_amm":     "date_amm",
}

// --- GET /v1/medicaments ----------------------------------------------------

// ListMedicaments applique la recherche et les filtres, combinés en ET.
func (h *Handlers) ListMedicaments(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	// Négociation de cache au plus tôt : la graine ne dépend que de la
	// version du jeu de données, de la route et des paramètres, jamais du
	// résultat. Un client qui détient déjà cette version évite ainsi la
	// recherche, le filtrage, le tri et la sérialisation.
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	query, err := parseQuery(q, false)
	if err != nil {
		fail(w, r, err)
		return
	}
	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	statutAMM, err := parseEnum(q, "statut_amm", statutAMMFilter)
	if err != nil {
		fail(w, r, err)
		return
	}
	procedure, err := parseEnum(q, "procedure_amm", procedureAMMFilter)
	if err != nil {
		fail(w, r, err)
		return
	}
	generique, err := parseEnum(q, "generique", generiqueFilter)
	if err != nil {
		fail(w, r, err)
		return
	}
	tri, err := parseEnum(q, "sort", sortFilter)
	if err != nil {
		fail(w, r, err)
		return
	}
	commercialise, hasCommercialise, err := parseBool(q, "commercialise")
	if err != nil {
		fail(w, r, err)
		return
	}
	surveillance, hasSurveillance, err := parseBool(q, "surveillance")
	if err != nil {
		fail(w, r, err)
		return
	}
	mitm, hasMITM, err := parseBool(q, "mitm")
	if err != nil {
		fail(w, r, err)
		return
	}
	remboursable, hasRemboursable, err := parseBool(q, "remboursable")
	if err != nil {
		fail(w, r, err)
		return
	}
	rupture, hasRupture, err := parseBool(q, "rupture")
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	forme := strings.ToLower(strings.TrimSpace(q.Get("forme")))
	voie := strings.ToLower(strings.TrimSpace(q.Get("voie")))
	titulaire := store.Normalize(q.Get("titulaire"))
	substance := strings.TrimSpace(q.Get("substance"))

	// Ensemble de départ : les résultats de recherche si `q` est fourni,
	// toutes les spécialités sinon.
	type candidate struct {
		id    store.SpecID
		score float64
	}
	var candidates []candidate

	if query != "" {
		results := h.search(s, query)
		candidates = make([]candidate, 0, len(results))
		for _, res := range results {
			candidates = append(candidates, candidate{id: res.Spec, score: res.Score})
		}
	} else {
		candidates = make([]candidate, 0, s.NbSpecialites())
		for i := 0; i < s.NbSpecialites(); i++ {
			candidates = append(candidates, candidate{id: store.SpecID(i)})
		}
	}

	substanceNorm := store.Normalize(substance)
	filtered := candidates[:0]
	for _, c := range candidates {
		sp := s.Spec(c.id)
		if hasCommercialise && (s.EtatCommercialisation(sp) == "Commercialisée") != commercialise {
			continue
		}
		if statutAMM != "" && s.StatutAMM(sp) != statutAMM {
			continue
		}
		if procedure != "" && s.ProcedureAMM(sp) != procedure {
			continue
		}
		if forme != "" && !strings.EqualFold(s.Forme(sp), forme) {
			continue
		}
		if voie != "" && !s.VoieMatches(sp, voie) {
			continue
		}
		if titulaire != "" && !s.TitulaireMatches(sp, titulaire) {
			continue
		}
		if hasSurveillance && sp.Surveillance != surveillance {
			continue
		}
		if hasMITM && (len(s.MITM(c.id)) > 0) != mitm {
			continue
		}
		if substance != "" && !matchSubstance(s, c.id, substance, substanceNorm) {
			continue
		}
		if generique != "" && !matchGenerique(s, c.id, generique) {
			continue
		}
		if hasRemboursable && hasRemboursement(s, c.id) != remboursable {
			continue
		}
		if hasRupture && (len(s.Ruptures(c.id)) > 0) != rupture {
			continue
		}
		filtered = append(filtered, c)
	}

	// Sans `q`, le tri par pertinence n'a pas de sens et bascule sur la
	// dénomination.
	if tri == "" {
		tri = "pertinence"
	}
	if tri == "pertinence" && query == "" {
		tri = "denomination"
	}
	switch tri {
	case "denomination":
		sort.SliceStable(filtered, func(i, j int) bool {
			return s.Spec(filtered[i].id).DenomNorm < s.Spec(filtered[j].id).DenomNorm
		})
	case "date_amm":
		sort.SliceStable(filtered, func(i, j int) bool {
			return s.Spec(filtered[i].id).DateAMM > s.Spec(filtered[j].id).DateAMM
		})
	}

	lo, hi, next, hasMore := paginate(len(filtered), offset, limit, s.Manifest.Hash)
	data := make([]SpecListView, 0, hi-lo)
	for _, c := range filtered[lo:hi] {
		var score *float64
		if query != "" {
			sc := c.score
			score = &sc
		}
		data = append(data, specListView(s, c.id, score))
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{
			Total: len(filtered), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version,
		},
	})
}

// matchSubstance accepte indifféremment un code de substance ou une
// dénomination : l'appelant ne sait pas toujours lequel il détient, et
// exiger le bon serait un obstacle gratuit.
func matchSubstance(s *store.Store, id store.SpecID, raw, norm string) bool {
	for _, c := range s.Composants(id) {
		if codeStr(c.Code) == raw {
			return true
		}
		if norm != "" && strings.Contains(c.DenomNorm, norm) {
			return true
		}
	}
	return false
}

// matchGenerique teste le rôle d'une spécialité dans son groupe générique.
// L'énumération de la source est discontinue — 0, 1, 2, 4, la valeur 3
// n'existe pas — d'où la comparaison directe au code plutôt qu'un intervalle.
func matchGenerique(s *store.Store, id store.SpecID, typeStr string) bool {
	gid, ok := s.GroupeDe(id)
	if !ok {
		return false
	}
	g := s.Grp(gid)
	if g == nil || len(typeStr) != 1 {
		return false
	}
	want := uint8(typeStr[0] - '0')
	for _, m := range g.Membres {
		if m.Spec == id {
			return m.Type == want
		}
	}
	return false
}

func hasRemboursement(s *store.Store, id store.SpecID) bool {
	for _, p := range s.Presentations(id) {
		if len(p.TauxRemb) > 0 {
			return true
		}
	}
	return false
}

// normalizedParams extrait les paramètres significatifs pour l'ETag.
//
// Les valeurs par défaut sont omises : `?limit=20&q=x` et `?q=x` produisent
// la même réponse, donc doivent produire le même ETag.
func normalizedParams(q url.Values) map[string]string {
	out := make(map[string]string, 8)
	for _, name := range etagParams {
		if v := strings.TrimSpace(q.Get(name)); v != "" {
			out[name] = v
		}
	}
	if v := q.Get("limit"); v != "" && v != strconv.Itoa(DefaultLimit) {
		out["limit"] = v
	}
	return out
}

// etagParams énumère les paramètres qui font varier une réponse, donc son
// ETag. Un paramètre de filtrage absent de cette liste produirait un 304 pour
// une réponse qui, elle, aurait changé : toute nouvelle option de requête doit
// y être ajoutée.
var etagParams = []string{
	"q", "commercialise", "statut_amm", "procedure_amm", "forme", "voie",
	"titulaire", "substance", "surveillance", "mitm", "generique",
	"remboursable", "rupture", "sort", "cursor", "include", "statut",
	"actives", "cis", "depuis",
}

// --- GET /v1/medicaments/{cis} ---------------------------------------------

// includeSections énumère les sections optionnelles de la fiche détaillée,
// déjà triées : l'ordre sert le message d'erreur, et la liste est trop courte
// pour qu'un ensemble batte un parcours linéaire.
var includeSections = []string{
	"avis", "composition", "conditions", "generiques", "presentations", "ruptures",
}

// GetMedicament sert la fiche d'un médicament.
func (h *Handlers) GetMedicament(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	cis := r.PathValue("cis")
	if err := validateCode(cis, "cis", 8); err != nil {
		fail(w, r, err)
		return
	}
	id, found := s.SpecByCIS(cis)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucune spécialité ne porte le code CIS "+cis+".")
		return
	}

	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	include := map[string]bool{}
	if raw := strings.TrimSpace(q.Get("include")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			p := strings.ToLower(strings.TrimSpace(part))
			if p == "all" {
				for _, k := range includeSections {
					include[k] = true
				}
				continue
			}
			if !slices.Contains(includeSections, p) {
				fail(w, r, invalid("include ne reconnaît pas la section %q ; valeurs admises : %s ou all.",
					truncate(p, 40), "{"+strings.Join(includeSections, ", ")+"}"))
				return
			}
			include[p] = true
		}
	}

	v := specView(s, id)
	if include["presentations"] {
		v.Presentations = h.presViews(s, id)
	}
	if include["composition"] {
		v.Composition = h.compoViews(s, id)
	}
	if include["generiques"] {
		if gid, ok := s.GroupeDe(id); ok {
			g := groupeView(s, s.Grp(gid))
			v.Generiques = &g
		}
	}
	if include["avis"] {
		v.Avis = h.avisSections(s, id)
	}
	if include["conditions"] {
		v.Conditions = h.conditionViews(s, id)
	}
	if include["ruptures"] {
		v.Ruptures = h.ruptViews(s, id)
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: v,
		Meta: &Meta{DatasetVersion: s.Manifest.Version},
	})
}

func (h *Handlers) presViews(s *store.Store, id store.SpecID) []PresView {
	list := s.Presentations(id)
	out := make([]PresView, 0, len(list))
	for i := range list {
		out = append(out, presView(s, &list[i]))
	}
	return out
}

func (h *Handlers) compoViews(s *store.Store, id store.SpecID) []CompoView {
	list := s.Composants(id)
	out := make([]CompoView, 0, len(list))
	for i := range list {
		out = append(out, compoView(s, &list[i]))
	}
	return out
}

func (h *Handlers) avisSections(s *store.Store, id store.SpecID) *AvisSections {
	smr := s.AvisSMR(id)
	asmr := s.AvisASMR(id)
	sec := &AvisSections{
		SMR:  make([]AvisView, 0, len(smr)),
		ASMR: make([]AvisView, 0, len(asmr)),
	}
	for i := range smr {
		sec.SMR = append(sec.SMR, avisView(&smr[i]))
	}
	for i := range asmr {
		sec.ASMR = append(sec.ASMR, avisView(&asmr[i]))
	}
	return sec
}

func (h *Handlers) conditionViews(s *store.Store, id store.SpecID) []string {
	list := s.Conditions(id)
	out := make([]string, 0, len(list))
	for i := range list {
		out = append(out, list[i].Texte)
	}
	return out
}

func (h *Handlers) ruptViews(s *store.Store, id store.SpecID) []RuptView {
	list := s.Ruptures(id)
	out := make([]RuptView, 0, len(list))
	for i := range list {
		out = append(out, ruptView(s, &list[i]))
	}
	return out
}

// --- Sous-ressources --------------------------------------------------------

// subResource factorise les cinq sous-ressources d'un médicament : même
// validation, même 404, même enveloppe. Seule la projection change.
func (h *Handlers) subResource(w http.ResponseWriter, r *http.Request, project func(*store.Store, store.SpecID) any) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	cis := r.PathValue("cis")
	if err := validateCode(cis, "cis", 8); err != nil {
		fail(w, r, err)
		return
	}
	id, found := s.SpecByCIS(cis)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucune spécialité ne porte le code CIS "+cis+".")
		return
	}

	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}
	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: project(s, id),
		Meta: &Meta{DatasetVersion: s.Manifest.Version},
	})
}

// GetPresentationsDe sert les présentations d'un médicament.
func (h *Handlers) GetPresentationsDe(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any { return h.presViews(s, id) })
}

// GetComposition sert la composition d'un médicament.
func (h *Handlers) GetComposition(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any { return h.compoViews(s, id) })
}

// GetGeneriques sert le groupe générique d'un médicament.
func (h *Handlers) GetGeneriques(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any {
		gid, ok := s.GroupeDe(id)
		if !ok {
			// Un médicament sans groupe générique n'est pas une erreur :
			// c'est le cas de la majorité d'entre eux.
			return nil
		}
		return groupeView(s, s.Grp(gid))
	})
}

// GetAvis sert les avis SMR et ASMR d'un médicament.
func (h *Handlers) GetAvis(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any { return h.avisSections(s, id) })
}

// GetConditions sert les conditions de prescription et de délivrance.
func (h *Handlers) GetConditions(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any { return h.conditionViews(s, id) })
}

// GetRupturesDe sert les fiches de disponibilité d'un médicament.
func (h *Handlers) GetRupturesDe(w http.ResponseWriter, r *http.Request) {
	h.subResource(w, r, func(s *store.Store, id store.SpecID) any { return h.ruptViews(s, id) })
}
