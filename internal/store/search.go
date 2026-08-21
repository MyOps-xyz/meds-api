package store

import (
	"math"
	"sort"
	"strings"
)

// Poids des champs indexés (docs/05-conception-recherche.md §3).
const (
	FieldDenom     uint8 = 0
	FieldSubstance uint8 = 1
	FieldTitulaire uint8 = 2
)

// posting est une occurrence d'un token dans une spécialité.
//
// Le champ d'origine et la position sont portés par le posting lui-même
// plutôt que par des index séparés : à 180 000 entrées, trois index
// parallèles coûteraient plus cher en cache qu'un seul tableau de 8 octets
// par entrée.
type posting struct {
	Spec  SpecID
	Field uint8
	// First marque un token en tête de dénomination : le nom commercial y
	// figure presque toujours, ce qui en fait un signal de pertinence fort.
	First bool
}

// SearchIndex est l'index de recherche : index inversé sur tokens triés,
// index trigramme pour la tolérance aux fautes.
type SearchIndex struct {
	tokens   []string
	postings [][]posting
	df       []int32

	// grams associe un trigramme aux indices des tokens qui le contiennent.
	grams map[[3]byte][]int32

	nDocs int
}

// BuildSearchIndex construit l'index de recherche du Store.
//
// Il est bâti après le reste : il s'appuie sur les dénominations
// normalisées déjà calculées à la construction des spécialités, qui ne sont
// donc normalisées qu'une fois.
func (s *Store) BuildSearchIndex() {
	idx := &SearchIndex{nDocs: len(s.specs)}
	type entry struct {
		token string
		p     posting
	}
	entries := make([]entry, 0, len(s.specs)*8)

	add := func(text string, id SpecID, field uint8) {
		for i, tok := range Tokenize(text) {
			entries = append(entries, entry{
				token: tok,
				p:     posting{Spec: id, Field: field, First: i == 0 && field == FieldDenom},
			})
		}
	}

	for i := range s.specs {
		id := SpecID(i)
		add(s.specs[i].Denom, id, FieldDenom)
		for _, t := range s.specs[i].Titulaires {
			add(s.titulaires.Resolve(t), id, FieldTitulaire)
		}
	}
	// Les substances sont indexées via les composants : c'est ce qui permet
	// de trouver un médicament par son principe actif, cas d'usage explicite
	// de l'API.
	for _, c := range s.composants.All() {
		add(c.Denom, c.Spec, FieldSubstance)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].token != entries[j].token {
			return entries[i].token < entries[j].token
		}
		return entries[i].p.Spec < entries[j].p.Spec
	})

	for i := 0; i < len(entries); {
		j := i
		for j < len(entries) && entries[j].token == entries[i].token {
			j++
		}
		list := make([]posting, 0, j-i)
		var lastSpec SpecID = -1
		var docs int32
		for k := i; k < j; k++ {
			p := entries[k].p
			// Un même token peut apparaître plusieurs fois dans une même
			// spécialité (deux substances, dénomination et titulaire) : les
			// postings restent distincts pour porter le champ d'origine,
			// mais la fréquence documentaire ne compte qu'une fois.
			if p.Spec != lastSpec {
				docs++
				lastSpec = p.Spec
			}
			list = append(list, p)
		}
		idx.tokens = append(idx.tokens, entries[i].token)
		idx.postings = append(idx.postings, list)
		idx.df = append(idx.df, docs)
		i = j
	}

	idx.buildTrigrams()
	s.search = idx
}

func (idx *SearchIndex) buildTrigrams() {
	idx.grams = make(map[[3]byte][]int32, len(idx.tokens)*4)
	for i, tok := range idx.tokens {
		for _, g := range trigrams(tok) {
			idx.grams[g] = append(idx.grams[g], int32(i))
		}
	}
}

// trigrams découpe un token en séquences de trois octets, avec marqueurs de
// bord, de sorte que le début et la fin du mot pèsent dans la similarité.
//
// Le découpage est fait sur les octets et non sur les runes : après
// normalisation, les tokens sont en ASCII pur — accents supprimés,
// ponctuation retirée — et un découpage par octet est alors exact tout en
// évitant une conversion par token.
// trigramCount est le nombre de trigrammes de tok, sans les construire.
// Utilisé sur le chemin de repli, où seule la longueur importe.
func trigramCount(tok string) int {
	if tok == "" {
		return 0
	}
	return len(tok) + 1
}

