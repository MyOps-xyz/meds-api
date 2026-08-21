package api

import (
	"strconv"

	"github.com/MyOps-xyz/meds-api/internal/store"
)

// Les vues de ce fichier sont la forme publique du jeu de données. Elles
// sont distinctes des structures du Store, qui sont compactes et internes :
// exposer directement des uint16 internés n'aurait aucun sens pour un
// intégrateur, et figer la représentation mémoire dans le contrat d'API
// interdirait toute optimisation ultérieure.
//
// Deux vues coexistent pour les médicaments : la liste sert à **choisir**,
// la fiche à **consulter**. La liste omet composition et avis — la fiche
// complète d'un médicament à 37 présentations et 40 avis pèse plusieurs
// dizaines de kilooctets, inutiles à qui ne veut qu'un libellé.

// SpecListView est la représentation allégée d'une spécialité en liste.
type SpecListView struct {
	CIS                   string   `json:"cis"`
	Denomination          string   `json:"denomination"`
	FormePharmaceutique   string   `json:"forme_pharmaceutique"`
	VoiesAdministration   []string `json:"voies_administration"`
	StatutAMM             string   `json:"statut_amm"`
	EtatCommercialisation string   `json:"etat_commercialisation"`
	DateAMM               *string  `json:"date_amm"`
	Titulaires            []string `json:"titulaires"`
	SurveillanceRenforcee bool     `json:"surveillance_renforcee"`
	Substances            []string `json:"substances"`
	NbPresentations       int      `json:"nb_presentations"`
	MITM                  bool     `json:"mitm"`
	Score                 *float64 `json:"score,omitempty"`
	Links                 selfLink `json:"_links"`
}

type selfLink struct {
	Self          string `json:"self"`
	Presentations string `json:"presentations"`
}

// SpecView est la fiche complète, dont les sections optionnelles ne sont
// peuplées que si `include` les demande.
type SpecView struct {
	CIS                          string    `json:"cis"`
	Denomination                 string    `json:"denomination"`
	FormePharmaceutique          string    `json:"forme_pharmaceutique"`
	VoiesAdministration          []string  `json:"voies_administration"`
	StatutAMM                    string    `json:"statut_amm"`
	ProcedureAMM                 string    `json:"procedure_amm"`
	EtatCommercialisation        string    `json:"etat_commercialisation"`
	DateAMM                      *string   `json:"date_amm"`
	StatutBdm                    *string   `json:"statut_bdm"`
	NumeroAutorisationEuropeenne *string   `json:"numero_autorisation_europeenne"`
	Titulaires                   []string  `json:"titulaires"`
	SurveillanceRenforcee        bool      `json:"surveillance_renforcee"`
	MITM                         *MITMView `json:"mitm"`

	Presentations []PresView    `json:"presentations,omitempty"`
	Composition   []CompoView   `json:"composition,omitempty"`
	Generiques    *GroupeView   `json:"generiques,omitempty"`
	Avis          *AvisSections `json:"avis,omitempty"`
	Conditions    []string      `json:"conditions,omitempty"`
	Ruptures      []RuptView    `json:"ruptures,omitempty"`
}

// PresView est une présentation commerciale.
//
// Tous les prix sont des pointeurs : l'absence de prix concerne 7 276 des
// 20 903 présentations, et la confondre avec un prix nul fausserait toute
// statistique construite sur l'API.
type PresView struct {
	CIP13                       string  `json:"cip13"`
	CIP7                        string  `json:"cip7"`
	Libelle                     string  `json:"libelle"`
	StatutAdministratif         string  `json:"statut_administratif"`
	EtatCommercialisation       string  `json:"etat_commercialisation"`
	DateDeclaration             *string `json:"date_declaration"`
	AgrementCollectivites       *bool   `json:"agrement_collectivites"`
	TauxRemboursement           []int   `json:"taux_remboursement"`
	PrixMedicamentCents         *int32  `json:"prix_medicament_cents"`
	PrixPublicCents             *int32  `json:"prix_public_cents"`
	HonorairesDispensationCents *int32  `json:"honoraires_dispensation_cents"`
	IndicationsRemboursement    *string `json:"indications_remboursement"`
}

