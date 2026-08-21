package api

import (
	"html"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/store"
)

// --- GET /v1/presentations/{cip} -------------------------------------------

// GetPresentation sert le lookup par code-barres.
func (h *Handlers) GetPresentation(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	cip := r.PathValue("cip")
	if err := validateCode(cip, "cip", 7, 13); err != nil {
		fail(w, r, err)
		return
	}
	pid, found := s.PresByCIP(cip)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucune présentation ne porte le code CIP "+cip+".")
		return
	}

	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, nil)
	if NotModified(w, r, seed) {
		return
	}

	p := s.Pres(pid)
	sp := s.Spec(p.Spec)
	cis := cisStr(sp.CIS)

	v := PresDetailView{
		PresView: presView(s, p),
		Medicament: parentRef{
			CIS: cis, Denomination: sp.Denom, Link: "/v1/medicaments/" + cis,
		},
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: v, Meta: &Meta{DatasetVersion: s.Manifest.Version},
	})
}

// --- Substances -------------------------------------------------------------

// ListSubstances sert le référentiel des substances.
func (h *Handlers) ListSubstances(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	query, err := parseQuery(q, false)
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	norm := store.Normalize(query)
	all := s.AllSubs()
	matched := make([]int, 0, len(all))
	for i := range all {
		if norm == "" || strings.Contains(all[i].DenomNorm, norm) || all[i].CodeStr == query {
			matched = append(matched, i)
		}
	}

	lo, hi, next, hasMore := paginate(len(matched), offset, limit, s.Manifest.Hash)
	data := make([]SubView, 0, hi-lo)
	for _, i := range matched[lo:hi] {
		data = append(data, subView(&all[i], true))
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{Total: len(matched), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version},
	})
}

// GetSubstance sert une substance du référentiel.
func (h *Handlers) GetSubstance(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	code := r.PathValue("code")
	id, found := s.SubByCode(code)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucune substance ne porte le code "+truncate(code, 40)+".")
		return
	}

	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, nil)
	if NotModified(w, r, seed) {
		return
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: subView(s.Sub(id), true), Meta: &Meta{DatasetVersion: s.Manifest.Version},
	})
}

// GetSubstanceMedicaments sert les spécialités contenant une substance.
func (h *Handlers) GetSubstanceMedicaments(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	code := r.PathValue("code")
	subID, found := s.SubByCode(code)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucune substance ne porte le code "+truncate(code, 40)+".")
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	// Le parcours est global faute d'index inverse substance → spécialités :
	// à 32 420 composants, il coûte quelques centaines de microsecondes, ce
	// qui ne justifie pas encore un index dédié et sa consommation mémoire.
	seen := make(map[store.SpecID]struct{})
	var ids []store.SpecID
	for i := 0; i < s.NbSpecialites(); i++ {
		id := store.SpecID(i)
		for _, c := range s.Composants(id) {
			if c.Sub == subID {
				if _, dup := seen[id]; !dup {
					seen[id] = struct{}{}
					ids = append(ids, id)
				}
				break
			}
		}
	}

	lo, hi, next, hasMore := paginate(len(ids), offset, limit, s.Manifest.Hash)
	data := make([]SpecListView, 0, hi-lo)
	for _, id := range ids[lo:hi] {
		data = append(data, specListView(s, id, nil))
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{Total: len(ids), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version},
	})
}

// --- Groupes génériques -----------------------------------------------------

// ListGroupes sert le référentiel des groupes génériques.
func (h *Handlers) ListGroupes(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	query, err := parseQuery(q, false)
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	norm := store.Normalize(query)
	all := s.AllGrps()
	matched := make([]int, 0, len(all))
	for i := range all {
		if norm == "" || strings.Contains(all[i].LibelleNorm, norm) {
			matched = append(matched, i)
		}
	}

	lo, hi, next, hasMore := paginate(len(matched), offset, limit, s.Manifest.Hash)
	data := make([]GroupeView, 0, hi-lo)
	for _, i := range matched[lo:hi] {
		data = append(data, groupeView(s, &all[i]))
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{Total: len(matched), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version},
	})
}

