package store

import (
	"fmt"
	"sort"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
)

// Store est la représentation en mémoire, immuable, du jeu de données.
//
// Immuable au sens fort : aucune méthode ne modifie un champ après Build.
// C'est la condition de correction de la lecture concurrente sans verrou
// (docs/03-modele-de-donnees.md §4.6), et c'est aussi ce qui rend la bascule
// de version possible par simple écriture de pointeur.
type Store struct {
	// Métadonnées du snapshot chargé.
	Manifest *snapshot.Manifest
	// BuildDuration est le temps de construction, exposé en métrique.
	BuildDuration time.Duration

	specs []Spec
	subs  []Sub
	grps  []Grp

	// Relations 1-N, indexées par SpecID.
	presentations *CSR[Pres]
	composants    *CSR[Compo]
	avisSMR       *CSR[Avis]
	avisASMR      *CSR[Avis]
	conditions    *CSR[Cond]
	ruptures      *CSR[Rupt]
	mitm          *CSR[MITM]

	// presAll est la liste plate des présentations, dans l'ordre du CSR :
	// c'est elle que PresID indexe, de sorte qu'un accès par CIP13 ne
	// nécessite pas de repasser par la spécialité.
	presAll []Pres

	// Index d'accès direct (docs/03-modele-de-donnees.md §4.3).
	byCIS        map[uint32]SpecID
	byCIP13      map[uint64]PresID
	byCIP7       map[uint32]PresID
	bySubstance  map[uint32]SubID
	byGroupe     map[uint32]GrpID
	specToGroupe map[SpecID]GrpID

	// Tables d'internement des champs à faible cardinalité.
	formes     *Interner
	voies      *Interner
	titulaires *Interner

	enumStatutAMM   *enumTable
	enumProcedure   *enumTable
	enumEtatCommerc *enumTable
	enumStatutBdm   *enumTable
	enumStatutAdmin *enumTable
	enumEtatPres    *enumTable
	enumNature      *enumTable

	// Index de recherche (lot 3), construit à la suite.
	search *SearchIndex
}

// Build convertit un Dataset relu depuis un snapshot en Store.
//
// Le coût est payé une fois, hors du chemin de requête. L'ordre des étapes
// suit les dépendances : les spécialités d'abord, puisque tout s'y rattache,
// puis les entités dérivées, puis les relations en CSR, puis les index.
func Build(d *bdpm.Dataset, m *snapshot.Manifest) (*Store, error) {
	start := time.Now()

	s := &Store{
		Manifest:        m,
		formes:          NewInterner("forme_pharmaceutique"),
		voies:           NewInterner("voie_administration"),
		titulaires:      NewInterner("titulaire"),
		enumStatutAMM:   newEnumTable("statut_amm"),
		enumProcedure:   newEnumTable("procedure_amm"),
		enumEtatCommerc: newEnumTable("etat_commercialisation"),
		enumStatutBdm:   newEnumTable("statut_bdm"),
		enumStatutAdmin: newEnumTable("statut_administratif"),
		enumEtatPres:    newEnumTable("etat_commercialisation_presentation"),
		enumNature:      newEnumTable("nature_composant"),
	}

	if err := s.buildSpecs(d); err != nil {
		return nil, err
	}
	s.buildSubstances(d)
	s.buildGroupes(d)
	if err := s.buildRelations(d); err != nil {
		return nil, err
	}
	// Les tables d'internement sont vérifiées avant publication : un
	// débordement corromprait tous les libellés du champ concerné.
	for _, in := range []*Interner{s.formes, s.voies, s.titulaires} {
		if err := in.Err(); err != nil {
			return nil, err
		}
	}
	s.freeze()

	s.BuildDuration = time.Since(start)
	return s, nil
}

