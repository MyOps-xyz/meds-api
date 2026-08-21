package store

// CSR stocke une relation 1-N en Compressed Sparse Row
// (docs/03-modele-de-donnees.md §4.2).
//
// Une map[SpecID][]T coûterait 15 857 entrées de table de hachage plus
// 15 857 slices allouées séparément, éparpillées en mémoire, et un défaut de
// cache par accès. Le CSR n'alloue que deux slices contiguës pour toute la
// relation ; Get est une soustraction de bornes, sans allocation ni
// recherche, et les enfants d'un même parent sont physiquement adjacents.
type CSR[T any] struct {
	offsets []int32
	values  []T
	// empty est une slice vide **non nulle**, préallouée une fois. Elle est
	// ce que Get retourne pour un parent sans enfant : une re-tranche de
	// values ne conviendrait pas, car sur une relation entièrement vide elle
	// vaudrait nil, et l'API sérialiserait `null` là où un intégrateur
	// attend `[]`.
	empty []T
}

// BuildCSR construit un index CSR pour n parents.
//
// parentOf donne le parent de chaque valeur ; les valeurs sont réordonnées
// par parent. L'implémentation est un tri par comptage en deux passes, en
// O(n) : compter, puis placer. Un tri comparatif serait inutilement coûteux
// alors que la clé est un entier dense et borné.
//
// Une valeur dont le parent est hors bornes est ignorée : à ce stade,
// l'intégrité référentielle a déjà été vérifiée à l'ingestion (T-09), et une
// panique au chargement d'un snapshot serait une réponse disproportionnée à
// une incohérence résiduelle.
func BuildCSR[T any](n int, values []T, parentOf func(T) int32) *CSR[T] {
	c := &CSR[T]{offsets: make([]int32, n+1), empty: make([]T, 0)}
	if n == 0 {
		return c
	}

	counts := make([]int32, n)
	kept := 0
	for i := range values {
		p := parentOf(values[i])
		if p < 0 || int(p) >= n {
			continue
		}
		counts[p]++
		kept++
	}

	var acc int32
	for i := 0; i < n; i++ {
		c.offsets[i] = acc
		acc += counts[i]
	}
	c.offsets[n] = acc

	c.values = make([]T, kept)
	cursor := make([]int32, n)
	copy(cursor, c.offsets[:n])
	for i := range values {
		p := parentOf(values[i])
		if p < 0 || int(p) >= n {
			continue
		}
		c.values[cursor[p]] = values[i]
		cursor[p]++
	}
	return c
}

// Get retourne les valeurs rattachées au parent id.
//
// La slice retournée pointe dans le stockage du Store : l'appelant ne doit
// **jamais** la modifier ni la trier en place, sous peine de muter un Store
// publié et lu sans verrou par d'autres requêtes
// (docs/03-modele-de-donnees.md §4.6).
//
// Un parent sans enfant retourne une slice vide non nulle, ce qui permet aux
// handlers de sérialiser `[]` plutôt que `null` sans cas particulier — la
// distinction compte pour un intégrateur.
func (c *CSR[T]) Get(id int32) []T {
	if id < 0 || int(id)+1 >= len(c.offsets) {
		return c.empty
	}
	lo, hi := c.offsets[id], c.offsets[id+1]
	if lo == hi {
		return c.empty
	}
	return c.values[lo:hi:hi]
}

// Len est le nombre total de valeurs indexées.
func (c *CSR[T]) Len() int { return len(c.values) }

// All expose toutes les valeurs, dans l'ordre du CSR (groupées par parent).
func (c *CSR[T]) All() []T { return c.values }
