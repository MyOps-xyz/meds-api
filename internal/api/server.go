package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/store"
)

// Délais du serveur HTTP (docs/06-securite.md §5).
const (
	// ReadHeaderTimeout est la protection contre Slowloris : une connexion
	// qui n'a pas fini d'envoyer ses en-têtes au bout de ce délai est
	// coupée. Sans lui, quelques milliers de connexions ouvertes envoyant un
	// octet par minute suffiraient à épuiser le serveur.
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 10 * time.Second
	WriteTimeout      = 15 * time.Second
	IdleTimeout       = 60 * time.Second
	// MaxHeaderBytes borne la taille totale des en-têtes.
	MaxHeaderBytes = 32 * 1024
	// ShutdownGrace est le délai laissé aux requêtes en cours à l'arrêt.
	ShutdownGrace = 15 * time.Second
)

// Options configure le serveur.
type Options struct {
	Addr           string
	Logger         *slog.Logger
	Holder         *store.Holder
	Keys           *KeyStore
	RateLimiter    *RateLimiter
	CORSOrigins    []string
	Rank           store.RankParams
	Syncer         Syncer
	MetricsHandler http.Handler
	OnPanic        func(route string)
	// Observer est appelé après chaque requête servie, pour les métriques.
	Observer func(route, method string, status int, d time.Duration, size int)
	// InFlight encadre le traitement, pour la jauge de concurrence.
	InFlight func() func()
	// SearchObserver reçoit les mesures de recherche.
	SearchObserver func(mode string, d time.Duration, results int, fallback bool)
}

// Server est le serveur HTTP de l'API.
type Server struct {
	http *http.Server
	log  *slog.Logger
}

// NewServer construit le serveur et son routage.
func NewServer(opts Options) *Server {
	h := NewHandlers(opts.Holder, opts.Rank, opts.Syncer, opts.Keys)
	h.observeSearch = opts.SearchObserver
	mux := http.NewServeMux()

	// Les patrons de ServeMux combinent méthode et chemin (Go 1.22+) : une
	// méthode non déclarée pour un chemin existant donne un 405 sans code
	// dédié, et aucune dépendance de routage externe n'est nécessaire.
	//
	// Chaque route est étiquetée par son **patron**, jamais par le chemin
	// réel : c'est ce qui empêche 15 857 requêtes sur des CIS distincts de
	// créer 15 857 séries de métriques.
	protected := []route{
		{"GET /v1/medicaments", h.ListMedicaments},
		{"GET /v1/medicaments/{cis}", h.GetMedicament},
		{"GET /v1/medicaments/{cis}/presentations", h.GetPresentationsDe},
		{"GET /v1/medicaments/{cis}/composition", h.GetComposition},
		{"GET /v1/medicaments/{cis}/generiques", h.GetGeneriques},
		{"GET /v1/medicaments/{cis}/avis", h.GetAvis},
		{"GET /v1/medicaments/{cis}/conditions", h.GetConditions},
		{"GET /v1/medicaments/{cis}/ruptures", h.GetRupturesDe},
		{"GET /v1/presentations/{cip}", h.GetPresentation},
		{"GET /v1/substances", h.ListSubstances},
		{"GET /v1/substances/{code}", h.GetSubstance},
		{"GET /v1/substances/{code}/medicaments", h.GetSubstanceMedicaments},
		{"GET /v1/groupes-generiques", h.ListGroupes},
		{"GET /v1/groupes-generiques/{id}", h.GetGroupe},
		{"GET /v1/ruptures", h.ListRuptures},
		{"GET /v1/mitm", h.ListMITM},
		{"GET /v1/suggest", h.Suggest},
		{"GET /v1/dataset", h.GetDataset},
	}

	authChain := []func(http.Handler) http.Handler{NegotiateContent}
	if opts.Keys != nil {
		authChain = append(authChain, Auth(opts.Keys))
	}
	if opts.RateLimiter != nil {
		// La limitation vient **après** l'authentification : la clé API est
		// une bien meilleure identité que l'adresse IP, dont plusieurs
		// clients peuvent partager la sortie.
		authChain = append(authChain, Limit(opts.RateLimiter))
	}

	for _, route := range protected {
		// Le marquage de route précède l'authentification et la limitation :
		// un 401 ou un 429 doit porter la route qu'il refuse.
		mws := append([]func(http.Handler) http.Handler{routeTagger(route.pattern)}, authChain...)
		mux.Handle(route.pattern, chain(http.HandlerFunc(route.handler), mws...))
	}

	// Le même chemin est aussi enregistré **sans verbe**, associé à un 405.
	// Sans cela, la route attrape-tout ci-dessous capterait DELETE
	// /v1/medicaments et répondrait 404 : le client conclurait que la
	// ressource n'existe pas, alors que c'est sa méthode qui est refusée.
	// ServeMux préfère toujours le patron le plus spécifique, donc « GET
	// /v1/medicaments » l'emporte pour un GET et ne laisse à cette entrée
	// que les autres méthodes.
	for _, path := range barePaths(protected) {
		mux.Handle(path, withRoute(path, methodNotAllowed))
	}

	// Routes d'exploitation, exemptes d'authentification. /metrics est
	// restreint au réseau interne par le frontal, jamais par l'application :
	// une application qui filtre par adresse source se trompe de couche et
	// se fait tromper par le premier NAT venu (deploy/edge/README.md).
	mux.Handle("GET /openapi.json", withRoute("GET /openapi.json", h.OpenAPI))
	mux.Handle("GET /docs", withRoute("GET /docs", h.Docs))
	mux.Handle("GET /healthz", withRoute("GET /healthz", h.Healthz))
	mux.Handle("GET /readyz", withRoute("GET /readyz", h.Readyz))
	if opts.MetricsHandler != nil {
		mux.Handle("GET /metrics", withRoute("GET /metrics", opts.MetricsHandler.ServeHTTP))
	}
	mux.Handle("POST /admin/sync", chain(withRoute("POST /admin/sync", h.AdminSync), Harden))

	// Toute autre route : 404 au format RFC 9457, jamais la page texte par
	// défaut de net/http.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, ErrNotFound, "Cette route n'existe pas. Consultez /openapi.json.")
	}))

	global := []func(http.Handler) http.Handler{
		RequestID,
		Logger(opts.Logger, opts.OnPanic),
		SecurityHeaders,
		Harden,
	}
	if len(opts.CORSOrigins) > 0 {
		global = append(global, CORS(opts.CORSOrigins))
	}
	if opts.Observer != nil {
		global = append(global, observe(opts.Observer, opts.InFlight))
	}

	return &Server{
		log: opts.Logger,
		http: &http.Server{
			Addr:              opts.Addr,
			Handler:           chain(mux, global...),
			ReadHeaderTimeout: ReadHeaderTimeout,
			ReadTimeout:       ReadTimeout,
			WriteTimeout:      WriteTimeout,
			IdleTimeout:       IdleTimeout,
			MaxHeaderBytes:    MaxHeaderBytes,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		},
	}
}

