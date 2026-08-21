package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// minAPIKeyLength est la longueur minimale acceptée pour une clé API ou une
// clé d'administration. 32 caractères garantissent une entropie suffisante
// pour rendre toute énumération hors de portée (docs/06-securite.md §3.1) ;
// ce n'est volontairement pas une exigence de format (préfixe, alphabet),
// seulement de longueur, l'appelant restant libre du schéma de génération.
const minAPIKeyLength = 32

// Valeurs par défaut appliquées lorsque la variable d'environnement
// correspondante est absente ou vide. Chacune est documentée dans
// docs/02-architecture.md §7.
//
// MEDS_SYNC_CRON obéit à la même règle, avec une nuance : seule son absence
// déclenche le défaut ; une valeur explicitement vide désactive
// l'ordonnanceur — voir le commentaire de Load à ce sujet.
const (
	defaultAddr           = ":8080"
	defaultDataDir        = "/data"
	defaultSyncJitter     = "30m"
	defaultSyncOnStart    = "true"
	defaultRateLimit      = "100"
	defaultRateBurst      = "200"
	defaultMaxRejectRatio = "0.01"
	defaultSnapshotKeep   = "3"
	defaultLogLevel       = "info"
)

// DefaultSyncCronExpression est l'expression cron appliquée lorsque
// MEDS_SYNC_CRON est absente de l'environnement (« quotidien à 04:00 »,
// docs/02-architecture.md §7). Elle est exportée pour que les gabarits de
// déploiement (Helm, Compose…) et les tests puissent la référencer sans
// dupliquer la chaîne littérale.
const DefaultSyncCronExpression = "0 4 * * *"

// Config regroupe l'intégralité de la configuration du service, déjà
// validée et typée : aucune valeur invalide ne peut être représentée par
// cette structure une fois retournée par Load.
type Config struct {
	// Addr est l'adresse d'écoute du serveur HTTP (MEDS_ADDR).
	Addr string
	// DataDir est le répertoire contenant les snapshots (MEDS_DATA_DIR).
	DataDir string
	// APIKeys contient les clés API en clair, dédupliquées et validées.
	// Ne jamais journaliser ce champ directement : utiliser LogValue/String.
	APIKeys []string
	// AdminKey est la clé requise pour POST /admin/sync. Une chaîne vide
	// signifie que l'endpoint d'administration est désactivé.
	AdminKey string
	// SyncCron décrit la planification de synchronisation. SyncCron.Enabled
	// vaut faux si MEDS_SYNC_CRON est vide : l'ordonnanceur est alors
	// désactivé, ce qui est un état valide (instance de lecture seule dans
	// un déploiement à volume partagé, voir docs/02-architecture.md §8).
	SyncCron CronSchedule
	// SyncJitter est le décalage aléatoire maximal appliqué avant chaque
	// synchronisation planifiée (MEDS_SYNC_JITTER).
	SyncJitter time.Duration
	// SyncOnStart déclenche une synchronisation si aucun snapshot n'est
	// présent au démarrage (MEDS_SYNC_ON_START).
	SyncOnStart bool
	// RateLimit est le nombre de requêtes par seconde autorisées par clé
	// API (MEDS_RATE_LIMIT).
	RateLimit int
	// RateBurst est la rafale autorisée au-delà de RateLimit
	// (MEDS_RATE_BURST) ; toujours >= RateLimit.
	RateBurst int
	// CORSOrigins liste les origines autorisées ; une liste vide désactive
	// CORS (MEDS_CORS_ORIGINS).
	CORSOrigins []string
	// MaxRejectRatio est le taux de rejet au-delà duquel une ingestion
	// échoue plutôt que d'être publiée (MEDS_MAX_REJECT_RATIO), dans [0,1].
	MaxRejectRatio float64
	// SnapshotKeep est le nombre de snapshots conservés sur disque
	// (MEDS_SNAPSHOT_KEEP), au moins 1.
	SnapshotKeep int
	// LogLevel est le niveau de verbosité des journaux (MEDS_LOG_LEVEL).
	LogLevel slog.Level
}

