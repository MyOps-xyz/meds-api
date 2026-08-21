// Commande meds-api : serveur HTTP exposant la Base de Données Publique des
// Médicaments, avec son ordonnanceur de synchronisation intégré.
//
// Le câblage complet est décrit dans docs/02-architecture.md §4.1.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/api"
	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/config"
	"github.com/MyOps-xyz/meds-api/internal/metrics"
	"github.com/MyOps-xyz/meds-api/internal/pipeline"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

// Injectées à la compilation via -ldflags (voir T-46).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			printVersion()
			return
		case "healthcheck":
			// Sous-commande de healthcheck du conteneur : l'image distroless
			// n'a ni curl ni wget, le binaire se sonde donc lui-même
			// (docs/08-deploiement-docker.md §3).
			os.Exit(healthcheck())
		}
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meds-api :", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		// La configuration est validée avant tout le reste, et un défaut y
		// est fatal : un service de données de santé ne doit jamais démarrer
		// avec un paramètre de sécurité substitué en silence.
		return fmt.Errorf("configuration : %w", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("démarrage", "version", version, "commit", commit, "config", cfg)

	validation := bdpm.DefaultValidationOptions()
	validation.MaxRejectRatio = cfg.MaxRejectRatio

	met := metrics.New()
	holder := store.NewHolder()

	// La fraîcheur est calculée à la collecte, depuis le Store réellement
	// publié : une jauge écrite au moment de la bascule vieillirait faux
	// entre deux synchronisations.
	met.SetAgeSource(func() (time.Time, bool) {
		s := holder.Load()
		if s == nil {
			return time.Time{}, false
		}
		t, err := time.Parse(time.RFC3339, s.Manifest.GeneratedAt)
		return t, err == nil
	})
	pipe := pipeline.New(pipeline.Options{
		DataDir:    cfg.DataDir,
		Generator:  "meds-api/" + version,
		Keep:       cfg.SnapshotKeep,
		Holder:     holder,
		Logger:     log,
		Validation: validation,
		Metrics:    met,
	})

	// Un snapshot déjà présent est chargé immédiatement : le service est
	// prêt en moins d'une seconde après un redémarrage, sans retélécharger.
	if err := pipe.LoadCurrent(); err != nil {
		log.Error("chargement du snapshot existant impossible", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Première synchronisation si aucun snapshot n'est publié.
	if !holder.Ready() && cfg.SyncOnStart {
		go func() {
			if _, err := pipe.Run(ctx); err != nil {
				log.Error("synchronisation initiale en échec", "error", err)
			}
		}()
	}

	sched := pipeline.NewScheduler(cfg.SyncCron, cfg.SyncJitter, pipe, log)
	go sched.Run(ctx)

	srv := api.NewServer(api.Options{
		Addr:           cfg.Addr,
		Logger:         log,
		Holder:         holder,
		Keys:           api.NewKeyStore(cfg.APIKeys, cfg.AdminKey),
		RateLimiter:    api.NewRateLimiter(cfg.RateLimit, cfg.RateBurst, nil),
		CORSOrigins:    cfg.CORSOrigins,
		Rank:           store.DefaultRankParams(),
		Syncer:         pipe,
		MetricsHandler: met.Handler(),
		Observer:       met.ObserveRequest,
		OnPanic:        met.ObservePanic,
		InFlight: func() func() {
			met.IncInFlight()
			return met.DecInFlight
		},
		SearchObserver: met.ObserveSearch,
	})

	errCh := make(chan error, 1)
	go func() {
		log.Info("écoute HTTP", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("arrêt demandé, drainage des requêtes en cours")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), api.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("arrêt : %w", err)
	}
	log.Info("arrêt terminé")
	return nil
}

// healthcheck interroge la sonde de vivacité locale.
// healthcheckDefaultAddr doit rester aligné sur le défaut de MEDS_ADDR dans
// internal/config.
const healthcheckDefaultAddr = ":8080"

// healthcheckURL dérive l'URL de sonde de l'adresse d'écoute du serveur.
//
// Seul le port est repris tel quel. Une adresse d'écoute désigne une
// *interface*, pas une destination : ":8080", "0.0.0.0:8080" et "[::]:8080"
// veulent tous dire « toutes les interfaces », et se joignent depuis le même
// conteneur par la boucle locale. Concaténer l'adresse brute, comme le
// faisait la première version, donnait "http://127.0.0.10.0.0.0:8080" pour la
// deuxième forme — une sonde qui échoue sans que rien ne soit en panne.
//
// Une adresse qui nomme une interface précise est en revanche conservée : si
// le serveur n'écoute que sur 10.0.0.5, c'est là qu'il faut le joindre.
func healthcheckURL(addr string) (string, error) {
	if addr == "" {
		addr = healthcheckDefaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("MEDS_ADDR %q : adresse \"host:port\" attendue : %w", addr, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

func healthcheck() int {
	url, err := healthcheckURL(os.Getenv("MEDS_ADDR"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck :", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	// L'adresse vient de l'environnement du conteneur, pas d'une requête :
	// c'est la même variable que celle sur laquelle le serveur écoute, et
	// l'appel vise explicitement la boucle locale. Il n'y a pas de surface
	// de falsification — qui contrôle l'environnement contrôle déjà le
	// processus.
	resp, err := client.Get(url) //nolint:gosec // adresse locale issue de la configuration du processus
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck :", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck : statut", resp.StatusCode)
		return 1
	}
	return 0
}

func printVersion() {
	fmt.Printf("meds-api %s (commit %s, construit le %s)\n", version, commit, date)
	if info, ok := debug.ReadBuildInfo(); ok {
		fmt.Printf("go %s\n", info.GoVersion)
	}
}
