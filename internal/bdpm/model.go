// Package bdpm — entités du snapshot.
//
// Les structures ci-dessous sont la représentation persistée (NDJSON) et,
// via le partage de structures Go entre snapshot et réponse HTTP
// (docs/03-modele-de-donnees.md §3), la forme sérialisée exposée par
// l'API. Les noms de champs JSON reprennent donc au caractère près ceux de
// docs/03-modele-de-donnees.md §3.1 à §3.7.
//
// Un champ absent dans la source (chaîne vide) est représenté par un
// pointeur nil plutôt que par une valeur zéro : distinguer *absent* de
// *vide* ou de *zéro* est une exigence explicite du modèle, en particulier
// pour les prix (docs/03 §3.2) où confondre prix absent et prix nul
// fausserait toute statistique sur 7 276 des 20 903 présentations.
package bdpm

// Specialite est une entrée de CIS_bdpm.txt (docs/03-modele-de-donnees.md
// §3.1) : une spécialité pharmaceutique identifiée par son code CIS.
type Specialite struct {
	CIS                          string   `json:"cis"`
	Denomination                 string   `json:"denomination"`
	FormePharmaceutique          string   `json:"forme_pharmaceutique"`
	VoiesAdministration          []string `json:"voies_administration"`
	StatutAMM                    string   `json:"statut_amm"`
	ProcedureAMM                 string   `json:"procedure_amm"`
	EtatCommercialisation        string   `json:"etat_commercialisation"`
	DateAMM                      string   `json:"date_amm"`
	StatutBdm                    *string  `json:"statut_bdm"`
	NumeroAutorisationEuropeenne *string  `json:"numero_autorisation_europeenne"`
	Titulaires                   []string `json:"titulaires"`
	SurveillanceRenforcee        bool     `json:"surveillance_renforcee"`
}

// Presentation est une entrée de CIS_CIP_bdpm.txt
// (docs/03-modele-de-donnees.md §3.2) : une présentation commerciale
// (boîte) d'une spécialité, avec son prix éventuel.
type Presentation struct {
	CIP13                       string  `json:"cip13"`
	CIP7                        string  `json:"cip7"`
	CIS                         string  `json:"cis"`
	Libelle                     string  `json:"libelle"`
	StatutAdministratif         string  `json:"statut_administratif"`
	EtatCommercialisation       string  `json:"etat_commercialisation"`
	DateDeclaration             string  `json:"date_declaration"`
	AgrementCollectivites       *bool   `json:"agrement_collectivites"`
	TauxRemboursement           []int   `json:"taux_remboursement"`
	PrixMedicamentCents         *int32  `json:"prix_medicament_cents"`
	PrixPublicCents             *int32  `json:"prix_public_cents"`
	HonorairesDispensationCents *int32  `json:"honoraires_dispensation_cents"`
	IndicationsRemboursement    *string `json:"indications_remboursement"`
}

// Composant est une entrée de CIS_COMPO_bdpm.txt
// (docs/03-modele-de-donnees.md §3.3) : un élément de composition d'une
// spécialité (substance active ou fraction thérapeutique).
//
// Dosage reste du texte brut, jamais parsé : il mêle des notations
// métriques (« 1,00 mg ») et homéopathiques (« 2CH à 30CH et 4DH à
// 60DH ») ; toute tentative de normalisation serait une interprétation, et
// donc un risque sur une donnée de santé.
type Composant struct {
	CIS                   string `json:"cis"`
	ElementPharmaceutique string `json:"element_pharmaceutique"`
	CodeSubstance         string `json:"code_substance"`
	DenominationSubstance string `json:"denomination_substance"`
	Dosage                string `json:"dosage"`
	ReferenceDosage       string `json:"reference_dosage"`
	Nature                string `json:"nature"`
	NumeroLiaison         int    `json:"numero_liaison"`
}

// AvisSMR est une entrée de CIS_HAS_SMR_bdpm.txt
// (docs/03-modele-de-donnees.md §3.6) : un avis de service médical rendu.
//
// LienAvisCT est toujours nil à l'issue du parseur de ce paquet : sa
// résolution depuis HAS_LiensPageCT_bdpm.txt est le rôle de T-10, qui
// joint ParseLiensCT par CodeDossierHAS. Le champ existe déjà ici pour que
// l'entité finale n'ait pas à changer de forme entre T-08 et T-10.
type AvisSMR struct {
	CIS             string  `json:"cis"`
	CodeDossierHAS  string  `json:"code_dossier_has"`
	MotifEvaluation string  `json:"motif_evaluation"`
	DateAvis        string  `json:"date_avis"`
	Valeur          string  `json:"valeur"`
	Libelle         string  `json:"libelle"`
	LienAvisCT      *string `json:"lien_avis_ct"`
}