// Load construit une Config à partir d'une fonction d'accès à
// l'environnement (typiquement os.Getenv), avec validation stricte : toute
// variable absente ou mal formée fait échouer le chargement, sans jamais
// substituer silencieusement une valeur par défaut à un paramètre de
// sécurité (MEDS_API_KEYS notamment).
//
// lookupEnv est injecté plutôt qu'un appel direct à os.LookupEnv afin que
// les tests restent hermétiques (aucun recours à t.Setenv ni à un état de
// processus partagé entre tests parallèles).
//
// C'est os.LookupEnv et non os.Getenv qui est attendu ici, parce que
// MEDS_SYNC_CRON exige de distinguer « variable absente » de « variable
// présente mais vide ». Les deux formes sont documentées et signifient des
// choses opposées : absente, l'ordonnanceur tourne selon
// DefaultSyncCronExpression (docs/02-architecture.md §7) ; explicitement
// vide, il est désactivé — ce que docs/02-architecture.md §8 exige pour les
// instances secondaires d'un déploiement à volume partagé. Avec os.Getenv,
// les deux cas se confondent en "" et il faut sacrifier l'un des deux : un
// démarrage par défaut qui ne synchronise jamais, ou une désactivation
// devenue inexprimable. Aucun des deux n'est acceptable, d'où cette
// signature.
//
// Toutes les variables invalides sont rapportées ensemble via errors.Join :
// un exploitant découvre en un seul démarrage l'ensemble des corrections à
// apporter, plutôt qu'une à la fois au fil des redémarrages successifs.
func Load(lookupEnv func(string) (string, bool)) (*Config, error) {
	var errs []error
	cfg := &Config{}

	// La quasi-totalité des variables traitent « absente » et « vide » de
	// façon identique : toutes deux retombent sur la valeur par défaut.
	// getenv aplatit donc les deux cas, et seul MEDS_SYNC_CRON consulte
	// lookupEnv directement.
	getenv := func(key string) string {
		v, _ := lookupEnv(key)
		return v
	}

	// envOr factorise le préambule commun à presque toutes les variables :
	// lire, élaguer, retomber sur le défaut si le résultat est vide.
	envOr := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	// --- MEDS_ADDR ---
	addrRaw := envOr("MEDS_ADDR", defaultAddr)
	if _, port, err := net.SplitHostPort(addrRaw); err != nil {
		errs = append(errs, fmt.Errorf(
			"MEDS_ADDR: valeur %q invalide : adresse \"host:port\" attendue (ex. \":8080\") : %w",
			addrRaw, err,
		))
	} else if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		errs = append(errs, fmt.Errorf(
			"MEDS_ADDR: port %q invalide : entier entre 0 et 65535 attendu", port,
		))
	} else {
		cfg.Addr = addrRaw
	}

	// --- MEDS_DATA_DIR ---
	dataDir := envOr("MEDS_DATA_DIR", defaultDataDir)
	cfg.DataDir = dataDir

	// --- MEDS_API_KEYS (requis, jamais de défaut) ---
	apiKeys, err := parseAPIKeys(getenv("MEDS_API_KEYS"))
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.APIKeys = apiKeys
	}

	// --- MEDS_ADMIN_KEY (optionnel) ---
	adminKey := strings.TrimSpace(getenv("MEDS_ADMIN_KEY"))
	if adminKey != "" {
		if len(adminKey) < minAPIKeyLength {
			errs = append(errs, fmt.Errorf(
				"MEDS_ADMIN_KEY: longueur insuffisante (%d caractères) : au moins %d caractères attendus",
				len(adminKey), minAPIKeyLength,
			))
		}
		for _, k := range apiKeys {
			if k == adminKey {
				errs = append(errs, errors.New(
					"MEDS_ADMIN_KEY: doit être différente de toute clé listée dans MEDS_API_KEYS",
				))
				break
			}
		}
	}
	cfg.AdminKey = adminKey

	// --- MEDS_SYNC_CRON (voir le commentaire de Load ci-dessus) ---
	// Absente ⇒ défaut documenté ; présente mais vide ⇒ ordonnanceur
	// désactivé, ce qui est un état valide et non une erreur.
	cronRaw, cronSet := lookupEnv("MEDS_SYNC_CRON")
	if !cronSet {
		cronRaw = DefaultSyncCronExpression
	}
	cron, err := ParseCron(cronRaw)
	if err != nil {
		errs = append(errs, fmt.Errorf("MEDS_SYNC_CRON: %w", err))
	} else {
		cfg.SyncCron = cron
	}

	// --- MEDS_SYNC_JITTER ---
	jitterRaw := envOr("MEDS_SYNC_JITTER", defaultSyncJitter)
	if d, err := time.ParseDuration(jitterRaw); err != nil {
		errs = append(errs, fmt.Errorf(
			"MEDS_SYNC_JITTER: valeur %q invalide : durée Go attendue (ex. \"30m\") : %w", jitterRaw, err,
		))
	} else if d < 0 {
		errs = append(errs, fmt.Errorf(
			"MEDS_SYNC_JITTER: valeur %q invalide : une durée négative n'a pas de sens", jitterRaw,
		))
	} else {
		cfg.SyncJitter = d
	}

	// --- MEDS_SYNC_ON_START ---
	onStartRaw := envOr("MEDS_SYNC_ON_START", defaultSyncOnStart)
	if b, err := strconv.ParseBool(onStartRaw); err != nil {
		errs = append(errs, fmt.Errorf(
			"MEDS_SYNC_ON_START: valeur %q invalide : booléen attendu (true/false)", onStartRaw,
		))
	} else {
		cfg.SyncOnStart = b
	}

	// --- MEDS_RATE_LIMIT / MEDS_RATE_BURST (validation croisée) ---
	rateLimitRaw := envOr("MEDS_RATE_LIMIT", defaultRateLimit)
	rateLimit, rlErr := strconv.Atoi(rateLimitRaw)
	rateLimitValid := rlErr == nil && rateLimit > 0
	if !rateLimitValid {
		errs = append(errs, fmt.Errorf(
			"MEDS_RATE_LIMIT: valeur %q invalide : entier strictement positif attendu", rateLimitRaw,
		))
	} else {
		cfg.RateLimit = rateLimit
	}

	rateBurstRaw := envOr("MEDS_RATE_BURST", defaultRateBurst)
	rateBurst, rbErr := strconv.Atoi(rateBurstRaw)
	switch {
	case rbErr != nil:
		errs = append(errs, fmt.Errorf(
			"MEDS_RATE_BURST: valeur %q invalide : entier attendu", rateBurstRaw,
		))
	case rateLimitValid && rateBurst < rateLimit:
		errs = append(errs, fmt.Errorf(
			"MEDS_RATE_BURST: valeur %d invalide : doit être supérieure ou égale à MEDS_RATE_LIMIT (%d)",
			rateBurst, rateLimit,
		))
	default:
		cfg.RateBurst = rateBurst
	}

	// --- MEDS_CORS_ORIGINS ---
	origins, err := parseCORSOrigins(strings.TrimSpace(getenv("MEDS_CORS_ORIGINS")))
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.CORSOrigins = origins
	}

	// --- MEDS_MAX_REJECT_RATIO ---
	ratioRaw := envOr("MEDS_MAX_REJECT_RATIO", defaultMaxRejectRatio)
	if f, err := strconv.ParseFloat(ratioRaw, 64); err != nil {
		errs = append(errs, fmt.Errorf(
			"MEDS_MAX_REJECT_RATIO: valeur %q invalide : nombre décimal attendu", ratioRaw,
		))
	} else if f < 0 || f > 1 {
		errs = append(errs, fmt.Errorf(
			"MEDS_MAX_REJECT_RATIO: valeur %v invalide : doit être comprise entre 0 et 1", f,
		))
	} else {
		cfg.MaxRejectRatio = f
	}

	// --- MEDS_SNAPSHOT_KEEP ---
	keepRaw := envOr("MEDS_SNAPSHOT_KEEP", defaultSnapshotKeep)
	if n, err := strconv.Atoi(keepRaw); err != nil {
		errs = append(errs, fmt.Errorf(
			"MEDS_SNAPSHOT_KEEP: valeur %q invalide : entier attendu", keepRaw,
		))
	} else if n < 1 {
		errs = append(errs, fmt.Errorf(
			"MEDS_SNAPSHOT_KEEP: valeur %d invalide : au moins 1 attendu", n,
		))
	} else {
		cfg.SnapshotKeep = n
	}

	// --- MEDS_LOG_LEVEL ---
	levelRaw := envOr("MEDS_LOG_LEVEL", defaultLogLevel)
	lvl, err := parseLogLevel(levelRaw)
	if err != nil {
		errs = append(errs, err)
	} else {
		cfg.LogLevel = lvl
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// parseAPIKeys découpe et valide MEDS_API_KEYS. Aucune valeur de clé
// n'apparaît jamais dans les erreurs retournées : seules la position dans
// la liste et la longueur sont mentionnées.
func parseAPIKeys(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New(
			"MEDS_API_KEYS: variable requise et absente : au moins une clé API attendue, séparées par des virgules",
		)
	}

	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	firstSeenAt := make(map[string]int, len(parts))
	var errs []error

	for i, p := range parts {
		k := strings.TrimSpace(p)
		pos := i + 1
		switch {
		case k == "":
			errs = append(errs, fmt.Errorf(
				"MEDS_API_KEYS: clé vide à la position %d : vérifier les virgules superflues", pos,
			))
		case len(k) < minAPIKeyLength:
			errs = append(errs, fmt.Errorf(
				"MEDS_API_KEYS: clé à la position %d trop courte (%d caractères) : au moins %d caractères attendus",
				pos, len(k), minAPIKeyLength,
			))
		default:
			if first, dup := firstSeenAt[k]; dup {
				errs = append(errs, fmt.Errorf(
					"MEDS_API_KEYS: clé dupliquée (positions %d et %d)", first, pos,
				))
				continue
			}
			firstSeenAt[k] = pos
			keys = append(keys, k)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return keys, nil
}

// parseCORSOrigins découpe et valide MEDS_CORS_ORIGINS. Une chaîne vide
// désactive CORS (nil, pas une erreur). Chaque origine doit être une URL
// absolue avec schéma http(s) et hôte, sans chemin, requête ni fragment ;
// le joker "*" est explicitement refusé (docs/06-securite.md §6).
func parseCORSOrigins(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	var errs []error

	for i, p := range parts {
		o := strings.TrimSpace(p)
		pos := i + 1
		switch o {
		case "":
			errs = append(errs, fmt.Errorf("MEDS_CORS_ORIGINS: origine vide à la position %d", pos))
		case "*":
			errs = append(errs, fmt.Errorf(
				"MEDS_CORS_ORIGINS: origine %q refusée : le joker '*' n'est pas autorisé (docs/06-securite.md §6)", o,
			))
		default:
			u, err := url.Parse(o)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("MEDS_CORS_ORIGINS: origine %q invalide : %w", o, err))
			case u.Scheme != "http" && u.Scheme != "https":
				errs = append(errs, fmt.Errorf(
					"MEDS_CORS_ORIGINS: origine %q invalide : schéma http ou https requis", o,
				))
			case u.Host == "":
				errs = append(errs, fmt.Errorf(
					"MEDS_CORS_ORIGINS: origine %q invalide : hôte requis (ex. \"https://example.com\")", o,
				))
			case u.Path != "" || u.RawQuery != "" || u.Fragment != "":
				errs = append(errs, fmt.Errorf(
					"MEDS_CORS_ORIGINS: origine %q invalide : ni chemin, ni requête, ni fragment autorisés", o,
				))
			default:
				origins = append(origins, o)
			}
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return origins, nil
}

// parseLogLevel valide MEDS_LOG_LEVEL contre l'énumération admise.
func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf(
			"MEDS_LOG_LEVEL: valeur %q invalide : attendu parmi debug, info, warn, error", raw,
		)
	}
}