func trigrams(tok string) [][3]byte {
	if tok == "" {
		return nil
	}
	padded := "  " + tok + " "
	out := make([][3]byte, 0, len(padded)-2)
	for i := 0; i+3 <= len(padded); i++ {
		out = append(out, [3]byte{padded[i], padded[i+1], padded[i+2]})
	}
	return out
}

// RankParams regroupe les paramètres de classement
// (docs/05-conception-recherche.md §4). Ils sont réglables sans toucher à
// l'algorithme, ce qui est la condition pour ajuster la pertinence sur le
// jeu de requêtes de référence sans risque de régression algorithmique.
type RankParams struct {
	PoidsDenom     float64
	PoidsSubstance float64
	PoidsTitulaire float64

	BonusExact      float64
	BonusPrefixe    float64
	BonusTokenTete  float64
	BonusCommercial float64
	BonusAMMActive  float64
	MalusSubstance  float64

	// FacteurApproche minore le score des tokens trouvés par tolérance aux
	// fautes, pour qu'une correspondance exacte reste toujours devant.
	FacteurApproche float64
	// SeuilJaccard est la similarité minimale d'un candidat approché.
	SeuilJaccard float64
	// SeuilRepli est le nombre de résultats en deçà duquel la tolérance aux
	// fautes se déclenche. Une recherche fructueuse n'en paie jamais le coût.
	SeuilRepli int
}

// DefaultRankParams retourne les valeurs documentées.
func DefaultRankParams() RankParams {
	return RankParams{
		PoidsDenom:      3.0,
		PoidsSubstance:  2.0,
		PoidsTitulaire:  1.0,
		BonusExact:      50,
		BonusPrefixe:    20,
		BonusTokenTete:  10,
		BonusCommercial: 15,
		BonusAMMActive:  5,
		MalusSubstance:  -5,
		FacteurApproche: 0.6,
		SeuilJaccard:    0.4,
		SeuilRepli:      5,
	}
}

// SearchResult est une spécialité trouvée, avec son score.
type SearchResult struct {
	Spec  SpecID
	Score float64
}

// SearchStats décrit le déroulement d'une recherche, pour l'observabilité
// et pour les tests : c'est ce qui permet de vérifier que le repli ne se
// déclenche pas quand la recherche exacte suffit.
type SearchStats struct {
	Tokens         int
	RepliDeclenche bool
	Candidats      int
}

// Search exécute une recherche conjonctive sur la dénomination, les
// substances et les titulaires.
//
// Le dernier token est traité comme un préfixe lorsque la requête ne se
// termine pas par un espace : la recherche reste réactive pendant la frappe
// sans endpoint distinct.
func (s *Store) Search(query string, params RankParams) ([]SearchResult, SearchStats) {
	var stats SearchStats
	if s.search == nil {
		return nil, stats
	}
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return nil, stats
	}
	stats.Tokens = len(tokens)

	prefixLast := !strings.HasSuffix(query, " ")
	hits := s.search.lookup(tokens, prefixLast, params, false)

	if len(hits) < params.SeuilRepli {
		stats.RepliDeclenche = true
		approx := s.search.lookup(tokens, prefixLast, params, true)
		hits = mergeHits(hits, approx)
	}
	stats.Candidats = len(hits)

	results := make([]SearchResult, 0, len(hits))
	normQuery := Normalize(query)
	for id, sc := range hits {
		results = append(results, SearchResult{Spec: id, Score: s.bonus(id, normQuery, sc, params)})
	}

	// Tri par score décroissant, départagé par CIS croissant : sans ce
	// départage, deux spécialités de score égal changeraient d'ordre d'une
	// requête à l'autre, et la pagination par curseur deviendrait
	// incohérente.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return s.specs[results[i].Spec].CIS < s.specs[results[j].Spec].CIS
	})
	return results, stats
}

// hitScore accumule le score d'une spécialité pendant la recherche.
type hitScore struct {
	score         float64
	onlySubstance bool
	tokenTete     bool
}

