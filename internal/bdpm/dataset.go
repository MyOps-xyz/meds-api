package bdpm

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Noms canoniques des dix fichiers ingérés. Ils servent de clé partout où
// un fichier est désigné : quarantaine, manifest, rapport d'ingestion.
const (
	FileSpecialites   = "CIS_bdpm.txt"
	FilePresentations = "CIS_CIP_bdpm.txt"
	FileComposants    = "CIS_COMPO_bdpm.txt"
	FileAvisSMR       = "CIS_HAS_SMR_bdpm.txt"
	FileAvisASMR      = "CIS_HAS_ASMR_bdpm.txt"
	FileLiensCT       = "HAS_LiensPageCT_bdpm.txt"
	FileGroupes       = "CIS_GENER_bdpm.txt"
	FileConditions    = "CIS_CPD_bdpm.txt"
	FileRuptures      = "CIS_CIP_Dispo_Spec.txt"
	FileMITM          = "CIS_MITM.txt"
)

// Substance est une entité dérivée (docs/03-modele-de-donnees.md §3.4) :
// elle n'existe pas dans la source et résulte de l'agrégation de
// CIS_COMPO_bdpm.txt par code de substance.
type Substance struct {
	Code          string `json:"code"`
	Denomination  string `json:"denomination"`
	NbSpecialites int    `json:"nb_specialites"`
}

// MembreGroupe est une spécialité au sein d'un groupe générique.
type MembreGroupe struct {
	CIS         string `json:"cis"`
	Type        int    `json:"type"`
	TypeLibelle string `json:"type_libelle"`
	Ordre       int    `json:"ordre"`
}

// GroupeGenerique est un groupe dénormalisé avec ses membres
// (docs/03-modele-de-donnees.md §3.5).
type GroupeGenerique struct {
	ID      string         `json:"id"`
	Libelle string         `json:"libelle"`
	Membres []MembreGroupe `json:"membres"`
}

// SourceFile décrit un fichier source tel qu'il a été ingéré. Ces
// métadonnées alimentent le manifest (docs/03-modele-de-donnees.md §3.8).
type SourceFile struct {
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
	Lines      int    `json:"lines"`
	Encoding   string `json:"encoding"`
	LineEnding string `json:"line_ending"`
}

// Dataset est le jeu de données complet issu d'une ingestion : les entités
// directement parsées, puis les entités dérivées construites par Derive.
type Dataset struct {
	Specialites   []Specialite
	Presentations []Presentation
	Composants    []Composant
	AvisSMR       []AvisSMR
	AvisASMR      []AvisASMR
	LiensCT       []LienAvisCT
	Appartenances []GroupeAppartenance
	Conditions    []Condition
	Ruptures      []Rupture
	MITM          []InfoMITM

	// Entités dérivées (T-10), vides tant que Derive n'a pas été appelée.
	Substances []Substance
	Groupes    []GroupeGenerique

	// SourceFiles décrit les fichiers ingérés, dans l'ordre canonique.
	SourceFiles []SourceFile
}

// Counts restitue le nombre d'enregistrements par entité, pour le manifest
// et le rapport d'ingestion.
func (d *Dataset) Counts() map[string]int {
	return map[string]int{
		"specialites":        len(d.Specialites),
		"presentations":      len(d.Presentations),
		"composants":         len(d.Composants),
		"substances":         len(d.Substances),
		"groupes_generiques": len(d.Groupes),
		"avis_smr":           len(d.AvisSMR),
		"avis_asmr":          len(d.AvisASMR),
		"conditions":         len(d.Conditions),
		"ruptures":           len(d.Ruptures),
		"mitm":               len(d.MITM),
	}
}

// TotalRecords est le nombre total d'enregistrements retenus, dérivés
// exclus : c'est le dénominateur du taux de rejet.
func (d *Dataset) TotalRecords() int {
	return len(d.Specialites) + len(d.Presentations) + len(d.Composants) +
		len(d.AvisSMR) + len(d.AvisASMR) + len(d.LiensCT) +
		len(d.Appartenances) + len(d.Conditions) + len(d.Ruptures) + len(d.MITM)
}