// GetGroupe sert un groupe générique et ses membres.
func (h *Handlers) GetGroupe(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	gid, found := s.GrpByID(id)
	if !found {
		WriteProblem(w, r, ErrNotFound, "Aucun groupe générique ne porte l'identifiant "+truncate(id, 40)+".")
		return
	}

	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, nil)
	if NotModified(w, r, seed) {
		return
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: groupeView(s, s.Grp(gid)), Meta: &Meta{DatasetVersion: s.Manifest.Version},
	})
}

// --- Ruptures ---------------------------------------------------------------

var statutRuptureFilter = map[string]string{
	"rupture":            "rupture",
	"tension":            "tension",
	"arret":              "arret",
	"remise_disposition": "remise_disposition",
}

// ListRuptures sert les fiches de disponibilité.
func (h *Handlers) ListRuptures(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	statut, err := parseEnum(q, "statut", statutRuptureFilter)
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	// `actives` vaut true par défaut : la question posée à cet endpoint est
	// « qu'est-ce qui manque en ce moment ? », pas « qu'est-ce qui a manqué
	// un jour ». Le défaut inverse noierait le signal utile.
	actives, hasActives, err := parseBool(q, "actives")
	if err != nil {
		fail(w, r, err)
		return
	}
	if !hasActives {
		actives = true
	}

	var cisFilter store.SpecID = store.InvalidID
	if raw := strings.TrimSpace(q.Get("cis")); raw != "" {
		if err := validateCode(raw, "cis", 8); err != nil {
			fail(w, r, err)
			return
		}
		id, found := s.SpecByCIS(raw)
		if !found {
			WriteProblem(w, r, ErrNotFound, "Aucune spécialité ne porte le code CIS "+raw+".")
			return
		}
		cisFilter = id
	}

	depuis := store.DateAbsente
	if raw := strings.TrimSpace(q.Get("depuis")); raw != "" {
		d, valide := store.ParseDay(raw)
		if !valide {
			fail(w, r, invalid("depuis doit être une date ISO 8601 (AAAA-MM-JJ), reçu %q.", truncate(raw, 40)))
			return
		}
		depuis = d
	}

	all := s.AllRuptures()
	matched := make([]int, 0, len(all))
	for i := range all {
		r0 := &all[i]
		if statut != "" && r0.Statut != statut {
			continue
		}
		if cisFilter != store.InvalidID && r0.Spec != cisFilter {
			continue
		}
		if depuis > store.DateAbsente && r0.DateMAJ < depuis {
			continue
		}
		if actives {
			if _, remise := r0.DateRemiseDisposition(); remise {
				continue
			}
		}
		matched = append(matched, i)
	}
	// Les plus récemment mises à jour d'abord : c'est l'ordre utile en
	// exploitation.
	sort.SliceStable(matched, func(a, b int) bool {
		return all[matched[a]].DateMAJ > all[matched[b]].DateMAJ
	})

	lo, hi, next, hasMore := paginate(len(matched), offset, limit, s.Manifest.Hash)
	data := make([]RuptView, 0, hi-lo)
	for _, i := range matched[lo:hi] {
		data = append(data, ruptView(s, &all[i]))
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{Total: len(matched), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version},
	})
}

// --- MITM -------------------------------------------------------------------

// MITMListView est une entrée de la liste des médicaments d'intérêt
// thérapeutique majeur.
type MITMListView struct {
	CIS          string `json:"cis"`
	CodeATC      string `json:"code_atc"`
	Denomination string `json:"denomination"`
	LienBDPM     string `json:"lien_bdpm"`
	Link         string `json:"_link"`
}

// ListMITM sert les médicaments d'intérêt thérapeutique majeur.
func (h *Handlers) ListMITM(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	limit, err := parseLimit(q, MaxLimit)
	if err != nil {
		fail(w, r, err)
		return
	}
	offset, err := decodeCursor(q.Get("cursor"), s.Manifest.Hash)
	if err != nil {
		fail(w, r, err)
		return
	}

	all := s.AllMITM()
	lo, hi, next, hasMore := paginate(len(all), offset, limit, s.Manifest.Hash)
	data := make([]MITMListView, 0, hi-lo)
	for i := lo; i < hi; i++ {
		cis := cisStr(s.Spec(all[i].Spec).CIS)
		data = append(data, MITMListView{
			CIS: cis, CodeATC: all[i].CodeATC, Denomination: all[i].Denom,
			LienBDPM: all[i].Lien, Link: "/v1/medicaments/" + cis,
		})
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data,
		Meta: &Meta{Total: len(all), Limit: limit, HasMore: hasMore,
			NextCursor: next, DatasetVersion: s.Manifest.Version},
	})
}