// AvisASMR est une entrée de CIS_HAS_ASMR_bdpm.txt
// (docs/03-modele-de-donnees.md §3.6) : un avis d'amélioration du service
// médical rendu. Il partage six colonnes identiques à AvisSMR ; seul
// Niveau lui est propre.
//
// Niveau n'est renseigné que lorsque Valeur est un chiffre romain pur
// (« I » à « V ») ; les trois valeurs textuelles observées dans la source
// (« Commentaires sans chiffrage de l'ASMR », « V dans l'attente de
// données », « Commentaires ») laissent Niveau à nil.
type AvisASMR struct {
	CIS             string  `json:"cis"`
	CodeDossierHAS  string  `json:"code_dossier_has"`
	MotifEvaluation string  `json:"motif_evaluation"`
	DateAvis        string  `json:"date_avis"`
	Valeur          string  `json:"valeur"`
	Niveau          *int    `json:"niveau"`
	Libelle         string  `json:"libelle"`
	LienAvisCT      *string `json:"lien_avis_ct"`
}

// LienAvisCT est une entrée de HAS_LiensPageCT_bdpm.txt
// (docs/03-modele-de-donnees.md §3.6). Ce n'est pas une entité du
// snapshot à part entière : elle est consommée par T-10 pour résoudre
// AvisSMR.LienAvisCT et AvisASMR.LienAvisCT, et n'est donc pas exposée
// telle quelle en NDJSON — d'où l'absence de tags JSON snake_case dans
// docs/03, que cette structure ne prétend pas reproduire.
type LienAvisCT struct {
	CodeDossierHAS string
	URL            string
}

// GroupeAppartenance est une ligne brute de CIS_GENER_bdpm.txt : une
// spécialité appartenant à un groupe générique, avec sa place dans ce
// groupe. C'est une représentation intermédiaire : la dénormalisation en
// groupes portant leurs membres (docs/03-modele-de-donnees.md §3.5) est le
// rôle de T-10, qui agrège ces appartenances par GroupeID.
type GroupeAppartenance struct {
	GroupeID    string
	Libelle     string
	CIS         string
	Type        int
	TypeLibelle string
	Ordre       int
}

// Condition est une entrée de CIS_CPD_bdpm.txt
// (docs/03-modele-de-donnees.md §3.7) : une condition de prescription ou
// de délivrance associée à une spécialité.
type Condition struct {
	CIS       string `json:"cis"`
	Condition string `json:"condition"`
}

// Rupture est une entrée de CIS_CIP_Dispo_Spec.txt
// (docs/03-modele-de-donnees.md §3.7) : une rupture de stock, tension
// d'approvisionnement, arrêt de commercialisation ou remise à disposition.
//
// Statut et Libelle sont dérivés de CodeStatut, jamais repris du libellé
// source, qui est incohérent (piège P5,
// docs/01-analyse-source-bdpm.md#p5). LibelleSource conserve la valeur
// brute pour la traçabilité et l'audit. CIP13 à nil signifie que toute la
// spécialité est concernée, pas qu'on ignore laquelle.
type Rupture struct {
	CIS                    string  `json:"cis"`
	CIP13                  *string `json:"cip13"`
	CodeStatut             int     `json:"code_statut"`
	Statut                 string  `json:"statut"`
	Libelle                string  `json:"libelle"`
	LibelleSource          string  `json:"libelle_source"`
	DateDebut              string  `json:"date_debut"`
	DateDebutApproximative bool    `json:"date_debut_approximative"`
	DateMiseAJour          string  `json:"date_mise_a_jour"`
	DateRemiseDisposition  *string `json:"date_remise_disposition"`
	LienANSM               string  `json:"lien_ansm"`
}

// InfoMITM est une entrée de CIS_MITM.txt (docs/03-modele-de-donnees.md
// §3.7) : le statut de médicament d'intérêt thérapeutique majeur d'une
// spécialité. LienBDPM est réécrit en HTTPS à l'ingestion, la source ne
// fournissant que du HTTP.
type InfoMITM struct {
	CIS          string `json:"cis"`
	CodeATC      string `json:"code_atc"`
	Denomination string `json:"denomination"`
	LienBDPM     string `json:"lien_bdpm"`
}

// UnknownValueFunc signale qu'une valeur d'énumération de la source n'a
// jamais été observée parmi celles répertoriées dans
// docs/01-analyse-source-bdpm.md §3, sans jamais mettre la ligne porteuse
// en quarantaine : les énumérations issues de la source sont conservées
// telles quelles dans le snapshot, et le référentiel doit rester servi
// même si l'ANSM ajoute une modalité. field identifie la colonne logique
// (ex. "statut_amm") et value la valeur inattendue rencontrée. T-09
// branchera un compteur par (file, field, value) sur cette fonction ; ce
// paquet se contente de l'invoquer. Peut être nil, auquel cas les valeurs
// inconnues ne sont simplement pas signalées.
type UnknownValueFunc func(file, field, value string)

func reportUnknown(unknown UnknownValueFunc, known map[string]struct{}, file, field, value string) {
	if unknown == nil || value == "" {
		return
	}
	if _, ok := known[value]; ok {
		return
	}
	unknown(file, field, value)
}