// PresDetailView ajoute le médicament parent, inclus d'office : après un
// scan de code-barres, l'appelant veut toujours savoir de quel médicament il
// s'agit, et lui imposer un second appel serait un défaut de conception.
type PresDetailView struct {
	PresView
	Medicament parentRef `json:"medicament"`
}

type parentRef struct {
	CIS          string `json:"cis"`
	Denomination string `json:"denomination"`
	Link         string `json:"_link"`
}

// CompoView est un élément de composition.
type CompoView struct {
	ElementPharmaceutique string `json:"element_pharmaceutique"`
	CodeSubstance         string `json:"code_substance"`
	DenominationSubstance string `json:"denomination_substance"`
	Dosage                string `json:"dosage"`
	ReferenceDosage       string `json:"reference_dosage"`
	Nature                string `json:"nature"`
	NumeroLiaison         int    `json:"numero_liaison"`
}

// AvisView est un avis SMR ou ASMR.
type AvisView struct {
	CodeDossierHAS  string  `json:"code_dossier_has"`
	MotifEvaluation string  `json:"motif_evaluation"`
	DateAvis        *string `json:"date_avis"`
	Valeur          string  `json:"valeur"`
	// Sans omitempty, contrairement à la première rédaction : la convention
	// de docs/04-specification-api.md §1 est « absence de valeur ⇒ null,
	// jamais "" ni 0 ». Un champ qui disparaît par intermittence oblige le
	// client à distinguer « clé absente » de « clé nulle », alors que tous
	// les autres champs optionnels de l'API valent null. `niveau` était le
	// seul à faire exception — détecté par le test d'atteignabilité O1, qui
	// ne le trouvait dans aucune réponse.
	Niveau     *int    `json:"niveau"`
	Libelle    string  `json:"libelle"`
	LienAvisCT *string `json:"lien_avis_ct"`
}

// AvisSections regroupe les deux familles d'avis.
type AvisSections struct {
	SMR  []AvisView `json:"smr"`
	ASMR []AvisView `json:"asmr"`
}

// MembreView est une spécialité au sein d'un groupe générique.
type MembreView struct {
	CIS                   string `json:"cis"`
	Denomination          string `json:"denomination"`
	Type                  int    `json:"type"`
	TypeLibelle           string `json:"type_libelle"`
	Ordre                 int    `json:"ordre"`
	EtatCommercialisation string `json:"etat_commercialisation"`
	Link                  string `json:"_link"`
}

// GroupeView est un groupe générique dénormalisé.
type GroupeView struct {
	ID      string       `json:"id"`
	Libelle string       `json:"libelle"`
	Membres []MembreView `json:"membres"`
}

// RuptView est une fiche de disponibilité.
type RuptView struct {
	CIS                    string  `json:"cis"`
	CIP13                  *string `json:"cip13"`
	CodeStatut             int     `json:"code_statut"`
	Statut                 string  `json:"statut"`
	Libelle                string  `json:"libelle"`
	LibelleSource          string  `json:"libelle_source"`
	DateDebut              *string `json:"date_debut"`
	DateDebutApproximative bool    `json:"date_debut_approximative"`
	DateMiseAJour          *string `json:"date_mise_a_jour"`
	DateRemiseDisposition  *string `json:"date_remise_disposition"`
	LienANSM               string  `json:"lien_ansm"`
}

// MITMView est le statut de médicament d'intérêt thérapeutique majeur.
type MITMView struct {
	CodeATC      string `json:"code_atc"`
	Denomination string `json:"denomination,omitempty"`
	LienBDPM     string `json:"lien_bdpm,omitempty"`
}

// SubView est une substance du référentiel.
type SubView struct {
	Code          string    `json:"code"`
	Denomination  string    `json:"denomination"`
	NbSpecialites int       `json:"nb_specialites"`
	Links         *subLinks `json:"_links,omitempty"`
}

type subLinks struct {
	Medicaments string `json:"medicaments"`
}