// --- Autocomplétion ---------------------------------------------------------

// SuggestView est une proposition d'autocomplétion.
type SuggestView struct {
	Type      string `json:"type"`
	CIS       string `json:"cis,omitempty"`
	Code      string `json:"code,omitempty"`
	Label     string `json:"label"`
	Highlight string `json:"highlight,omitempty"`
}

// Suggest sert l'autocomplétion.
func (h *Handlers) Suggest(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	seed := ETagSeed(s.Manifest.Hash, r.Method, r.URL.Path, normalizedParams(q))
	if NotModified(w, r, seed) {
		return
	}

	query, err := parseQuery(q, true)
	if err != nil {
		fail(w, r, err)
		return
	}
	limit, err := parseLimit(q, MaxSuggestLimit)
	if err != nil {
		fail(w, r, err)
		return
	}

	norm := store.Normalize(query)
	data := make([]SuggestView, 0, limit)

	// Les médicaments d'abord : c'est ce que l'utilisateur cherche dans la
	// grande majorité des cas.
	for i := 0; i < s.NbSpecialites() && len(data) < limit; i++ {
		sp := s.Spec(store.SpecID(i))
		if !strings.HasPrefix(sp.DenomNorm, norm) {
			continue
		}
		data = append(data, SuggestView{
			Type: "medicament", CIS: cisStr(sp.CIS), Label: sp.Denom,
			Highlight: highlight(sp.Denom, len(query)),
		})
	}
	for i := 0; i < s.NbSubstances() && len(data) < limit; i++ {
		sub := s.Sub(store.SubID(i))
		if !strings.HasPrefix(sub.DenomNorm, norm) {
			continue
		}
		data = append(data, SuggestView{
			Type: "substance", Code: sub.CodeStr, Label: sub.Denom,
			Highlight: highlight(sub.Denom, len(query)),
		})
	}

	WriteJSON(w, r, http.StatusOK, seed, Envelope{
		Data: data, Meta: &Meta{Total: len(data), Limit: limit, DatasetVersion: s.Manifest.Version},
	})
}

// highlight entoure le préfixe correspondant de balises <em>.
//
// Le libellé est **échappé en HTML avant** l'insertion des balises : la
// source contient des caractères actifs (« & », « < »), et les insérer tels
// quels dans un champ destiné à être rendu ferait de l'API le vecteur d'une
// injection dans la page de l'appelant.
func highlight(label string, prefixLen int) string {
	if prefixLen <= 0 || prefixLen > len(label) {
		return html.EscapeString(label)
	}
	return "<em>" + html.EscapeString(label[:prefixLen]) + "</em>" + html.EscapeString(label[prefixLen:])
}

// --- GET /v1/dataset --------------------------------------------------------

// SourceView documente la provenance et la licence, en application de la
// Licence Ouverte qui impose la mention de la source et de sa date de mise à
// jour.
type SourceView struct {
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	URL       string `json:"url"`
	Licence   string `json:"licence"`
}

// DatasetView est l'état et la provenance du jeu de données.
type DatasetView struct {
	Version     string         `json:"version"`
	Hash        string         `json:"hash"`
	GeneratedAt string         `json:"generated_at"`
	AgeSeconds  int64          `json:"age_seconds"`
	Source      SourceView     `json:"source"`
	Counts      map[string]int `json:"counts"`
	Quarantine  any            `json:"quarantine"`
	OutOfScope  any            `json:"out_of_scope"`
	Files       any            `json:"files"`
	Warnings    []string       `json:"warnings,omitempty"`
	Disclaimer  string         `json:"disclaimer"`
}

