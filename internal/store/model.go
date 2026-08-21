package store

// Identifiants de position dans les slices contiguës du Store. Ce sont des
// index, pas des clés métier : ils ne survivent pas à un rechargement et ne
// doivent jamais fuiter dans une réponse HTTP.
type (
	// SpecID est la position d'une spécialité dans Store.specs.
	SpecID int32
	// PresID est la position d'une présentation.
	PresID int32
	// SubID est la position d'une substance.
	SubID int32
	// GrpID est la position d'un groupe générique.
	GrpID int32
)

// InvalidID marque l'absence de résultat sur une résolution d'index.
const InvalidID = -1

// Spec est une spécialité en représentation compacte
// (docs/03-modele-de-donnees.md §4.1).
//
// Les champs à faible cardinalité sont internés (uint16) ou énumérés
// (uint8), et le CIS est un uint32 : les codes CIS sont des entiers à huit
// chiffres, donc inférieurs à 2³². Économie de 20 octets par spécialité sur
// le seul CIS, et comparaison de clés réduite à une comparaison d'entiers.
type Spec struct {
	Denom     string
	DenomNorm string
	NumEuro   string

	Voies      []uint16
	Titulaires []uint16

	CIS     uint32
	DateAMM int32

	Forme uint16

	StatutAMM    uint8
	Procedure    uint8
	EtatCommerc  uint8
	StatutBdm    uint8
	Surveillance bool
}

// Pres est une présentation en représentation compacte.
//
// CIP13 est un uint64 : treize chiffres dépassent 2³². CIP7 tient dans un
// uint32.
type Pres struct {
	Libelle        string
	IndicationsRmb string

	TauxRemb []uint8

	CIP13 uint64
	Spec  SpecID

	CIP7            uint32
	DateDeclaration int32

	PrixMedicamentCents         int32
	PrixPublicCents             int32
	HonorairesDispensationCents int32

	// Les prix sont des int32 et non des *int32 : un pointeur par prix
	// coûterait 8 octets d'indirection et une allocation, pour 20 903
	// présentations dont 7 276 sans prix. L'absence est portée par un
	// masque de bits, et la distinction absent/zéro — exigence explicite du
	// modèle — reste donc exacte.
	present uint8

	StatutAdmin  uint8
	EtatCommerc  uint8
	AgrementColl uint8
}

// Bits de presence pour Pres.present.
const (
	presPrixMedicament uint8 = 1 << iota
	presPrixPublic
	presHonoraires
	presAgrement
)

// PrixMedicament retourne le prix en centimes et un booléen d'existence.
// Un prix absent (7 276 présentations sur 20 903) n'est jamais confondu avec
// un prix nul.
func (p Pres) PrixMedicament() (int32, bool) {
	return p.PrixMedicamentCents, p.present&presPrixMedicament != 0
}

// PrixPublic retourne le prix public en centimes et son existence.
func (p Pres) PrixPublic() (int32, bool) {
	return p.PrixPublicCents, p.present&presPrixPublic != 0
}

// Honoraires retourne les honoraires de dispensation et leur existence.
func (p Pres) Honoraires() (int32, bool) {
	return p.HonorairesDispensationCents, p.present&presHonoraires != 0
}

// Agrement retourne l'agrément aux collectivités et son existence.
func (p Pres) Agrement() (bool, bool) {
	return p.AgrementColl != 0, p.present&presAgrement != 0
}

// Compo est un élément de composition.
//
// Dosage reste une chaîne brute, jamais interprétée : la source mêle des
// notations métriques et homéopathiques, et normaliser serait interpréter
// une donnée de santé.
type Compo struct {
	Element string
	Denom   string
	// DenomNorm est la forme normalisée de Denom, calculée à l'ingestion
	// comme pour les spécialités et les substances. La dénomination du
	// composant provient du fichier des composants et diffère de celle de la
	// substance liée : elle a donc besoin de sa propre forme normalisée.
	DenomNorm string
	Dosage    string
	Reference string

	Spec    SpecID
	Sub     SubID
	Code    uint32
	Liaison int32
	Nature  uint8
}

// Avis est un avis SMR ou ASMR. Les deux partagent la même structure ;
// Niveau n'est renseigné que pour les ASMR chiffrés en romain.
type Avis struct {
	Motif    string
	Valeur   string
	Libelle  string
	Lien     string
	Dossier  string
	Spec     SpecID
	DateAvis int32
	Niveau   int8 // -1 si non chiffré
}

// Sub est une substance active du référentiel dérivé.
type Sub struct {
	Denom     string
	DenomNorm string
	CodeStr   string
	Code      uint32
	NbSpecs   int32
}

// Membre est une spécialité au sein d'un groupe générique.
type Membre struct {
	Spec  SpecID
	Type  uint8
	Ordre int32
}

// Grp est un groupe générique dénormalisé.
type Grp struct {
	Libelle     string
	LibelleNorm string
	IDStr       string
	ID          uint32
	Membres     []Membre
}

// Cond est une condition de prescription et de délivrance.
type Cond struct {
	Texte string
	Spec  SpecID
}

// Rupt est une rupture, tension ou remise à disposition.
type Rupt struct {
	Statut        string
	Libelle       string
	LibelleSource string
	Lien          string

	Spec  SpecID
	Pres  PresID // InvalidID si toute la spécialité est concernée
	CIP13 uint64

	DateDebut     int32
	DateMAJ       int32
	DateRemise    int32
	CodeStatut    uint8
	DebutApprox   bool
	hasRemise     bool
	toutLeProduit bool
}

// DateRemiseDisposition retourne la date de remise à disposition et son
// existence.
func (r Rupt) DateRemiseDisposition() (int32, bool) { return r.DateRemise, r.hasRemise }

// ToutLeProduit indique que la rupture porte sur la spécialité entière et
// non sur une présentation précise. C'est une information distincte de
// « conditionnement inconnu ».
func (r Rupt) ToutLeProduit() bool { return r.toutLeProduit }

// MITM est le statut de médicament d'intérêt thérapeutique majeur.
type MITM struct {
	CodeATC string
	Denom   string
	Lien    string
	Spec    SpecID
}