// --- Conversions Store → vue ------------------------------------------------

func cisStr(cis uint32) string { return padLeft(strconv.FormatUint(uint64(cis), 10), 8) }

// padLeft restitue la forme canonique d'un code à longueur fixe. Le CIS est
// stocké en entier ; un code commençant par un zéro perdrait ce zéro sans
// ce remplissage, et ne correspondrait plus à ce que l'appelant a envoyé.
func padLeft(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return zeros[:n-len(s)] + s
}

const zeros = "0000000000000"

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func specListView(s *store.Store, id store.SpecID, score *float64) SpecListView {
	sp := s.Spec(id)
	cis := cisStr(sp.CIS)

	compos := s.Composants(id)
	subs := make([]string, 0, len(compos))
	seen := make(map[string]struct{}, len(compos))
	for i := range compos {
		d := compos[i].Denom
		if _, ok := seen[d]; ok || d == "" {
			continue
		}
		seen[d] = struct{}{}
		subs = append(subs, d)
	}

	return SpecListView{
		CIS:                   cis,
		Denomination:          sp.Denom,
		FormePharmaceutique:   s.Forme(sp),
		VoiesAdministration:   nonNil(s.Voies(sp)),
		StatutAMM:             s.StatutAMM(sp),
		EtatCommercialisation: s.EtatCommercialisation(sp),
		DateAMM:               strPtr(s.DateAMM(sp)),
		Titulaires:            nonNil(s.Titulaires(sp)),
		SurveillanceRenforcee: sp.Surveillance,
		Substances:            subs,
		NbPresentations:       len(s.Presentations(id)),
		MITM:                  len(s.MITM(id)) > 0,
		Score:                 score,
		Links: selfLink{
			Self:          "/v1/medicaments/" + cis,
			Presentations: "/v1/medicaments/" + cis + "/presentations",
		},
	}
}

func specView(s *store.Store, id store.SpecID) SpecView {
	sp := s.Spec(id)
	statutBdm, hasBdm := s.StatutBdm(sp)

	v := SpecView{
		CIS:                   cisStr(sp.CIS),
		Denomination:          sp.Denom,
		FormePharmaceutique:   s.Forme(sp),
		VoiesAdministration:   nonNil(s.Voies(sp)),
		StatutAMM:             s.StatutAMM(sp),
		ProcedureAMM:          s.ProcedureAMM(sp),
		EtatCommercialisation: s.EtatCommercialisation(sp),
		DateAMM:               strPtr(s.DateAMM(sp)),
		Titulaires:            nonNil(s.Titulaires(sp)),
		SurveillanceRenforcee: sp.Surveillance,
	}
	if hasBdm {
		v.StatutBdm = &statutBdm
	}
	v.NumeroAutorisationEuropeenne = strPtr(sp.NumEuro)
	if m := s.MITM(id); len(m) > 0 {
		v.MITM = &MITMView{CodeATC: m[0].CodeATC, Denomination: m[0].Denom, LienBDPM: m[0].Lien}
	}
	return v
}

func presView(s *store.Store, p *store.Pres) PresView {
	v := PresView{
		CIP13:                 padLeft(strconv.FormatUint(p.CIP13, 10), 13),
		CIP7:                  padLeft(strconv.FormatUint(uint64(p.CIP7), 10), 7),
		Libelle:               p.Libelle,
		StatutAdministratif:   s.StatutAdministratif(p),
		EtatCommercialisation: s.EtatCommercialisationPres(p),
		TauxRemboursement:     tauxView(p.TauxRemb),
	}
	if d := store.FormatDay(p.DateDeclaration); d != "" {
		v.DateDeclaration = &d
	}
	if prix, ok := p.PrixMedicament(); ok {
		v.PrixMedicamentCents = &prix
	}
	if prix, ok := p.PrixPublic(); ok {
		v.PrixPublicCents = &prix
	}
	if h, ok := p.Honoraires(); ok {
		v.HonorairesDispensationCents = &h
	}
	if agr, ok := p.Agrement(); ok {
		v.AgrementCollectivites = &agr
	}
	v.IndicationsRemboursement = strPtr(p.IndicationsRmb)
	return v
}

