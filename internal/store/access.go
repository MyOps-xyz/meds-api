package store

import "sync/atomic"

// Toutes les méthodes de ce fichier ont un récepteur pointeur en **lecture
// seule** : aucune ne modifie le Store. Les slices retournées pointent dans
// son stockage et ne doivent jamais être modifiées ni triées en place par un
// appelant (docs/03-modele-de-donnees.md §4.6).

// NbSpecialites est le nombre de spécialités du jeu.
func (s *Store) NbSpecialites() int { return len(s.specs) }

// NbPresentations est le nombre de présentations.
func (s *Store) NbPresentations() int { return len(s.presAll) }

// NbSubstances est le nombre de substances du référentiel dérivé.
func (s *Store) NbSubstances() int { return len(s.subs) }

// NbGroupes est le nombre de groupes génériques servis.
func (s *Store) NbGroupes() int { return len(s.grps) }

// SpecByCIS résout un code CIS. Un code inconnu retourne InvalidID sans
// panique : les codes viennent d'une URL publique.
func (s *Store) SpecByCIS(cis string) (SpecID, bool) {
	n, ok := parseUint32(cis)
	if !ok {
		return InvalidID, false
	}
	id, ok := s.byCIS[n]
	if !ok {
		return InvalidID, false
	}
	return id, true
}

// PresByCIP résout un code CIP7 ou CIP13 indifféremment.
//
// La longueur discrimine sans ambiguïté : 7 ou 13 chiffres. Les deux mènent
// à la même présentation, ce qui évite à un intégrateur de savoir laquelle
// des deux formes il détient.
func (s *Store) PresByCIP(cip string) (PresID, bool) {
	n, ok := parseUint(cip)
	if !ok {
		return InvalidID, false
	}
	switch len(cip) {
	case 13:
		if id, ok := s.byCIP13[n]; ok {
			return id, true
		}
	case 7:
		if n7, ok := toUint32(n); ok {
			if id, ok := s.byCIP7[n7]; ok {
				return id, true
			}
		}
	}
	return InvalidID, false
}

// SubByCode résout un code de substance.
func (s *Store) SubByCode(code string) (SubID, bool) {
	n, ok := parseUint32(code)
	if !ok {
		return InvalidID, false
	}
	id, ok := s.bySubstance[n]
	if !ok {
		return InvalidID, false
	}
	return id, true
}

// GrpByID résout un identifiant de groupe générique.
func (s *Store) GrpByID(id string) (GrpID, bool) {
	n, ok := parseUint32(id)
	if !ok {
		return InvalidID, false
	}
	gid, ok := s.byGroupe[n]
	if !ok {
		return InvalidID, false
	}
	return gid, true
}

// Spec retourne une spécialité par sa position.
func (s *Store) Spec(id SpecID) *Spec {
	if id < 0 || int(id) >= len(s.specs) {
		return nil
	}
	return &s.specs[id]
}

// Sub retourne une substance par sa position.
func (s *Store) Sub(id SubID) *Sub {
	if id < 0 || int(id) >= len(s.subs) {
		return nil
	}
	return &s.subs[id]
}

// Grp retourne un groupe générique par sa position.
func (s *Store) Grp(id GrpID) *Grp {
	if id < 0 || int(id) >= len(s.grps) {
		return nil
	}
	return &s.grps[id]
}

// Pres retourne une présentation par sa position.
func (s *Store) Pres(id PresID) *Pres {
	if id < 0 || int(id) >= len(s.presAll) {
		return nil
	}
	return &s.presAll[id]
}

// Presentations, Composants, AvisSMR, AvisASMR, Conditions, Ruptures et
// MITM sont les relations 1-N d'une spécialité. Chacune retourne une slice
// vide non nulle si la spécialité n'a pas d'enfant.
func (s *Store) Presentations(id SpecID) []Pres { return s.presentations.Get(int32(id)) }

// Composants retourne la composition d'une spécialité.
func (s *Store) Composants(id SpecID) []Compo { return s.composants.Get(int32(id)) }

// AvisSMR retourne les avis de service médical rendu.
func (s *Store) AvisSMR(id SpecID) []Avis { return s.avisSMR.Get(int32(id)) }

// AvisASMR retourne les avis d'amélioration du service médical rendu.
func (s *Store) AvisASMR(id SpecID) []Avis { return s.avisASMR.Get(int32(id)) }

// Conditions retourne les conditions de prescription et de délivrance.
func (s *Store) Conditions(id SpecID) []Cond { return s.conditions.Get(int32(id)) }

// Ruptures retourne les ruptures et tensions d'approvisionnement.
func (s *Store) Ruptures(id SpecID) []Rupt { return s.ruptures.Get(int32(id)) }

// MITM retourne le statut de médicament d'intérêt thérapeutique majeur.
func (s *Store) MITM(id SpecID) []MITM { return s.mitm.Get(int32(id)) }

// GroupeDe retourne le groupe générique d'une spécialité, s'il existe.
func (s *Store) GroupeDe(id SpecID) (GrpID, bool) {
	g, ok := s.specToGroupe[id]
	return g, ok
}

// AllSpecs expose les spécialités pour un parcours complet (filtres,
// listings). La slice ne doit pas être modifiée.
func (s *Store) AllSpecs() []Spec { return s.specs }

