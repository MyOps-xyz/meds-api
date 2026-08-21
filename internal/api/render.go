package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// GzipThreshold est la taille au-delà de laquelle la réponse est compressée.
// En deçà, la compression coûte plus de temps processeur qu'elle n'économise
// d'octets, et ajoute une latence sur les réponses les plus fréquentes.
const GzipThreshold = 1024

// bufPool recycle les tampons de sérialisation. Sans lui, chaque réponse
// allouerait un tampon de plusieurs kilooctets, à jeter aussitôt : à
// 3 000 requêtes par seconde, c'est le poste d'allocation dominant.
var bufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// gzipPool recycle les compresseurs, dont l'initialisation alloue ~200 Kio
// de fenêtre.
var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// Meta accompagne toute réponse de liste.
type Meta struct {
	Total          int    `json:"total"`
	Limit          int    `json:"limit"`
	HasMore        bool   `json:"has_more"`
	NextCursor     string `json:"next_cursor,omitempty"`
	DatasetVersion string `json:"dataset_version"`
}

// Envelope est l'enveloppe de réponse commune.
type Envelope struct {
	Data any   `json:"data"`
	Meta *Meta `json:"meta,omitempty"`
}

// WriteJSON sérialise une réponse, en gérant l'ETag, le 304, la compression
// et la mise en cache.
//
// L'ordre des opérations compte : l'ETag est calculé **avant** toute
// compression, de sorte qu'il ne dépend pas de l'Accept-Encoding du client.
// Deux clients, l'un acceptant gzip et l'autre non, obtiennent le même ETag
// pour la même ressource — c'est la condition pour qu'un cache intermédiaire
// ne serve pas une réponse compressée à un client qui ne la comprend pas.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, etagSeed string, v any) {
	h := w.Header()

	// La négociation de cache a lieu **avant** toute sérialisation.
	//
	// L'ordre inverse — sérialiser puis comparer — était la première
	// rédaction : un 304 coûtait alors exactement le prix d'un 200, corps
	// construit puis jeté. Mesuré, il sortait à 61 µs au p99 pour un budget
	// de 50 µs, soit le coût d'une recherche complète. Or l'ETag ne dépend
	// que de la version du jeu de données, de la route et des paramètres :
	// rien n'oblige à connaître la réponse pour savoir qu'elle n'a pas changé.
	if etagSeed != "" {
		if h.Get("ETag") == "" && NotModified(w, r, etagSeed) {
			return
		}
	} else {
		h.Set("Cache-Control", "no-store")
	}

	// Assertion vérifiée : le pool ne contient que des *bytes.Buffer, mais
	// une assertion sèche transformerait une hypothèse fausse en panique —
	// dans la fonction qui sérialise **toutes** les réponses, donc en 500 sur
	// chaque requête. Trois lignes de repli valent mieux que ce risque.
	buf, ok := bufPool.Get().(*bytes.Buffer)
	if !ok {
		buf = new(bytes.Buffer)
	}
	buf.Reset()
	defer bufPool.Put(buf)

	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		WriteProblem(w, r, ErrInternal, "La réponse n'a pas pu être sérialisée.")
		return
	}

	h.Set("Content-Type", "application/json; charset=utf-8")

	body := buf.Bytes()
	if len(body) > GzipThreshold && acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		w.WriteHeader(status)

		zw, ok := gzipPool.Get().(*gzip.Writer)
		if !ok {
			zw = gzip.NewWriter(nil)
		}
		zw.Reset(w)
		_, _ = zw.Write(body)
		_ = zw.Close()
		gzipPool.Put(zw)
		return
	}

	h.Add("Vary", "Accept-Encoding")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NotModified pose les en-têtes de cache et répond `304` si le client détient
// déjà cette version.
//
// Appelée **au plus tôt** dans un handler — avant la recherche, le filtrage et
// la pagination — elle évite tout le travail de construction de la réponse.
// C'est ce que décrit docs/07-performance.md §2.1 en budgétant un 304 à 50 µs
// « comparaison d'ETag, aucune sérialisation » : sans court-circuit précoce,
// le 304 paierait le prix fort du 200 qu'il remplace.
//
// Retourne vrai lorsque la réponse a été écrite et que le handler doit rendre
// la main immédiatement.
func NotModified(w http.ResponseWriter, r *http.Request, etagSeed string) bool {
	h := w.Header()
	etag := `"` + etagOf(etagSeed) + `"`
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=3600")

	if !matchesETag(r.Header.Get("If-None-Match"), etag) {
		return false
	}
	// 304 : ni corps, ni Content-Type, conformément à la RFC 9110.
	h.Del("Content-Type")
	w.WriteHeader(http.StatusNotModified)
	return true
}

// etagOf est le SHA-256 tronqué de la graine. Seize octets hexadécimaux
// suffisent : la collision d'ETag n'est pas une faille, seulement un 304
// indu, et la probabilité en est négligeable devant la fréquence de
// rechargement du dataset.
func etagOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

// matchesETag applique la comparaison faible de If-None-Match : `*`
// correspond à tout, et une liste de valeurs séparées par des virgules est
// acceptée.
func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		c := strings.TrimSpace(candidate)
		c = strings.TrimPrefix(c, "W/")
		if c == etag {
			return true
		}
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(enc, ";", 2)[0]), "gzip") {
			return true
		}
	}
	return false
}

// ETagSeed construit la graine d'ETag : version du dataset, méthode, chemin
// et paramètres **normalisés**.
//
// La normalisation est ce qui fait que `?limit=20&q=x` et `?q=x` donnent le
// même ETag lorsque 20 est la valeur par défaut de limit : deux requêtes qui
// produisent la même réponse doivent produire le même ETag, sans quoi le
// cache du client se fragmente inutilement.
func ETagSeed(datasetHash, method, path string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(datasetHash)
	b.WriteByte('|')
	b.WriteString(method)
	b.WriteByte('|')
	b.WriteString(path)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return b.String()
}