// Disclaimer est l'avertissement obligatoire
// (docs/04-specification-api.md §6). Il est servi sur /v1/dataset et dans la
// description OpenAPI : une API de données de santé doit dire ce qu'elle
// n'est pas.
const Disclaimer = "Les données proviennent de la Base de données publique des médicaments (ANSM), " +
	"diffusée sous Licence Ouverte (Etalab). Elles sont fournies à titre informatif et ne se " +
	"substituent ni au Résumé des Caractéristiques du Produit, ni à l'avis d'un professionnel de " +
	"santé. Cette API ne constitue pas un dispositif médical et n'apporte aucune aide à la " +
	"décision clinique."

// GetDataset sert l'état et la provenance du jeu de données.
func (h *Handlers) GetDataset(w http.ResponseWriter, r *http.Request) {
	s, ok := h.current(w, r)
	if !ok {
		return
	}
	m := s.Manifest

	var age int64
	if t, err := time.Parse(time.RFC3339, m.GeneratedAt); err == nil {
		age = int64(time.Since(t).Seconds())
	}

	v := DatasetView{
		Version:     m.Version,
		Hash:        m.Hash,
		GeneratedAt: m.GeneratedAt,
		AgeSeconds:  age,
		Source: SourceView{
			Name:      "Base de données publique des médicaments (BDPM)",
			Publisher: "ANSM — Agence nationale de sécurité du médicament et des produits de santé",
			URL:       "https://base-donnees-publique.medicaments.gouv.fr/",
			Licence:   "Licence Ouverte / Open Licence (Etalab)",
		},
		Counts:     m.Counts,
		Quarantine: m.Quarantine,
		OutOfScope: m.OutOfScope,
		Files:      m.SourceFiles,
		Warnings:   m.Warnings,
		Disclaimer: Disclaimer,
	}

	// Pas d'ETag : l'âge du jeu de données change à chaque seconde, un ETag
	// donnerait un 304 sur une réponse pourtant différente.
	WriteJSON(w, r, http.StatusOK, "", Envelope{Data: v})
}

// --- Exploitation -----------------------------------------------------------

// Healthz répond dès que le processus vit, indépendamment du jeu de données.
//
// La distinction avec /readyz est ce qui évite qu'un orchestrateur ne tue en
// boucle un conteneur en cours de première synchronisation : le processus est
// vivant, il n'est simplement pas encore prêt à servir.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, r, http.StatusOK, "", map[string]string{"status": "ok"})
}

// Readyz répond 200 dès qu'un jeu de données est publié, 503 sinon.
//
// Il ne juge **pas** la fraîcheur : un jeu de données vieux de trois jours
// reste servable, et retirer le service de la rotation pour cette raison
// transformerait un incident d'ingestion en panne totale. La fraîcheur est
// une alerte, pas une sonde.
func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	s := h.holder.Load()
	if s == nil {
		WriteJSON(w, r, http.StatusServiceUnavailable, "", map[string]string{
			"status": "not_ready",
			"reason": "aucun jeu de données publié",
		})
		return
	}
	WriteJSON(w, r, http.StatusOK, "", map[string]any{
		"status":          "ready",
		"dataset_version": s.Manifest.Version,
	})
}

// AdminSync déclenche une synchronisation.
func (h *Handlers) AdminSync(w http.ResponseWriter, r *http.Request) {
	// Endpoint inexistant si aucune clé d'administration n'est configurée :
	// 404 plutôt que 401, pour ne pas révéler qu'une capacité
	// d'administration existe mais n'est pas activée.
	if h.keys == nil || !h.keys.HasAdmin() || h.sync == nil {
		WriteProblem(w, r, ErrNotFound, "Cet endpoint n'est pas activé.")
		return
	}
	key := bearerToken(r)
	if key == "" || !h.keys.ValidAdmin(key) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="meds-api-admin"`)
		WriteProblem(w, r, ErrUnauthorized, "Cet endpoint exige la clé d'administration.")
		return
	}

	id, err := h.sync.Trigger()
	if err != nil {
		if strings.Contains(err.Error(), ErrSyncInProgress.Error()) {
			WriteProblem(w, r, ErrConflict, "Une synchronisation est déjà en cours.")
			return
		}
		WriteProblem(w, r, ErrInternal, "La synchronisation n'a pas pu être déclenchée.")
		return
	}
	WriteJSON(w, r, http.StatusAccepted, "", Envelope{
		Data: map[string]string{"status": "accepted", "sync_id": id},
	})
}