// AllSubs expose le référentiel des substances.
func (s *Store) AllSubs() []Sub { return s.subs }

// AllGrps expose les groupes génériques.
func (s *Store) AllGrps() []Grp { return s.grps }

// AllRuptures expose toutes les ruptures, tous médicaments confondus.
func (s *Store) AllRuptures() []Rupt { return s.ruptures.All() }

// AllMITM expose tous les médicaments d'intérêt thérapeutique majeur.
func (s *Store) AllMITM() []MITM { return s.mitm.All() }

// Résolution des champs internés et énumérés, appliquée à la sérialisation.

// Forme résout la forme pharmaceutique d'une spécialité.
func (s *Store) Forme(sp *Spec) string { return s.formes.Resolve(sp.Forme) }

// Voies résout les voies d'administration.
func (s *Store) Voies(sp *Spec) []string { return s.voies.resolveAll(sp.Voies) }

// Titulaires résout les titulaires de l'AMM.
func (s *Store) Titulaires(sp *Spec) []string { return s.titulaires.resolveAll(sp.Titulaires) }

// VoieMatches teste une voie d'administration à la casse près, sans allouer.
// Destiné au filtrage, là où Voies sert la sérialisation.
func (s *Store) VoieMatches(sp *Spec, needle string) bool {
	return s.voies.AnyEqualFold(sp.Voies, needle)
}

// TitulaireMatches teste un titulaire par sous-chaîne normalisée, sans
// allouer. needle doit déjà être normalisé par l'appelant.
func (s *Store) TitulaireMatches(sp *Spec, needle string) bool {
	return s.titulaires.AnyNormalizedContains(sp.Titulaires, needle)
}

// StatutAMM résout le statut de l'AMM.
func (s *Store) StatutAMM(sp *Spec) string { return s.enumStatutAMM.resolve(sp.StatutAMM) }

// ProcedureAMM résout la procédure d'AMM.
func (s *Store) ProcedureAMM(sp *Spec) string { return s.enumProcedure.resolve(sp.Procedure) }

// EtatCommercialisation résout l'état de commercialisation d'une spécialité.
func (s *Store) EtatCommercialisation(sp *Spec) string {
	return s.enumEtatCommerc.resolve(sp.EtatCommerc)
}

// StatutBdm résout le statut BdM, absent pour la majorité des spécialités.
func (s *Store) StatutBdm(sp *Spec) (string, bool) {
	v := s.enumStatutBdm.resolve(sp.StatutBdm)
	return v, v != ""
}

// DateAMM résout la date d'AMM en ISO 8601.
func (s *Store) DateAMM(sp *Spec) string { return FormatDay(sp.DateAMM) }

// StatutAdministratif résout le statut administratif d'une présentation.
func (s *Store) StatutAdministratif(p *Pres) string { return s.enumStatutAdmin.resolve(p.StatutAdmin) }

// EtatCommercialisationPres résout l'état de commercialisation d'une présentation.
func (s *Store) EtatCommercialisationPres(p *Pres) string {
	return s.enumEtatPres.resolve(p.EtatCommerc)
}

// NatureComposant résout la nature d'un composant (SA ou FT).
func (s *Store) NatureComposant(c *Compo) string { return s.enumNature.resolve(c.Nature) }

// FormeCount, VoieCount et TitulaireCount exposent la cardinalité des tables
// d'internement, pour les métriques et les tests de gain mémoire.
func (s *Store) FormeCount() int { return s.formes.Len() }

// VoieCount est le nombre de voies d'administration distinctes.
func (s *Store) VoieCount() int { return s.voies.Len() }

// TitulaireCount est le nombre de titulaires distincts.
func (s *Store) TitulaireCount() int { return s.titulaires.Len() }

// Holder porte le Store en service et permet sa bascule atomique
// (docs/09-pipeline-mise-a-jour.md §3.8).
//
// La bascule est une **écriture de pointeur** : elle ne peut ni échouer, ni
// bloquer, ni être observée à moitié. Les requêtes en cours terminent sur
// l'ancien Store, que le ramasse-miettes libère ensuite. C'est tout le
// mécanisme de mise à jour sans coupure — il n'y a ni verrou, ni double
// tampon, ni drainage de connexions.
type Holder struct {
	ptr atomic.Pointer[Store]
}

// NewHolder construit un porteur vide. Load y retourne nil jusqu'à la
// première publication : c'est l'état d'un démarrage à froid, pendant lequel
// /readyz répond 503.
func NewHolder() *Holder { return &Holder{} }

// Load retourne le Store en service, ou nil si aucun n'est publié.
//
// Un handler doit appeler Load **une seule fois** par requête et travailler
// sur le pointeur obtenu : deux appels successifs pourraient encadrer une
// bascule et produire une réponse mêlant deux versions du jeu de données.
func (h *Holder) Load() *Store { return h.ptr.Load() }

// Store publie un nouveau Store. L'ancien reste servi aux requêtes en cours.
func (h *Holder) Store(s *Store) { h.ptr.Store(s) }

// Ready indique qu'un Store est publié.
func (h *Holder) Ready() bool { return h.ptr.Load() != nil }
