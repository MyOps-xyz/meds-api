package bdpm

// Motifs de quarantaine (T-08).
//
// Ce sont des identifiants de contrat, pas des messages libres : ils sont
// comptés par motif et exposés sur /v1/dataset (docs/03-modele-de-donnees.md
// §3.8, docs/09-pipeline-mise-a-jour.md §6). Un même motif est
// délibérément partagé entre plusieurs fichiers sources lorsque la nature
// du défaut est la même (ex. ReasonDateInvalide couvre aussi bien
// CIS_bdpm.txt que CIS_CIP_Dispo_Spec.txt) : la liste doit rester courte et
// stable, pas exhaustive par fichier.
const (
	// ReasonCISInvalide signale un code CIS qui n'est pas huit chiffres.
	ReasonCISInvalide = "cis_invalide"
	// ReasonCIP7Invalide signale un code CIP7 qui n'est pas sept chiffres.
	ReasonCIP7Invalide = "cip7_invalide"
	// ReasonCIP13Invalide signale un code CIP13 qui n'est pas treize
	// chiffres (docs/03-modele-de-donnees.md §3.2 et §3.7 : un CIP13
	// absent, en revanche, est une valeur légitime, jamais une erreur).
	ReasonCIP13Invalide = "cip13_invalide"
	// ReasonDateInvalide signale une date qui ne respecte ni le format
	// JJ/MM/AAAA ni AAAAMMJJ selon le fichier concerné.
	ReasonDateInvalide = "date_invalide"
	// ReasonPrixInvalide signale un prix non vide mais non convertible en
	// décimal (virgule ou point).
	ReasonPrixInvalide = "prix_invalide"
	// ReasonTauxRemboursementInvalide signale un taux de remboursement non
	// convertible en entier une fois le `%` et les espaces retirés.
	ReasonTauxRemboursementInvalide = "taux_remboursement_invalide"
	// ReasonAgrementCollectivitesInvalide signale une valeur d'agrément
	// collectivités hors de {oui, non, inconnu}.
	ReasonAgrementCollectivitesInvalide = "agrement_collectivites_invalide"
	// ReasonSurveillanceRenforceeInvalide signale une valeur de
	// surveillance renforcée hors de {Oui, Non}.
	ReasonSurveillanceRenforceeInvalide = "surveillance_renforcee_invalide"
	// ReasonNatureInvalide signale une nature de composant hors de
	// {SA, FT, ST} — ST étant accepté et converti en FT (piège P3).
	ReasonNatureInvalide = "nature_invalide"
	// ReasonNumeroLiaisonInvalide signale un numéro de liaison SA/FT non
	// entier.
	ReasonNumeroLiaisonInvalide = "numero_liaison_invalide"
	// ReasonTypeGeneriqueInconnu signale un type de générique hors de
	// {0, 1, 2, 4} — la valeur 3 n'existe pas, l'énumération est
	// discontinue (docs/01-analyse-source-bdpm.md §3.7).
	ReasonTypeGeneriqueInconnu = "type_generique_inconnu"
	// ReasonOrdreInvalide signale un numéro de tri dans le groupe non
	// entier.
	ReasonOrdreInvalide = "ordre_invalide"
	// ReasonCodeStatutInconnu signale un code de statut de rupture hors de
	// {1, 2, 3, 4}.
	ReasonCodeStatutInconnu = "code_statut_inconnu"
	// ReasonCodeDossierHASVide signale un code de dossier HAS vide, qui ne
	// peut servir de clé de jointure vers les avis SMR/ASMR.
	ReasonCodeDossierHASVide = "code_dossier_has_vide"
)