func (s *Store) buildSpecs(d *bdpm.Dataset) error {
	s.specs = make([]Spec, 0, len(d.Specialites))
	s.byCIS = make(map[uint32]SpecID, len(d.Specialites))

	for _, sp := range d.Specialites {
		cis, ok := parseUint32(sp.CIS)
		if !ok {
			return fmt.Errorf("CIS non numérique ou hors bornes dans le snapshot : %q", sp.CIS)
		}

		statutAMM, err := s.enumStatutAMM.intern(sp.StatutAMM)
		if err != nil {
			return err
		}
		procedure, err := s.enumProcedure.intern(sp.ProcedureAMM)
		if err != nil {
			return err
		}
		etat, err := s.enumEtatCommerc.intern(sp.EtatCommercialisation)
		if err != nil {
			return err
		}
		var bdm uint8
		if sp.StatutBdm != nil {
			if bdm, err = s.enumStatutBdm.intern(*sp.StatutBdm); err != nil {
				return err
			}
		}

		var numEuro string
		if sp.NumeroAutorisationEuropeenne != nil {
			numEuro = *sp.NumeroAutorisationEuropeenne
		}

		// Index dans une slice qu'on vient de dimensionner sur le nombre de
		// spécialités du snapshot, lui-même plafonné par la validation
		// d'ingestion. Un dépassement d'int32 supposerait deux milliards de
		// médicaments.
		id := SpecID(len(s.specs)) //nolint:gosec // index borné par la taille du snapshot validé
		s.specs = append(s.specs, Spec{
			Denom:        sp.Denomination,
			DenomNorm:    Normalize(sp.Denomination),
			NumEuro:      numEuro,
			Voies:        s.voies.internAll(sp.VoiesAdministration),
			Titulaires:   s.titulaires.internAll(sp.Titulaires),
			CIS:          cis,
			DateAMM:      parseDay(sp.DateAMM),
			Forme:        s.formes.Intern(sp.FormePharmaceutique),
			StatutAMM:    statutAMM,
			Procedure:    procedure,
			EtatCommerc:  etat,
			StatutBdm:    bdm,
			Surveillance: sp.SurveillanceRenforcee,
		})
		s.byCIS[cis] = id
	}
	return nil
}

func (s *Store) buildSubstances(d *bdpm.Dataset) {
	s.subs = make([]Sub, 0, len(d.Substances))
	s.bySubstance = make(map[uint32]SubID, len(d.Substances))

	for _, sub := range d.Substances {
		code, ok := parseUint32(sub.Code)
		if !ok {
			// Un code de substance non numérique n'a jamais été observé,
			// mais il resterait consultable par sa forme textuelle : on
			// conserve l'entrée sans l'indexer plutôt que de la perdre.
			s.subs = append(s.subs, Sub{
				Denom: sub.Denomination, DenomNorm: Normalize(sub.Denomination),
				CodeStr: sub.Code, NbSpecs: toInt32(sub.NbSpecialites),
			})
			continue
		}
		id := SubID(len(s.subs)) //nolint:gosec // index borné par la taille du snapshot validé
		s.subs = append(s.subs, Sub{
			Denom:     sub.Denomination,
			DenomNorm: Normalize(sub.Denomination),
			CodeStr:   sub.Code,
			Code:      code,
			NbSpecs:   toInt32(sub.NbSpecialites),
		})
		s.bySubstance[code] = id
	}
}

func (s *Store) buildGroupes(d *bdpm.Dataset) {
	s.grps = make([]Grp, 0, len(d.Groupes))
	s.byGroupe = make(map[uint32]GrpID, len(d.Groupes))
	s.specToGroupe = make(map[SpecID]GrpID, len(d.Groupes)*4)

	for _, g := range d.Groupes {
		membres := make([]Membre, 0, len(g.Membres))
		for _, mb := range g.Membres {
			cis, ok := parseUint32(mb.CIS)
			if !ok {
				continue
			}
			sid, ok := s.byCIS[cis]
			if !ok {
				continue
			}
			membres = append(membres, Membre{
				Spec: sid, Type: toUint8(mb.Type), Ordre: toInt32(mb.Ordre),
			})
		}

		id := GrpID(len(s.grps)) //nolint:gosec // index borné par la taille du snapshot validé
		gid, numeric := parseUint32(g.ID)
		s.grps = append(s.grps, Grp{
			Libelle:     g.Libelle,
			LibelleNorm: Normalize(g.Libelle),
			IDStr:       g.ID,
			ID:          gid,
			Membres:     membres,
		})
		if numeric {
			s.byGroupe[gid] = id
		}
		for _, mb := range membres {
			s.specToGroupe[mb.Spec] = id
		}
	}
}

