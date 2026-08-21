package bdpm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// dateFRLayout est le format des dates JJ/MM/AAAA rencontré dans tous les
// fichiers sources sauf CIS_HAS_SMR_bdpm.txt et CIS_HAS_ASMR_bdpm.txt
// (docs/01-analyse-source-bdpm.md §3, docs/09-pipeline-mise-a-jour.md
// §3.5).
const dateFRLayout = "02/01/2006"

// dateCompactLayout est le format AAAAMMJJ des dates d'avis SMR et ASMR,
// distinct du reste de la source.
const dateCompactLayout = "20060102"

// dateISOLayout est le format de sortie retenu pour toutes les dates du
// snapshot (docs/03-modele-de-donnees.md §3.1).
const dateISOLayout = "2006-01-02"

// parseDateFR convertit une date JJ/MM/AAAA en ISO 8601 (AAAA-MM-JJ). v ne
// doit pas être vide : les appelants qui doivent tolérer un champ vide
// utilisent parseDateFROptional.
func parseDateFR(v string) (string, error) {
	t, err := time.Parse(dateFRLayout, v)
	if err != nil {
		return "", fmt.Errorf("date %q non conforme au format JJ/MM/AAAA : %w", v, err)
	}
	return t.Format(dateISOLayout), nil
}

// parseDateFROptional se comporte comme parseDateFR, sauf qu'une valeur
// vide (après TrimSpace) retourne (nil, nil) plutôt qu'une erreur : c'est
// le cas de la date de remise à disposition d'une rupture, souvent absente
// (docs/01-analyse-source-bdpm.md §3.9).
func parseDateFROptional(v string) (*string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil //nolint:nilnil // absence de date : ni erreur ni valeur, un pointeur nil est la représentation correcte.
	}
	iso, err := parseDateFR(v)
	if err != nil {
		return nil, err
	}
	return &iso, nil
}

// parseDateCompact convertit une date AAAAMMJJ (avis SMR/ASMR) en ISO 8601.
func parseDateCompact(v string) (string, error) {
	t, err := time.Parse(dateCompactLayout, v)
	if err != nil {
		return "", fmt.Errorf("date %q non conforme au format AAAAMMJJ : %w", v, err)
	}
	return t.Format(dateISOLayout), nil
}

// parsePriceCents convertit un prix décimal à virgule française (« 24,34 »)
// en centimes entiers. Une valeur vide retourne (nil, nil) : un prix
// absent est une donnée légitime (34,8 % des présentations, non
// remboursables — docs/03-modele-de-donnees.md §3.2), jamais un zéro.
//
// Au-delà de 1 000 €, la source mesurée insère aussi une virgule comme
// séparateur de milliers (« 1,466,29 » pour 1 466,29 €, 977 occurrences sur
// les trois colonnes de prix de CIS_CIP_bdpm.txt le 14/08/2026) : ce n'est
// documenté nulle part, mais un remplacement aveugle de toutes les
// virgules par un point produirait « 1.466.29 », illisible pour
// strconv.ParseFloat, et mettrait ces lignes en quarantaine à tort. Seule
// la dernière virgule sépare la partie décimale ; les précédentes sont
// retirées comme séparateurs de milliers.
//
// L'arrondi via math.Round est délibéré, pas une troncature :
// 24.34 * 100 vaut 2433.9999… en virgule flottante binaire, et une
// troncature donnerait 2433 au lieu de 2434, soit un centime perdu sur une
// part importante des 20 903 présentations (piège P4).
func parsePriceCents(v string) (*int32, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil //nolint:nilnil // prix absent : ni erreur ni valeur, distingué à dessein d'un prix nul.
	}
	normalized := strings.ReplaceAll(v, " ", "")
	if n := strings.Count(normalized, ","); n > 1 {
		last := strings.LastIndex(normalized, ",")
		normalized = strings.ReplaceAll(normalized[:last], ",", "") + "." + normalized[last+1:]
	} else {
		normalized = strings.ReplaceAll(normalized, ",", ".")
	}
	f, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return nil, fmt.Errorf("prix %q non convertible en décimal : %w", v, err)
	}
	cents := int32(math.Round(f * 100))
	return &cents, nil
}

