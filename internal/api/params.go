package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Bornes de pagination (docs/04-specification-api.md §1.2).
const (
	DefaultLimit    = 20
	MaxLimit        = 100
	MaxSuggestLimit = 200
	// MaxQueryLen borne la longueur de `q`. Une requête de 10 000 caractères
	// n'a aucun sens métier et ne sert qu'à faire travailler le serveur.
	MaxQueryLen = 200
)

// paramError est une erreur de validation d'entrée, portant le message
// destiné à l'appelant.
type paramError struct {
	msg string
}

func (e *paramError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &paramError{msg: fmt.Sprintf(format, args...)}
}

// La validation applique une **liste blanche** : tout paramètre reconnu est
// contraint à un domaine explicite, et un paramètre inconnu est ignoré sans
// erreur (docs/06-securite.md). Ignorer l'inconnu plutôt que le rejeter est
// délibéré : c'est ce qui permet d'ajouter un paramètre optionnel dans une
// version ultérieure sans casser les clients qui l'envoient déjà, et de
// tolérer les paramètres de traçage (`utm_*`) ajoutés par les navigateurs.

// Les validateurs prennent les `url.Values` déjà décodées plutôt que la
// requête : `URL.Query()` re-parcourt et ré-alloue la chaîne brute à chaque
// appel, et un handler en enchaîne une dizaine. Le décodage a lieu une fois
// par requête, en tête de handler.

// parseLimit valide le paramètre `limit`.
func parseLimit(q url.Values, max int) (int, error) {
	raw := q.Get("limit")
	if raw == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalid("limit doit être un entier, reçu %q.", truncate(raw, 40))
	}
	if n < 1 || n > max {
		return 0, invalid("limit doit être compris entre 1 et %d, reçu %d.", max, n)
	}
	return n, nil
}

// parseQuery valide le paramètre `q`.
func parseQuery(q url.Values, required bool) (string, error) {
	v := strings.TrimSpace(q.Get("q"))
	if v == "" {
		if required {
			return "", invalid("le paramètre q est obligatoire et ne peut pas être vide.")
		}
		return "", nil
	}
	if len(v) > MaxQueryLen {
		return "", invalid("q ne peut pas dépasser %d caractères, reçu %d.", MaxQueryLen, len(v))
	}
	return v, nil
}

// parseBool valide un paramètre booléen. Absent ⇒ (false, false, nil), ce
// qui distingue « filtre non demandé » de « filtre demandé à false ».
func parseBool(q url.Values, name string) (value, present bool, err error) {
	raw := q.Get(name)
	if raw == "" {
		return false, false, nil
	}
	switch strings.ToLower(raw) {
	case "true", "1":
		return true, true, nil
	case "false", "0":
		return false, true, nil
	default:
		return false, false, invalid("%s doit valoir true ou false, reçu %q.", name, truncate(raw, 40))
	}
}

// parseEnum valide un paramètre contraint à un ensemble de valeurs.
func parseEnum(q url.Values, name string, allowed map[string]string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(q.Get(name)))
	if raw == "" {
		return "", nil
	}
	v, ok := allowed[raw]
	if !ok {
		return "", invalid("%s doit valoir l'une de %s, reçu %q.",
			name, sortedKeys(allowed), truncate(raw, 40))
	}
	return v, nil
}

func sortedKeys(m map[string]string) string {
	return "{" + strings.Join(slices.Sorted(maps.Keys(m)), ", ") + "}"
}

// validateCode valide un code numérique de longueur fixe (CIS, CIP).
func validateCode(value, name string, lengths ...int) error {
	for _, l := range lengths {
		if len(value) == l && isDigits(value) {
			return nil
		}
	}
	var want []string
	for _, l := range lengths {
		want = append(want, strconv.Itoa(l))
	}
	return invalid("%s doit être composé de %s chiffres, reçu %q.",
		name, strings.Join(want, " ou "), truncate(value, 40))
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// truncate borne la longueur d'une valeur reprise dans un message d'erreur :
// renvoyer 10 000 caractères hostiles à l'appelant serait lui offrir un
// amplificateur.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- Curseur de pagination (T-28) ------------------------------------------

// cursor encode la position dans un parcours et la version du jeu de
// données qui l'a produit.
//
// Le hash est ce qui rend la pagination sûre : si le dataset est rechargé
// entre deux pages, le curseur devient invalide et l'appelant reçoit une
// erreur explicite plutôt qu'une page silencieusement incohérente — des
// doublons ou des omissions qu'il n'aurait aucun moyen de détecter.
type cursor struct {
	Offset int    `json:"o"`
	Hash   string `json:"h"`
}

// hashPrefixLen est la longueur du préfixe de hash embarqué dans le curseur.
// Le hash complet ferait 64 caractères pour aucun bénéfice : le curseur
// n'est pas un secret, seulement un détecteur de changement de version.
const hashPrefixLen = 8

func encodeCursor(offset int, datasetHash string) string {
	c := cursor{Offset: offset, Hash: shortHash(datasetHash)}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor décode et valide un curseur.
//
// Un curseur forgé, tronqué ou illisible produit une erreur de paramètre,
// jamais une panique : il vient d'une URL publique et doit être traité comme
// une entrée hostile.
func decodeCursor(raw, datasetHash string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, invalid("le curseur est illisible.")
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, invalid("le curseur est illisible.")
	}
	if c.Offset < 0 {
		return 0, invalid("le curseur est illisible.")
	}
	if c.Hash != shortHash(datasetHash) {
		return 0, &staleCursorError{}
	}
	return c.Offset, nil
}

// staleCursorError distingue le curseur périmé du curseur malformé : le
// premier mérite un type d'erreur propre, car il est **normal** et
// rattrapable — l'appelant reprend son parcours du début.
type staleCursorError struct{}

func (e *staleCursorError) Error() string {
	return "le curseur a été émis pour une autre version du jeu de données ; reprendre la pagination du début"
}

func shortHash(h string) string {
	if len(h) > hashPrefixLen {
		return h[:hashPrefixLen]
	}
	return h
}

// paginate applique offset et limite à un total, et retourne les bornes de
// tranche ainsi que le curseur suivant.
func paginate(total, offset, limit int, datasetHash string) (lo, hi int, next string, hasMore bool) {
	if offset > total {
		offset = total
	}
	lo = offset
	hi = offset + limit
	if hi > total {
		hi = total
	}
	hasMore = hi < total
	if hasMore {
		next = encodeCursor(hi, datasetHash)
	}
	return lo, hi, next, hasMore
}