func (s *Store) buildRelations(d *bdpm.Dataset) error {
	n := len(s.specs)

	// Présentations. Le CSR réordonne les valeurs par spécialité ; les index
	// par CIP sont donc construits **après**, sur la liste réordonnée, pour
	// que PresID désigne bien une position dans presAll.
	pres := make([]Pres, 0, len(d.Presentations))
	for _, p := range d.Presentations {
		sid, ok := s.specIDOf(p.CIS)
		if !ok {
			continue
		}
		cip13, _ := parseUint(p.CIP13)
		cip7, _ := parseUint32(p.CIP7)

		statut, err := s.enumStatutAdmin.intern(p.StatutAdministratif)
		if err != nil {
			return err
		}
		etat, err := s.enumEtatPres.intern(p.EtatCommercialisation)
		if err != nil {
			return err
		}

		item := Pres{
			Libelle:         p.Libelle,
			CIP13:           cip13,
			Spec:            sid,
			CIP7:            cip7,
			DateDeclaration: parseDay(p.DateDeclaration),
			StatutAdmin:     statut,
			EtatCommerc:     etat,
			TauxRemb:        tauxToBytes(p.TauxRemboursement),
		}
		if p.PrixMedicamentCents != nil {
			item.PrixMedicamentCents = *p.PrixMedicamentCents
			item.present |= presPrixMedicament
		}
		if p.PrixPublicCents != nil {
			item.PrixPublicCents = *p.PrixPublicCents
			item.present |= presPrixPublic
		}
		if p.HonorairesDispensationCents != nil {
			item.HonorairesDispensationCents = *p.HonorairesDispensationCents
			item.present |= presHonoraires
		}
		if p.AgrementCollectivites != nil {
			item.present |= presAgrement
			if *p.AgrementCollectivites {
				item.AgrementColl = 1
			}
		}
		if p.IndicationsRemboursement != nil {
			item.IndicationsRmb = *p.IndicationsRemboursement
		}
		pres = append(pres, item)
	}

	s.presentations = BuildCSR(n, pres, func(p Pres) int32 { return int32(p.Spec) })
	s.presAll = s.presentations.All()

	s.byCIP13 = make(map[uint64]PresID, len(s.presAll))
	s.byCIP7 = make(map[uint32]PresID, len(s.presAll))
	for i := range s.presAll {
		if s.presAll[i].CIP13 != 0 {
			s.byCIP13[s.presAll[i].CIP13] = PresID(i)
		}
		if s.presAll[i].CIP7 != 0 {
			s.byCIP7[s.presAll[i].CIP7] = PresID(i)
		}
	}

	compos := make([]Compo, 0, len(d.Composants))
	for _, c := range d.Composants {
		sid, ok := s.specIDOf(c.CIS)
		if !ok {
			continue
		}
		nature, err := s.enumNature.intern(c.Nature)
		if err != nil {
			return err
		}
		code, _ := parseUint32(c.CodeSubstance)
		sub := SubID(InvalidID)
		if id, ok := s.bySubstance[code]; ok {
			sub = id
		}
		compos = append(compos, Compo{
			Element:   c.ElementPharmaceutique,
			Denom:     c.DenominationSubstance,
			DenomNorm: Normalize(c.DenominationSubstance),
			Dosage:    c.Dosage,
			Reference: c.ReferenceDosage,
			Spec:      sid,
			Sub:       sub,
			Code:      code,
			Liaison:   toInt32(c.NumeroLiaison),
			Nature:    nature,
		})
	}
	s.composants = BuildCSR(n, compos, func(c Compo) int32 { return int32(c.Spec) })

	smr := make([]Avis, 0, len(d.AvisSMR))
	for _, a := range d.AvisSMR {
		if sid, ok := s.specIDOf(a.CIS); ok {
			smr = append(smr, Avis{
				Motif: a.MotifEvaluation, Valeur: a.Valeur, Libelle: a.Libelle,
				Lien: deref(a.LienAvisCT), Dossier: a.CodeDossierHAS,
				Spec: sid, DateAvis: parseDay(a.DateAvis), Niveau: -1,
			})
		}
	}
	s.avisSMR = BuildCSR(n, smr, func(a Avis) int32 { return int32(a.Spec) })

	asmr := make([]Avis, 0, len(d.AvisASMR))
	for _, a := range d.AvisASMR {
		if sid, ok := s.specIDOf(a.CIS); ok {
			niveau := int8(-1)
			if a.Niveau != nil {
				// toUint8 a déjà ramené la valeur dans [0, 255] ; les niveaux
				// ASMR valent I à V, soit 1 à 5.
				niveau = int8(toUint8(*a.Niveau)) //nolint:gosec // borné par toUint8 juste au-dessus
			}
			asmr = append(asmr, Avis{
				Motif: a.MotifEvaluation, Valeur: a.Valeur, Libelle: a.Libelle,
				Lien: deref(a.LienAvisCT), Dossier: a.CodeDossierHAS,
				Spec: sid, DateAvis: parseDay(a.DateAvis), Niveau: niveau,
			})
		}
	}
	s.avisASMR = BuildCSR(n, asmr, func(a Avis) int32 { return int32(a.Spec) })

	conds := make([]Cond, 0, len(d.Conditions))
	for _, c := range d.Conditions {
		if sid, ok := s.specIDOf(c.CIS); ok {
			conds = append(conds, Cond{Texte: c.Condition, Spec: sid})
		}
	}
	s.conditions = BuildCSR(n, conds, func(c Cond) int32 { return int32(c.Spec) })

	rupts := make([]Rupt, 0, len(d.Ruptures))
	for _, r := range d.Ruptures {
		sid, ok := s.specIDOf(r.CIS)
		if !ok {
			continue
		}
		item := Rupt{
			Statut: r.Statut, Libelle: r.Libelle, LibelleSource: r.LibelleSource,
			Lien: r.LienANSM, Spec: sid, Pres: InvalidID,
			DateDebut: parseDay(r.DateDebut), DateMAJ: parseDay(r.DateMiseAJour),
			CodeStatut: toUint8(r.CodeStatut), DebutApprox: r.DateDebutApproximative,
			toutLeProduit: r.CIP13 == nil,
		}
		if r.CIP13 != nil {
			if cip, ok := parseUint(*r.CIP13); ok {
				item.CIP13 = cip
				if pid, ok := s.byCIP13[cip]; ok {
					item.Pres = pid
				}
			}
		}
		item.DateRemise, item.hasRemise = parseDayPtr(r.DateRemiseDisposition)
		rupts = append(rupts, item)
	}
	s.ruptures = BuildCSR(n, rupts, func(r Rupt) int32 { return int32(r.Spec) })

	mitms := make([]MITM, 0, len(d.MITM))
	for _, m := range d.MITM {
		if sid, ok := s.specIDOf(m.CIS); ok {
			mitms = append(mitms, MITM{
				CodeATC: m.CodeATC, Denom: m.Denomination, Lien: m.LienBDPM, Spec: sid,
			})
		}
	}
	s.mitm = BuildCSR(n, mitms, func(m MITM) int32 { return int32(m.Spec) })

	return nil
}