// lookup intersecte les postings des tokens. Le mode approché élargit chaque
// token à ses voisins trigrammes.
func (idx *SearchIndex) lookup(tokens []string, prefixLast bool, params RankParams, approx bool) map[SpecID]*hitScore {
	lists := make([][]posting, 0, len(tokens))
	weights := make([]float64, 0, len(tokens))

	for i, tok := range tokens {
		isLast := i == len(tokens)-1
		var merged []posting
		var factor float64 = 1

		switch {
		case approx:
			merged = idx.mergePostings(idx.approxTokens(tok, params.SeuilJaccard))
			factor = params.FacteurApproche
		case isLast && prefixLast:
			merged = idx.mergePostings(idx.prefixTokens(tok))
		default:
			if pos, ok := idx.exact(tok); ok {
				merged = idx.postings[pos]
			}
		}
		if len(merged) == 0 {
			return nil // conjonction : un token introuvable annule la requête
		}
		lists = append(lists, merged)
		weights = append(weights, factor*idx.idf(tok))
	}

	// Intersection du plus court au plus long : la taille du résultat est
	// bornée par la plus petite liste, commencer par elle réduit le travail
	// d'un ordre de grandeur sur les requêtes contenant un mot fréquent.
	order := make([]int, len(lists))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return len(lists[order[a]]) < len(lists[order[b]]) })

	acc := make(map[SpecID]*hitScore, len(lists[order[0]]))
	for _, p := range lists[order[0]] {
		h := acc[p.Spec]
		if h == nil {
			h = &hitScore{onlySubstance: true}
			acc[p.Spec] = h
		}
		applyPosting(h, p, weights[order[0]], params)
	}

	for _, oi := range order[1:] {
		next := make(map[SpecID]*hitScore, len(acc))
		for _, p := range lists[oi] {
			if h, ok := acc[p.Spec]; ok {
				applyPosting(h, p, weights[oi], params)
				next[p.Spec] = h
			}
		}
		acc = next
		if len(acc) == 0 {
			break
		}
	}
	return acc
}

func applyPosting(h *hitScore, p posting, weight float64, params RankParams) {
	var fieldWeight float64
	switch p.Field {
	case FieldDenom:
		fieldWeight = params.PoidsDenom
		h.onlySubstance = false
	case FieldSubstance:
		fieldWeight = params.PoidsSubstance
	default:
		fieldWeight = params.PoidsTitulaire
		h.onlySubstance = false
	}
	h.score += weight * fieldWeight
	if p.First {
		h.tokenTete = true
	}
}

// exact localise un token par dichotomie sur le tableau trié.
func (idx *SearchIndex) exact(tok string) (int, bool) {
	i := sort.SearchStrings(idx.tokens, tok)
	if i < len(idx.tokens) && idx.tokens[i] == tok {
		return i, true
	}
	return 0, false
}

// mergePostings concatène les listes d'occurrences des tokens désignés.
//
// La taille totale est calculée d'abord : sans cela, la concaténation d'une
// expansion de préfixe — jusqu'à maxPrefixExpansion listes, sur le chemin par
// défaut de toute recherche — recopie les mêmes occurrences à chaque
// doublement de capacité.
func (idx *SearchIndex) mergePostings(cands []int32) []posting {
	total := 0
	for _, c := range cands {
		total += len(idx.postings[c])
	}
	if total == 0 {
		return nil
	}
	merged := make([]posting, 0, total)
	for _, c := range cands {
		merged = append(merged, idx.postings[c]...)
	}
	return merged
}

// prefixTokens retourne les indices des tokens commençant par p, en
// s'appuyant sur le tri du tableau : borne inférieure par dichotomie, puis
// parcours tant que le préfixe tient.
func (idx *SearchIndex) prefixTokens(p string) []int32 {
	i := sort.SearchStrings(idx.tokens, p)
	out := make([]int32, 0, 16)
	for ; i < len(idx.tokens) && strings.HasPrefix(idx.tokens[i], p); i++ {
		// Index dans le tableau de tokens, quelques milliers d'entrées.
		out = append(out, int32(i)) //nolint:gosec // index borné par le nombre de tokens indexés
		if len(out) >= maxPrefixExpansion {
			break
		}
	}
	return out
}