// parsePercentages découpe un champ de taux de remboursement multi-valué
// (séparateur `;`) en entiers, en absorbant l'instabilité du format
// (« 65% » et « 65 % » coexistent — piège P4). Une valeur vide retourne un
// slice non nil de longueur zéro, pour sérialiser en `[]` plutôt qu'en
// `null` (le champ est documenté comme un tableau, docs/03 §3.2).
func parsePercentages(v string) ([]int, error) {
	out := make([]int, 0)
	for _, part := range splitMulti(v) {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(part, "%")
		part = strings.TrimSpace(part)
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("taux de remboursement %q non convertible en entier : %w", part, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// splitMulti découpe un champ multi-valué sur `;`, détrime chaque élément
// et retire les éléments vides (docs/09-pipeline-mise-a-jour.md §3.5). Le
// résultat est toujours un slice non nil, pour sérialiser en `[]` plutôt
// qu'en `null` sur les champs tableau du modèle (voies_administration,
// titulaires).
func splitMulti(v string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// trimOrNil détrime v et retourne nil si le résultat est vide, plutôt
// qu'une chaîne vide : une chaîne source vide devient toujours `null`
// dans le snapshot, jamais `""` (docs/09-pipeline-mise-a-jour.md §3.5).
func trimOrNil(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

// parseOuiNon convertit le champ « surveillance renforcée » de
// CIS_bdpm.txt (« Oui »/« Non ») en booléen.
func parseOuiNon(v string) (bool, error) {
	switch strings.TrimSpace(v) {
	case "Oui":
		return true, nil
	case "Non":
		return false, nil
	default:
		return false, fmt.Errorf("valeur %q hors de {Oui, Non}", v)
	}
}

// parseTriBool convertit le champ « agrément collectivités » de
// CIS_CIP_bdpm.txt (« oui »/« non »/« inconnu ») en booléen nullable :
// « inconnu » devient nil, jamais false (docs/03-modele-de-donnees.md
// §3.2).
func parseTriBool(v string) (*bool, error) {
	switch strings.TrimSpace(v) {
	case "oui":
		b := true
		return &b, nil
	case "non":
		b := false
		return &b, nil
	case "inconnu":
		return nil, nil //nolint:nilnil // "inconnu" est une troisième valeur légitime, distincte de oui et non.
	default:
		return nil, fmt.Errorf("valeur %q hors de {oui, non, inconnu}", v)
	}
}

// httpsify réécrit une URL `http://` en `https://` (docs/03
// §3.7 : les liens MITM ; appliqué aussi aux liens ANSM des ruptures par la
// même règle générale du pipeline — docs/09-pipeline-mise-a-jour.md §3.5).
// Une URL déjà en HTTPS, ou dans un autre schéma, est retournée telle
// quelle.
func httpsify(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "http://") {
		return "https://" + strings.TrimPrefix(v, "http://")
	}
	// strings.Clone : v peut sinon rester une sous-chaîne du tampon de
	// ligne courant du Scanner (voir le commentaire de doc de cloneAll).
	return strings.Clone(v)
}

// normalizeNature convertit la nature d'un composant (« SA », « FT » ou
// « ST ») en sa forme canonique (« SA » ou « FT »). ST n'existe pas dans la
// donnée mesurée mais est documenté par l'ANSM (piège P3,
// docs/01-analyse-source-bdpm.md §4) : l'accepter et le convertir en FT
// couvre le cas où l'ANSM alignerait un jour la donnée sur sa
// documentation, sans perdre 17 % du fichier de composition si elle ne l'a
// pas encore fait.
func normalizeNature(v string) (string, bool) {
	switch strings.TrimSpace(v) {
	case "SA":
		return "SA", true
	case "FT", "ST":
		return "FT", true
	default:
		return "", false
	}
}

// romanASMRToNiveau convertit une valeur d'ASMR exprimée en chiffre romain
// pur (« I » à « V ») en son niveau numérique (1 à 5). Les valeurs
// textuelles observées dans la source (« Commentaires sans chiffrage de
// l'ASMR », « V dans l'attente de données », « Commentaires ») ne
// correspondent à aucune entrée de cette table et retournent ok=false
// (docs/03-modele-de-donnees.md §3.6).
func romanASMRToNiveau(v string) (niveau int, ok bool) {
	switch strings.TrimSpace(v) {
	case "I":
		return 1, true
	case "II":
		return 2, true
	case "III":
		return 3, true
	case "IV":
		return 4, true
	case "V":
		return 5, true
	default:
		return 0, false
	}
}

// isDigits rapporte si v est composé exactement de n chiffres ASCII, sans
// signe ni espace. Utilisé pour valider les codes CIS (8), CIP7 (7) et
// CIP13 (13), qui sont documentés comme numériques mais représentés en
// chaîne pour préserver les zéros de tête.
func isDigits(v string, n int) bool {
	if len(v) != n {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// ruptureStatusTable dérive le slug et le libellé canonique d'un code de
// statut de rupture, jamais du libellé source qui est incohérent (piège
// P5, docs/01-analyse-source-bdpm.md §5). Les slugs des codes 1, 3 et 4
// suivent le style du slug documenté pour le code 2
// (« tension_approvisionnement », docs/03-modele-de-donnees.md §3.7).
var ruptureStatusTable = map[int]struct {
	slug    string
	libelle string
}{
	1: {"rupture_stock", "Rupture de stock"},
	2: {"tension_approvisionnement", "Tension d'approvisionnement"},
	3: {"arret_commercialisation", "Arrêt de commercialisation"},
	4: {"remise_disposition", "Remise à disposition"},
}

// ruptureStatus retourne le slug et le libellé canonique associés à code,
// ou ok=false si code est hors de {1, 2, 3, 4} (docs/01-analyse-source-bdpm.md
// §3.9).
func ruptureStatus(code int) (slug, libelle string, ok bool) {
	s, found := ruptureStatusTable[code]
	if !found {
		return "", "", false
	}
	return s.slug, s.libelle, true
}

// dateDebutApproximativeThreshold est la date à partir de laquelle l'ANSM
// documente que la « date de début » d'une rupture est fiable. Avant
// cette date, elle est en réalité la date de mise à jour de la fiche
// (docs/01-analyse-source-bdpm.md §3.9, docs/03-modele-de-donnees.md
// §3.7).
var dateDebutApproximativeThreshold = time.Date(2023, time.October, 6, 0, 0, 0, 0, time.UTC)

// isDateDebutApproximative rapporte si dateDebut (au format JJ/MM/AAAA)
// est strictement antérieure au 06/10/2023, auquel cas elle doit être
// signalée comme approximative. Une date déjà invalide n'est pas de son
// ressort : l'appelant doit valider dateDebut au préalable.
func isDateDebutApproximative(dateDebut string) (bool, error) {
	t, err := time.Parse(dateFRLayout, dateDebut)
	if err != nil {
		return false, fmt.Errorf("date de début %q non conforme au format JJ/MM/AAAA : %w", dateDebut, err)
	}
	return t.Before(dateDebutApproximativeThreshold), nil
}

// genericTypeLabels associe à chaque type de générique observé son
// libellé (docs/01-analyse-source-bdpm.md §3.7). La valeur 3 est
// délibérément absente : l'énumération est discontinue dans la source, et
// un parseur qui supposerait un intervalle 0–4 continu accepterait une
// valeur inexistante.
var genericTypeLabels = map[int]string{
	0: "princeps",
	1: "générique",
	2: "par complémentarité posologique",
	4: "substituable",
}

// genericTypeLabel retourne le libellé associé à un type de générique, ou
// ok=false si le type est inconnu (notamment 3, qui n'existe pas).
func genericTypeLabel(t int) (label string, ok bool) {
	label, ok = genericTypeLabels[t]
	return label, ok
}

// cloneAll copie chaque élément de v via strings.Clone. Les champs
// retournés par Scanner.Fields (et les valeurs qui en dérivent, comme les
// éléments issus de splitMulti) partagent leur mémoire sous-jacente avec
// le tampon de ligne courant du Scanner ; les copier explicitement avant
// de les conserver dans une entité qui survit à l'itération est la règle
// de sécurité de ce paquet (voir le commentaire de doc de Scanner.Fields).
func cloneAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = strings.Clone(s)
	}
	return out
}

// clonePtr copie *v via strings.Clone et retourne un nouveau pointeur, ou
// nil si v est nil (voir cloneAll).
func clonePtr(v *string) *string {
	if v == nil {
		return nil
	}
	c := strings.Clone(*v)
	return &c
}
