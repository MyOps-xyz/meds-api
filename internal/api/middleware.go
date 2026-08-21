package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxRoute
	ctxAPIKey
)

// RequestIDFrom extrait l'identifiant de requête du contexte. Il est
// toujours présent sur une requête passée par le middleware, et vide
// autrement — jamais une panique.
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}

// routeHolder porte le patron de route.
//
// Un pointeur partagé plutôt qu'une simple valeur de contexte : le patron
// n'est connu qu'**après** le routage, alors que les middlewares qui en ont
// besoin — journalisation et métriques — s'exécutent avant. Une valeur de
// contexte posée par le handler ne remonterait pas jusqu'à eux, puisque
// context.WithValue crée un contexte enfant. Le pointeur, lui, est visible
// des deux côtés.
type routeHolder struct{ pattern string }

// RouteFrom extrait le **patron** de route (`/v1/medicaments/{cis}`) et non
// le chemin réel. C'est ce qui empêche l'explosion cardinale des métriques :
// 15 857 requêtes sur des CIS distincts ne doivent créer qu'une seule série
// Prometheus.
//
// Retourne une chaîne vide tant que le routage n'a pas eu lieu, et pour une
// route inconnue.
func RouteFrom(ctx context.Context) string {
	h, _ := ctx.Value(ctxRoute).(*routeHolder)
	if h == nil {
		return ""
	}
	return h.pattern
}

// --- Identifiant de requête -------------------------------------------------

// ulidLike produit un identifiant trié dans le temps, lexicographiquement
// ordonné, au format ULID (26 caractères en base32 de Crockford).
//
// Un ULID plutôt qu'un UUID v4 : à volume de logs élevé, pouvoir trier les
// identifiants dans l'ordre chronologique sans horodatage séparé rend le
// dépouillement bien plus simple.
func ulidLike() string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	var buf [16]byte
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(buf[0:8], ms<<16)
	if _, err := rand.Read(buf[6:]); err != nil {
		// Un défaut d'entropie ne doit pas interrompre le service : la
		// partie temporelle reste discriminante à la milliseconde près.
		binary.BigEndian.PutUint64(buf[8:], ms)
	}

	out := make([]byte, 26)
	// 26 caractères × 5 bits = 130 bits ; les 2 bits de tête sont nuls.
	for i := 0; i < 26; i++ {
		bitPos := i * 5
		byteIdx := bitPos / 8
		bitOff := bitPos % 8
		var chunk uint16
		chunk = uint16(buf[byteIdx%16]) << 8
		if byteIdx+1 < 16 {
			chunk |= uint16(buf[byteIdx+1])
		}
		v := (chunk >> (11 - bitOff)) & 0x1F
		out[i] = crockford[v]
	}
	return string(out)
}

// maxClientRequestID borne la longueur d'un X-Request-Id fourni par le
// client.
const maxClientRequestID = 64