// maxPrefixExpansion borne l'expansion d'un préfixe. Une requête d'un seul
// caractère pourrait sinon élargir à des milliers de tokens et transformer
// une frappe en balayage complet de l'index.
const maxPrefixExpansion = 512

// approxTokens retourne les tokens dont la similarité de Jaccard sur les
// trigrammes dépasse le seuil.
//
// Le choix des trigrammes plutôt que de Levenshtein est délibéré :
// Levenshtein imposerait de comparer la requête aux 25 000 tokens, là où
// l'index trigramme ne remonte que quelques dizaines de candidats
// plausibles.
func (idx *SearchIndex) approxTokens(tok string, seuil float64) []int32 {
	qgrams := trigrams(tok)
	if len(qgrams) == 0 {
		return nil
	}
	counts := make(map[int32]int, 64)
	for _, g := range qgrams {
		for _, ti := range idx.grams[g] {
			counts[ti]++
		}
	}

	var out []int32
	for ti, inter := range counts {
		lenT := trigramCount(idx.tokens[ti])
		union := len(qgrams) + lenT - inter
		if union == 0 {
			continue
		}
		if float64(inter)/float64(union) >= seuil {
			out = append(out, ti)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// idf est la fréquence documentaire inverse : un token rare pèse plus qu'un
// token banal. La normalisation par longueur de document de BM25 est omise —
// les dénominations font toutes quelques mots, sa variance n'apporte rien.
func (idx *SearchIndex) idf(tok string) float64 {
	pos, ok := idx.exact(tok)
	var df float64
	if ok {
		df = float64(idx.df[pos])
	}
	n := float64(idx.nDocs)
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

func (s *Store) bonus(id SpecID, normQuery string, h *hitScore, params RankParams) float64 {
	score := h.score
	sp := &s.specs[id]

	switch {
	case sp.DenomNorm == normQuery:
		score += params.BonusExact
	case strings.HasPrefix(sp.DenomNorm, normQuery):
		score += params.BonusPrefixe
	}
	if h.tokenTete {
		score += params.BonusTokenTete
	}
	if s.enumEtatCommerc.resolve(sp.EtatCommerc) == "Commercialisée" {
		score += params.BonusCommercial
	}
	if strings.HasPrefix(s.enumStatutAMM.resolve(sp.StatutAMM), "Autorisation active") {
		score += params.BonusAMMActive
	}
	if h.onlySubstance {
		score += params.MalusSubstance
	}
	return score
}

func mergeHits(base, extra map[SpecID]*hitScore) map[SpecID]*hitScore {
	if len(base) == 0 {
		return extra
	}
	for id, h := range extra {
		if _, ok := base[id]; !ok {
			base[id] = h
		}
	}
	return base
}

// Suggestion est une proposition d'autocomplétion.
type Suggestion struct {
	Token string `json:"token"`
	Count int    `json:"count"`
}

// MaxSuggestions plafonne les propositions d'autocomplétion.
const MaxSuggestions = 200

// Suggest propose les tokens commençant par le préfixe donné, classés par
// fréquence documentaire décroissante.
//
// Aucune structure supplémentaire n'est nécessaire : le tableau de tokens
// trié de l'index inversé suffit. Un trie serait plus élégant sur le papier,
// mais à cette échelle un tableau contigu tient dans quelques lignes de
// cache là où un trie multiplie les sauts de pointeurs.
func (s *Store) Suggest(prefix string, limit int) []Suggestion {
	if s.search == nil {
		return nil
	}
	p := Normalize(prefix)
	if p == "" {
		return nil
	}
	if limit <= 0 || limit > MaxSuggestions {
		limit = MaxSuggestions
	}

	i := sort.SearchStrings(s.search.tokens, p)
	out := make([]Suggestion, 0, limit)
	for ; i < len(s.search.tokens) && strings.HasPrefix(s.search.tokens[i], p); i++ {
		out = append(out, Suggestion{Token: s.search.tokens[i], Count: int(s.search.df[i])})
		if len(out) >= MaxSuggestions {
			break
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Token < out[b].Token
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// TokenCount est le nombre de tokens distincts indexés.
func (s *Store) TokenCount() int {
	if s.search == nil {
		return 0
	}
	return len(s.search.tokens)
}