// ParseAll décode puis parse les dix fichiers d'un DatasetResult.
//
// Le décodage d'encodage (T-06) a lieu ici, par fichier et à chaque
// synchronisation : un fichier qui bascule d'un encodage à l'autre entre
// deux ingestions est traité sans intervention (piège P1).
//
// Un fichier absent du résultat est une erreur : ingérer un jeu partiel
// produirait un snapshot silencieusement amputé, très exactement ce que la
// validation de vraisemblance cherche à empêcher.
func ParseAll(res DatasetResult, q *Quarantine) (*Dataset, error) {
	byName := make(map[string]FileResult, len(res.Files))
	for _, f := range res.Files {
		byName[f.Name] = f
	}

	ds := &Dataset{}
	order := []string{
		FileSpecialites, FilePresentations, FileComposants,
		FileAvisSMR, FileAvisASMR, FileLiensCT,
		FileGroupes, FileConditions, FileRuptures, FileMITM,
	}

	for _, name := range order {
		fr, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("fichier absent du jeu téléchargé : %s", name)
		}
		text, enc, err := DecodeFile(fr.Bytes)
		if err != nil {
			return nil, fmt.Errorf("décodage de %s : %w", name, err)
		}

		if err := ds.parseOne(name, text, q); err != nil {
			return nil, err
		}

		ds.SourceFiles = append(ds.SourceFiles, SourceFile{
			Name:       name,
			SHA256:     fr.SHA256,
			Bytes:      len(fr.Bytes),
			Lines:      countLines(fr.Bytes),
			Encoding:   enc,
			LineEnding: detectLineEnding(fr.Bytes),
		})
	}
	return ds, nil
}

func (d *Dataset) parseOne(name, text string, q *Quarantine) error {
	// strings.NewReader et non bytes.NewReader([]byte(text)) : la conversion
	// recopierait intégralement le fichier déjà matérialisé par DecodeFile.
	r := strings.NewReader(text)
	var err error

	switch name {
	case FileSpecialites:
		d.Specialites, err = ParseSpecialites(name, r, q.Add, q.AddUnknown)
	case FilePresentations:
		d.Presentations, err = ParsePresentations(name, r, q.Add, q.AddUnknown)
	case FileComposants:
		d.Composants, err = ParseComposants(name, r, q.Add)
	case FileAvisSMR:
		d.AvisSMR, err = ParseAvisSMR(name, r, q.Add)
	case FileAvisASMR:
		d.AvisASMR, err = ParseAvisASMR(name, r, q.Add)
	case FileLiensCT:
		d.LiensCT, err = ParseLiensCT(name, r, q.Add)
	case FileGroupes:
		d.Appartenances, err = ParseGroupes(name, r, q.Add)
	case FileConditions:
		d.Conditions, err = ParseConditions(name, r, q.Add)
	case FileRuptures:
		d.Ruptures, err = ParseRuptures(name, r, q.Add)
	case FileMITM:
		d.MITM, err = ParseMITM(name, r, q.Add)
	default:
		return fmt.Errorf("fichier inattendu : %s", name)
	}
	if err != nil {
		return fmt.Errorf("parsing de %s : %w", name, err)
	}
	return nil
}

// countLines compte les enregistrements bruts d'un fichier source : les
// sauts de ligne, plus un si le fichier ne se termine pas par un saut
// (piège P7, CIS_MITM.txt), moins les lignes vides finales (piège P7,
// CIS_CPD_bdpm.txt).
func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	trimmed := bytes.TrimRight(b, "\r\n \t")
	if len(trimmed) == 0 {
		return 0
	}
	return bytes.Count(trimmed, []byte("\n")) + 1
}

// detectLineEnding rapporte la terminaison majoritaire du fichier. La
// distinction est purement informative — le Scanner traite les deux — mais
// un basculement amont mérite d'être visible dans le manifest.
func detectLineEnding(b []byte) string {
	lf := bytes.Count(b, []byte("\n"))
	if lf == 0 {
		return "none"
	}
	crlf := bytes.Count(b, []byte("\r\n"))
	switch crlf {
	case 0:
		return "lf"
	case lf:
		return "crlf"
	default:
		return "mixed"
	}
}

// Derive construit les entités dérivées de T-10 : le référentiel des
// substances, la dénormalisation des groupes génériques et la résolution
// des liens HAS dans les avis.
//
// L'appel est idempotent : rejouer Derive sur un même Dataset produit le
// même résultat.
func (d *Dataset) Derive() {
	d.deriveSubstances()
	d.deriveGroupes()
	d.resolveLiensCT()
}

