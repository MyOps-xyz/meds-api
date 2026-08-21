package store

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize applique la chaîne de normalisation de recherche.
//
// **Ce code est le seul de tout le projet à normaliser du texte**, et c'est
// la propriété qui compte : indexation et requête doivent emprunter
// exactement le même chemin, faute de quoi elles ne se rencontrent jamais.
// C'est le bug classique des moteurs de recherche maison, et
// TestNormalizeSymmetry existe pour l'interdire.
//
//	"BÉSILATE D'AMLODIPINE 5 mg, comprimé" → "besilate d amlodipine 5 mg comprime"
//
// L'apostrophe devient un séparateur : sans cela, une recherche sur
// « amlodipine » manquerait les spécialités indexées sous le nom du sel.
//
// # Sûreté en concurrence
//
// Cette fonction est appelée depuis chaque requête de recherche **et** depuis
// la construction d'un Store pendant une synchronisation : elle est donc
// exécutée en parallèle en permanence, et ne doit détenir aucun état.
//
// La première rédaction partageait un `transform.Chain` déclaré au niveau du
// paquet. C'était une course de données : `transform.String` appelle `Reset`
// puis `Transform` sur le transformeur, qui porte des tampons internes. Le
// détecteur de course l'a mise en évidence entre une construction de Store et
// une recherche concurrente. La conséquence n'aurait pas été un plantage
// visible mais des **résultats de recherche silencieusement faux**, le pire
// mode de défaillance possible pour cette API.
//
// D'où l'implémentation en une passe ci-dessous : `norm.NFD` est une valeur,
// utilisable simultanément par toutes les goroutines, et le reste du travail
// se fait sur la pile.
func Normalize(s string) string {
	if s == "" {
		return ""
	}

	// Décomposition canonique : « é » devient « e » suivi de l'accent aigu
	// combinant, que la boucle écarte ensuite comme marque non espaçante.
	// norm.NFD est une valeur immuable, sûre en concurrence.
	decomposed := norm.NFD.String(s)

	var b strings.Builder
	b.Grow(len(decomposed))
	lastSpace := true

	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Marque diacritique issue de la décomposition : écartée.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastSpace = false
		case lastSpace:
			// Séparateurs consécutifs écrasés : la sortie est directement
			// découpable, sans champ vide.
		default:
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// MinTokenLen est la longueur minimale d'un token indexé. Les tokens d'un
// seul caractère sont écartés : ils n'apportent aucune sélectivité et
// gonflent les postings.
const MinTokenLen = 2

// Tokenize normalise puis découpe en tokens indexables.
//
// Les mots vides ne sont pas retirés. Sur des dénominations de quelques
// mots, le gain de place est négligeable et le risque de perte de sens
// réel : « A 313 » et « POUR CENT » sont des noms de médicaments.
func Tokenize(s string) []string {
	normalized := Normalize(s)
	if normalized == "" {
		return nil
	}
	parts := strings.Split(normalized, " ")
	out := parts[:0]
	for _, p := range parts {
		if len(p) >= MinTokenLen {
			out = append(out, p)
		}
	}
	return out
}