func compoView(s *store.Store, c *store.Compo) CompoView {
	return CompoView{
		ElementPharmaceutique: c.Element,
		CodeSubstance:         codeStr(c.Code),
		DenominationSubstance: c.Denom,
		Dosage:                c.Dosage,
		ReferenceDosage:       c.Reference,
		Nature:                s.NatureComposant(c),
		NumeroLiaison:         int(c.Liaison),
	}
}

func avisView(a *store.Avis) AvisView {
	v := AvisView{
		CodeDossierHAS:  a.Dossier,
		MotifEvaluation: a.Motif,
		Valeur:          a.Valeur,
		Libelle:         a.Libelle,
	}
	if d := store.FormatDay(a.DateAvis); d != "" {
		v.DateAvis = &d
	}
	if a.Niveau >= 0 {
		n := int(a.Niveau)
		v.Niveau = &n
	}
	v.LienAvisCT = strPtr(a.Lien)
	return v
}

func ruptView(s *store.Store, r *store.Rupt) RuptView {
	v := RuptView{
		CIS:                    cisStr(s.Spec(r.Spec).CIS),
		CodeStatut:             int(r.CodeStatut),
		Statut:                 r.Statut,
		Libelle:                r.Libelle,
		LibelleSource:          r.LibelleSource,
		DateDebutApproximative: r.DebutApprox,
		LienANSM:               r.Lien,
	}
	if !r.ToutLeProduit() && r.CIP13 != 0 {
		cip := padLeft(strconv.FormatUint(r.CIP13, 10), 13)
		v.CIP13 = &cip
	}
	if d := store.FormatDay(r.DateDebut); d != "" {
		v.DateDebut = &d
	}
	if d := store.FormatDay(r.DateMAJ); d != "" {
		v.DateMiseAJour = &d
	}
	if day, ok := r.DateRemiseDisposition(); ok {
		if d := store.FormatDay(day); d != "" {
			v.DateRemiseDisposition = &d
		}
	}
	return v
}

func groupeView(s *store.Store, g *store.Grp) GroupeView {
	membres := make([]MembreView, 0, len(g.Membres))
	for _, m := range g.Membres {
		sp := s.Spec(m.Spec)
		if sp == nil {
			continue
		}
		cis := cisStr(sp.CIS)
		membres = append(membres, MembreView{
			CIS:                   cis,
			Denomination:          sp.Denom,
			Type:                  int(m.Type),
			TypeLibelle:           typeGeneriqueLabel(int(m.Type)),
			Ordre:                 int(m.Ordre),
			EtatCommercialisation: s.EtatCommercialisation(sp),
			Link:                  "/v1/medicaments/" + cis,
		})
	}
	return GroupeView{ID: g.IDStr, Libelle: g.Libelle, Membres: membres}
}

func subView(sub *store.Sub, withLinks bool) SubView {
	v := SubView{
		Code:          sub.CodeStr,
		Denomination:  sub.Denom,
		NbSpecialites: int(sub.NbSpecs),
	}
	if withLinks {
		v.Links = &subLinks{Medicaments: "/v1/substances/" + sub.CodeStr + "/medicaments"}
	}
	return v
}

// typeGeneriqueLabel reproduit l'énumération discontinue de la source : la
// valeur 3 n'existe pas (docs/01-analyse-source-bdpm.md §3.7).
func typeGeneriqueLabel(t int) string {
	switch t {
	case 0:
		return "princeps"
	case 1:
		return "générique"
	case 2:
		return "générique par complémentarité posologique"
	case 4:
		return "générique substituable"
	default:
		return ""
	}
}

func codeStr(code uint32) string {
	if code == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(code), 10)
}

func tauxView(t []uint8) []int {
	out := make([]int, 0, len(t))
	for _, v := range t {
		out = append(out, int(v))
	}
	return out
}

// nonNil garantit qu'une liste vide se sérialise en `[]` et non en `null` :
// la distinction compte pour un intégrateur qui itère sans tester.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