// route associe un patron « VERBE /chemin » à son handler. Le type est nommé
// pour que la table et les fonctions qui la parcourent partagent une seule
// définition de sa forme.
type route struct {
	pattern string
	handler http.HandlerFunc
}

// barePaths extrait les chemins distincts d'une table de routes, verbe
// retiré.
func barePaths(routes []route) []string {
	seen := make(map[string]struct{}, len(routes))
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		parts := strings.SplitN(r.pattern, " ", 2)
		if len(parts) != 2 {
			continue
		}
		if _, dup := seen[parts[1]]; dup {
			continue
		}
		seen[parts[1]] = struct{}{}
		out = append(out, parts[1])
	}
	return out
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET")
	WriteProblem(w, r, ErrMethodNotAllowed,
		"Cette ressource n'accepte que la méthode GET.")
}

// observe mesure chaque requête pour les métriques.
//
// Le middleware est placé **à l'intérieur** de RequestID et du routage, pour
// que RouteFrom retourne le patron de route et non une chaîne vide : c'est
// toute la différence entre 18 séries temporelles et une par code CIS.
func observe(
	f func(route, method string, status int, d time.Duration, size int),
	inFlight func() func(),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if inFlight != nil {
				defer inFlight()()
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			f(RouteFrom(r.Context()), r.Method, status, time.Since(start), rec.bytes)
		})
	}
}

// Handler expose le handler racine, pour les tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// ListenAndServe démarre le serveur.
func (s *Server) ListenAndServe() error { return s.http.ListenAndServe() }

// Shutdown arrête le serveur en laissant les requêtes en cours se terminer.
//
// Le délai de grâce est ce qui rend un déploiement transparent : le serveur
// cesse d'accepter de nouvelles connexions, laisse finir celles en vol, et ne
// coupe brutalement que si l'une d'elles dépasse le délai.
func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, ShutdownGrace)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}