// RequestID propage un identifiant de requête, repris du client s'il est
// plausible, régénéré sinon.
//
// Reprendre l'identifiant du client permet de corréler les traces de bout en
// bout. Le valider est indispensable : un identifiant non contrôlé finirait
// dans les logs, où il pourrait injecter des sauts de ligne et falsifier une
// entrée de journal.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = ulidLike()
		}
		w.Header().Set("X-Request-Id", id)

		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		// Le porteur de route est installé ici, au plus tôt, pour que les
		// middlewares situés au-dessus du routage puissent le lire une fois
		// rempli.
		ctx = context.WithValue(ctx, ctxRoute, &routeHolder{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxClientRequestID {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !alnum && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// --- Journalisation et récupération de panique ------------------------------

// statusRecorder capture le statut et la taille écrits, pour le journal et
// les métriques.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Logger journalise chaque requête en JSON structuré et transforme toute
// panique en 500 générique.
//
// La panique est capturée **ici**, au plus près du handler : le processus
// survit, la réponse ne contient ni trace d'exécution ni message d'erreur, et
// le journal conserve tout le détail avec le request_id. C'est la seule façon
// d'avoir à la fois un diagnostic complet et une absence totale de fuite.
func Logger(log *slog.Logger, onPanic func(route string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			defer func() {
				if rv := recover(); rv != nil {
					route := RouteFrom(r.Context())
					if onPanic != nil {
						onPanic(route)
					}
					log.Error("panique dans un handler",
						"request_id", RequestIDFrom(r.Context()),
						"route", route,
						"method", r.Method,
						"panic", rv,
					)
					if rec.status == 0 {
						WriteProblem(w, r, ErrInternal,
							"Une anomalie est survenue. Communiquez le request_id au support.")
					}
					return
				}
			}()

			next.ServeHTTP(rec, r)

			// L'en-tête Authorization n'est jamais journalisé, sous aucune
			// forme : ni la clé, ni son préfixe, ni sa longueur.
			log.Info("requête",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"route", RouteFrom(r.Context()),
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// --- Authentification (T-34) ------------------------------------------------

// KeyStore contient les empreintes des clés API.
//
// Les clés ne sont jamais conservées en clair : seul leur SHA-256 est gardé.
// Argon2id serait le choix par défaut pour un mot de passe, mais une clé API
// de 32 octets d'entropie n'a pas besoin d'être ralentie contre une attaque
// par dictionnaire — il n'y a pas de dictionnaire (ADR 0004).
type KeyStore struct {
	hashes [][32]byte
	admin  *[32]byte
}

// NewKeyStore construit le magasin à partir des clés en clair.
func NewKeyStore(keys []string, adminKey string) *KeyStore {
	ks := &KeyStore{hashes: make([][32]byte, 0, len(keys))}
	for _, k := range keys {
		ks.hashes = append(ks.hashes, sha256.Sum256([]byte(k)))
	}
	if adminKey != "" {
		h := sha256.Sum256([]byte(adminKey))
		ks.admin = &h
	}
	return ks
}

// Valid indique si la clé présentée est reconnue.
//
// La comparaison est à **temps constant** et parcourt systématiquement
// l'intégralité du magasin : sortir de la boucle au premier succès ferait
// varier la durée avec la position de la clé, et donnerait à un attaquant un
// canal d'information sur son ordre.
func (ks *KeyStore) Valid(key string) bool {
	sum := sha256.Sum256([]byte(key))
	var match int
	for i := range ks.hashes {
		match |= subtle.ConstantTimeCompare(sum[:], ks.hashes[i][:])
	}
	return match == 1
}

// ValidAdmin indique si la clé présentée est la clé d'administration.
func (ks *KeyStore) ValidAdmin(key string) bool {
	if ks.admin == nil {
		return false
	}
	sum := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(sum[:], ks.admin[:]) == 1
}

// HasAdmin indique qu'une clé d'administration est configurée.
func (ks *KeyStore) HasAdmin() bool { return ks.admin != nil }

// bearerToken extrait le jeton d'un en-tête Authorization.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// Auth exige une clé API valide.
func Auth(ks *KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := bearerToken(r)
			if key == "" || !ks.Valid(key) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="meds-api"`)
				WriteProblem(w, r, ErrUnauthorized,
					"Fournissez une clé API valide dans l'en-tête Authorization: Bearer <clé>.")
				return
			}
			// La clé est propagée sous forme d'empreinte courte, jamais en
			// clair : elle sert de clé de limitation de débit, et n'a aucune
			// raison de circuler telle quelle dans le contexte.
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxAPIKey, keyFingerprint(key))))
		})
	}
}

func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// --- Limitation de débit (T-35) ---------------------------------------------

// bucket est un seau à jetons. Le remplissage est calculé à la demande
// plutôt que par une horloge de fond : sans goroutine ni minuterie, un seau
// inactif ne coûte rien.
type bucket struct {
	tokens float64
	last   time.Time
}

// shard est un fragment de la table de seaux, avec son propre verrou.
// Sharder évite qu'un unique mutex ne sérialise toutes les requêtes de
// l'API sur son chemin le plus chaud.
type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// RateLimiter applique une limitation par clé et par adresse.
type RateLimiter struct {
	shards  []*shard
	rate    float64
	burst   float64
	ttl     time.Duration
	trusted []*net.IPNet
	nowFunc func() time.Time
	maxKeys int
}

// rateLimiterShards est le nombre de fragments. Une puissance de deux permet
// un masque plutôt qu'un modulo.
const rateLimiterShards = 64

// NewRateLimiter construit un limiteur.
//
// trustedProxies liste les réseaux dont l'en-tête X-Forwarded-For est
// honoré. En dehors, l'en-tête est **ignoré** : un client qui se déclare
// lui-même derrière un proxy pourrait sinon usurper une adresse à chaque
// requête et contourner entièrement la limitation.
func NewRateLimiter(rate, burst int, trustedProxies []string) *RateLimiter {
	rl := &RateLimiter{
		shards:  make([]*shard, rateLimiterShards),
		rate:    float64(rate),
		burst:   float64(burst),
		ttl:     10 * time.Minute,
		nowFunc: time.Now,
		maxKeys: 100000,
	}
	for i := range rl.shards {
		rl.shards[i] = &shard{buckets: make(map[string]*bucket)}
	}
	for _, cidr := range trustedProxies {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			rl.trusted = append(rl.trusted, n)
		}
	}
	return rl
}

func (rl *RateLimiter) shardFor(key string) *shard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return rl.shards[h&(rateLimiterShards-1)]
}

// Allow consomme un jeton et retourne le délai d'attente conseillé en cas de
// refus.
func (rl *RateLimiter) Allow(key string) (bool, time.Duration, int) {
	now := rl.nowFunc()
	sh := rl.shardFor(key)

	sh.mu.Lock()
	b, ok := sh.buckets[key]
	if !ok {
		// Le plafond par fragment borne la mémoire sous attaque distribuée :
		// un million d'adresses distinctes ne doit pas pouvoir faire enfler
		// la table indéfiniment. Au-delà, les seaux périmés sont purgés, et
		// si cela ne suffit pas la requête est acceptée — dégrader la
		// limitation vaut mieux qu'épuiser la mémoire du processus.
		if len(sh.buckets) >= rl.maxKeys/rateLimiterShards {
			rl.sweepShard(sh, now)
		}
		b = &bucket{tokens: rl.burst, last: now}
		sh.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rl.rate
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		deficit := 1 - b.tokens
		wait := time.Duration(deficit / rl.rate * float64(time.Second))
		sh.mu.Unlock()
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait, 0
	}
	b.tokens--
	remaining := int(b.tokens)
	sh.mu.Unlock()
	return true, 0, remaining
}

// sweepShard purge les seaux inactifs. Appelée sous le verrou du fragment.
func (rl *RateLimiter) sweepShard(sh *shard, now time.Time) {
	for k, b := range sh.buckets {
		if now.Sub(b.last) > rl.ttl {
			delete(sh.buckets, k)
		}
	}
}

// Size est le nombre de seaux vivants, pour les tests et les métriques.
func (rl *RateLimiter) Size() int {
	n := 0
	for _, sh := range rl.shards {
		sh.mu.Lock()
		n += len(sh.buckets)
		sh.mu.Unlock()
	}
	return n
}

// clientKey détermine la clé de limitation : l'empreinte de clé API si la
// requête est authentifiée, l'adresse IP sinon.
func (rl *RateLimiter) clientKey(r *http.Request) string {
	if fp, ok := r.Context().Value(ctxAPIKey).(string); ok && fp != "" {
		return "k:" + fp
	}
	return "i:" + rl.clientIP(r)
}

// clientIP résout l'adresse du client, en n'honorant X-Forwarded-For que si
// la connexion vient d'un proxy déclaré de confiance.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(rl.trusted) == 0 {
		return host
	}
	ip := net.ParseIP(host)
	if ip == nil || !rl.isTrusted(ip) {
		return host
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// La première adresse est celle du client d'origine.
		if first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); net.ParseIP(first) != nil {
			return first
		}
	}
	return host
}

func (rl *RateLimiter) isTrusted(ip net.IP) bool {
	for _, n := range rl.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Limit applique la limitation de débit.
func Limit(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rl.clientKey(r)
			ok, wait, remaining := rl.Allow(key)

			h := w.Header()
			h.Set("RateLimit-Limit", strconv.Itoa(int(rl.rate)))
			if !ok {
				h.Set("RateLimit-Remaining", "0")
				h.Set("RateLimit-Reset", strconv.Itoa(int(wait.Seconds())))
				h.Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				WriteProblem(w, r, ErrRateLimited,
					"Quota dépassé. Réessayez dans "+strconv.Itoa(int(wait.Seconds()))+" seconde(s).")
				return
			}
			h.Set("RateLimit-Remaining", strconv.Itoa(remaining))
			next.ServeHTTP(w, r)
		})
	}
}

// --- En-têtes et CORS (T-37) ------------------------------------------------

// SecurityHeaders pose les en-têtes de sécurité et retire la signature du
// serveur.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Une API JSON ne charge aucune ressource : la politique la plus
		// restrictive possible est aussi la seule correcte.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// Go n'émet pas d'en-tête Server par défaut ; le supprimer
		// explicitement protège d'un intermédiaire qui en ajouterait un.
		h.Del("Server")
		next.ServeHTTP(w, r)
	})
}

// CORS applique une liste blanche d'origines.
//
// Désactivé par défaut : sans origine configurée, aucun en-tête CORS n'est
// émis, et le navigateur refuse. C'est le bon défaut pour une API à clé —
// une clé API n'a rien à faire dans du code de page web.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[strings.TrimRight(o, "/")] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			if origin == "" || len(allowed) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[origin]; !ok {
				// Origine non listée : la requête est servie, mais sans
				// en-tête CORS. C'est le navigateur qui bloque, et c'est le
				// comportement correct — refuser côté serveur donnerait un
				// oracle sur les origines autorisées.
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match")
			h.Set("Access-Control-Expose-Headers", "ETag, X-Request-Id, RateLimit-Limit, RateLimit-Remaining, Retry-After")
			h.Set("Access-Control-Max-Age", "86400")
			// Jamais Allow-Credentials : combiné à une origine reflétée, ce
			// serait exactement la faille que la liste blanche prévient.

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- Durcissement HTTP (T-36) -----------------------------------------------

// MaxBodyBytes borne le corps d'une requête. L'API n'accepte que des GET et
// un POST sans corps ; 64 Kio est déjà généreux.
const MaxBodyBytes = 64 * 1024

// RequestTimeout borne la durée d'un handler.
const RequestTimeout = 5 * time.Second

// Harden borne le corps de requête et la durée de traitement.
func Harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

		ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NegotiateContent refuse les requêtes dont l'en-tête Accept exclut le JSON.
func NegotiateContent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if accept != "" && !acceptsJSON(accept) {
			WriteProblem(w, r, ErrNotAcceptable,
				"Cette API ne produit que du application/json.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func acceptsJSON(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		media := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch media {
		case "*/*", "application/*", "application/json", "application/problem+json":
			return true
		}
	}
	return false
}

// withRoute étiquette la requête avec le patron de route.
//
// L'écriture se fait dans le porteur installé par RequestID. Aucune
// synchronisation n'est nécessaire : une requête est traitée par une seule
// goroutine, et le porteur ne sort jamais de son cycle de vie.
func withRoute(pattern string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tagRoute(pattern, r)
		h(w, r)
	}
}

func tagRoute(pattern string, r *http.Request) {
	if holder, ok := r.Context().Value(ctxRoute).(*routeHolder); ok {
		holder.pattern = pattern
	}
}

// routeTagger pose le patron de route **avant** l'authentification et la
// limitation de débit.
//
// L'ordre compte : placé après elles, le patron n'était posé que par les
// requêtes effectivement servies. Mesuré le 15/08/2026 sous `spike.js`, les
// 220 111 refus de quota étaient tous étiquetés `route="other"` — en
// production, impossible de savoir quel endpoint sature. Le refus est
// justement le moment où l'on a le plus besoin de le savoir.
func routeTagger(pattern string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tagRoute(pattern, r)
			next.ServeHTTP(w, r)
		})
	}
}

// chain compose des middlewares, le premier de la liste étant le plus
// externe.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