// freeze libère les structures de construction et interdit toute mutation
// ultérieure des tables d'internement.
func (s *Store) freeze() {
	s.formes.Freeze()
	s.voies.Freeze()
	s.titulaires.FreezeNormalized()
	for _, e := range []*enumTable{
		s.enumStatutAMM, s.enumProcedure, s.enumEtatCommerc, s.enumStatutBdm,
		s.enumStatutAdmin, s.enumEtatPres, s.enumNature,
	} {
		e.freeze()
	}
}

func (s *Store) specIDOf(cis string) (SpecID, bool) {
	n, ok := parseUint32(cis)
	if !ok {
		return 0, false
	}
	id, ok := s.byCIS[n]
	return id, ok
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// tauxToBytes compacte les taux de remboursement. Les valeurs observées
// sont 0, 15, 30, 65 et 100 : un uint8 suffit, et un taux hors de [0, 100]
// est écarté plutôt que tronqué silencieusement.
func tauxToBytes(taux []int) []uint8 {
	if len(taux) == 0 {
		return nil
	}
	out := make([]uint8, 0, len(taux))
	for _, t := range taux {
		if t < 0 || t > 100 {
			continue
		}
		out = append(out, uint8(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LoadStore lit un snapshot depuis le disque et construit le Store
// correspondant.
func LoadStore(dir string) (*Store, error) {
	d, m, err := snapshot.Load(dir)
	if err != nil {
		return nil, err
	}
	s, err := Build(d, m)
	if err != nil {
		return nil, err
	}
	s.BuildSearchIndex()
	return s, nil
}