// LogValue implémente slog.LogValuer : elle permet de journaliser la
// configuration complète au démarrage (slog.Any("config", cfg)) sans jamais
// exposer une clé API ou la clé d'administration en clair, y compris dans
// des journaux structurés capturés par un tiers.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("data_dir", c.DataDir),
		slog.String("api_keys", fmt.Sprintf("[redacted] (%d clés)", len(c.APIKeys))),
		slog.String("admin_key", redactedAdminKey(c.AdminKey)),
		slog.String("sync_cron", c.SyncCron.Raw),
		slog.Bool("sync_enabled", c.SyncCron.Enabled),
		slog.Duration("sync_jitter", c.SyncJitter),
		slog.Bool("sync_on_start", c.SyncOnStart),
		slog.Int("rate_limit", c.RateLimit),
		slog.Int("rate_burst", c.RateBurst),
		slog.Any("cors_origins", c.CORSOrigins),
		slog.Float64("max_reject_ratio", c.MaxRejectRatio),
		slog.Int("snapshot_keep", c.SnapshotKeep),
		slog.String("log_level", c.LogLevel.String()),
	)
}

// String rend une représentation textuelle de la configuration, avec le
// même masquage des secrets que LogValue. Utile pour un affichage de
// diagnostic (fmt.Println, --help) hors du chemin de journalisation
// structurée.
// Elle délègue à LogValue plutôt que de reformater les champs : deux listes
// à tenir en phase, c'est la garantie qu'un champ ajouté n'apparaîtra que
// dans l'une des deux — et le masquage des secrets est précisément ce qu'on
// ne veut pas voir diverger.
func (c *Config) String() string {
	return "Config" + c.LogValue().String()
}

// redactedAdminKey ne révèle jamais la clé d'administration, seulement si
// elle est configurée.
func redactedAdminKey(k string) string {
	if k == "" {
		return "(non configurée)"
	}
	return "[redacted]"
}