// deriveSubstances agrège les composants par code de substance.
//
// Le nombre de spécialités est un décompte de CIS **distincts** : une même
// substance apparaît plusieurs fois pour une même spécialité dès qu'elle
// figure dans plusieurs éléments pharmaceutiques (gélule et poudre d'un
// même produit), et la compter deux fois surestimerait le référentiel.
//
// La dénomination retenue est la première rencontrée dans l'ordre des CIS
// croissants : la source en donne parfois plusieurs graphies pour un même
// code, et un choix stable et reproductible vaut mieux qu'un choix arbitraire
// dépendant de l'ordre du fichier.
func (d *Dataset) deriveSubstances() {
	type agg struct {
		denominations map[string]struct{}
		cis           map[string]struct{}
		firstCIS      string
		firstDenom    string
	}
	byCode := make(map[string]*agg)

	for _, c := range d.Composants {
		if c.CodeSubstance == "" {
			continue
		}
		a, ok := byCode[c.CodeSubstance]
		if !ok {
			a = &agg{
				denominations: make(map[string]struct{}),
				cis:           make(map[string]struct{}),
				firstCIS:      c.CIS,
				firstDenom:    c.DenominationSubstance,
			}
			byCode[c.CodeSubstance] = a
		}
		a.denominations[c.DenominationSubstance] = struct{}{}
		a.cis[c.CIS] = struct{}{}
		if c.CIS < a.firstCIS || (c.CIS == a.firstCIS && c.DenominationSubstance < a.firstDenom) {
			a.firstCIS = c.CIS
			a.firstDenom = c.DenominationSubstance
		}
	}

	out := make([]Substance, 0, len(byCode))
	for code, a := range byCode {
		out = append(out, Substance{
			Code:          code,
			Denomination:  a.firstDenom,
			NbSpecialites: len(a.cis),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	d.Substances = out
}

// deriveGroupes dénormalise les appartenances en groupes portant leurs
// membres, triés par ordre puis par CIS — l'ordre de la source est
// respecté quand il est renseigné, et le CIS départage les ex æquo pour
// que la sortie soit reproductible octet pour octet (exigence de T-11).
func (d *Dataset) deriveGroupes() {
	type agg struct {
		libelle string
		membres []MembreGroupe
	}
	byID := make(map[string]*agg)
	ids := make([]string, 0)

	for _, a := range d.Appartenances {
		g, ok := byID[a.GroupeID]
		if !ok {
			g = &agg{libelle: a.Libelle}
			byID[a.GroupeID] = g
			ids = append(ids, a.GroupeID)
		}
		g.membres = append(g.membres, MembreGroupe{
			CIS:         a.CIS,
			Type:        a.Type,
			TypeLibelle: a.TypeLibelle,
			Ordre:       a.Ordre,
		})
	}

	sort.Slice(ids, func(i, j int) bool { return lessGroupID(ids[i], ids[j]) })

	out := make([]GroupeGenerique, 0, len(ids))
	for _, id := range ids {
		g := byID[id]
		sort.Slice(g.membres, func(i, j int) bool {
			if g.membres[i].Ordre != g.membres[j].Ordre {
				return g.membres[i].Ordre < g.membres[j].Ordre
			}
			return g.membres[i].CIS < g.membres[j].CIS
		})
		out = append(out, GroupeGenerique{ID: id, Libelle: g.libelle, Membres: g.membres})
	}
	d.Groupes = out
}

// lessGroupID trie les identifiants de groupe numériquement quand ils le
// sont — un tri lexicographique placerait le groupe 10 avant le groupe 2,
// ce qui rend le référentiel désagréable à parcourir.
func lessGroupID(a, b string) bool {
	na, oka := atoiSafe(a)
	nb, okb := atoiSafe(b)
	if oka && okb && na != nb {
		return na < nb
	}
	if oka != okb {
		return oka
	}
	return a < b
}

func atoiSafe(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// resolveLiensCT renseigne LienAvisCT sur les avis SMR et ASMR à partir de
// HAS_LiensPageCT_bdpm.txt, joint par code de dossier HAS. Un avis dont le
// code de dossier n'a pas de lien conserve un LienAvisCT nil : c'est un cas
// normal et fréquent, pas une anomalie.
func (d *Dataset) resolveLiensCT() {
	if len(d.LiensCT) == 0 {
		return
	}
	byDossier := make(map[string]string, len(d.LiensCT))
	for _, l := range d.LiensCT {
		byDossier[l.CodeDossierHAS] = l.URL
	}
	for i := range d.AvisSMR {
		if url, ok := byDossier[d.AvisSMR[i].CodeDossierHAS]; ok {
			u := url
			d.AvisSMR[i].LienAvisCT = &u
		}
	}
	for i := range d.AvisASMR {
		if url, ok := byDossier[d.AvisASMR[i].CodeDossierHAS]; ok {
			u := url
			d.AvisASMR[i].LienAvisCT = &u
		}
	}
}
