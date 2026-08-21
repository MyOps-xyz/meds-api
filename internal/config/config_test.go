package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// validEnv retourne un jeu de variables d'environnement minimal et valide,
// que chaque test peut copier puis altérer pour cibler une seule règle de
// validation à la fois.
func validEnv() map[string]string {
	return map[string]string{
		"MEDS_API_KEYS": strings.Repeat("a", 32) + "," + strings.Repeat("b", 32),
	}
}

// getenvFrom imite os.LookupEnv sur une carte : une clé absente de la carte
// est une variable absente de l'environnement, une clé présente à "" est une
// variable explicitement vide. Cette distinction n'est significative que pour
// MEDS_SYNC_CRON, mais elle doit être fidèle pour que les tests de cette
// variable aient un sens.
func getenvFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(getenvFrom(validEnv()))
	if err != nil {
		t.Fatalf("Load() erreur inattendue : %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, attendu \":8080\"", cfg.Addr)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, attendu \"/data\"", cfg.DataDir)
	}
	if len(cfg.APIKeys) != 2 {
		t.Errorf("len(APIKeys) = %d, attendu 2", len(cfg.APIKeys))
	}
	if cfg.AdminKey != "" {
		t.Errorf("AdminKey = %q, attendu vide", cfg.AdminKey)
	}
	// MEDS_SYNC_CRON absente ⇒ défaut documenté « 0 4 * * * », ordonnanceur
	// actif. Le cas « présente mais vide » est couvert par
	// TestLoad_SyncCronAbsentVersusEmpty.
	if !cfg.SyncCron.Enabled || cfg.SyncCron.Raw != DefaultSyncCronExpression {
		t.Errorf("SyncCron sans variable d'environnement devrait valoir %q et être actif, obtenu %+v",
			DefaultSyncCronExpression, cfg.SyncCron)
	}
	if cfg.SyncJitter != 30*time.Minute {
		t.Errorf("SyncJitter = %v, attendu 30m", cfg.SyncJitter)
	}
	if !cfg.SyncOnStart {
		t.Error("SyncOnStart devrait valoir true par défaut")
	}
	if cfg.RateLimit != 100 {
		t.Errorf("RateLimit = %d, attendu 100", cfg.RateLimit)
	}
	if cfg.RateBurst != 200 {
		t.Errorf("RateBurst = %d, attendu 200", cfg.RateBurst)
	}
	if cfg.CORSOrigins != nil {
		t.Errorf("CORSOrigins = %v, attendu nil (CORS désactivé)", cfg.CORSOrigins)
	}
	if cfg.MaxRejectRatio != 0.01 {
		t.Errorf("MaxRejectRatio = %v, attendu 0.01", cfg.MaxRejectRatio)
	}
	if cfg.SnapshotKeep != 3 {
		t.Errorf("SnapshotKeep = %d, attendu 3", cfg.SnapshotKeep)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, attendu info", cfg.LogLevel)
	}
}

func TestLoad_MissingAPIKeys(t *testing.T) {
	t.Parallel()

	_, err := Load(getenvFrom(map[string]string{}))
	if err == nil {
		t.Fatal("Load() aurait dû échouer sans MEDS_API_KEYS")
	}
	if !strings.Contains(err.Error(), "MEDS_API_KEYS") {
		t.Errorf("message d'erreur %q ne mentionne pas MEDS_API_KEYS", err.Error())
	}
}

func TestLoad_ValidationRules(t *testing.T) {
	t.Parallel()

	longKey := strings.Repeat("a", 32)
	longKey2 := strings.Repeat("b", 32)

	tests := []struct {
		name    string
		mutate  func(env map[string]string)
		wantErr string // sous-chaîne attendue dans le message d'erreur
	}{
		{
			name:    "MEDS_ADDR sans port",
			mutate:  func(env map[string]string) { env["MEDS_ADDR"] = "localhost" },
			wantErr: "MEDS_ADDR",
		},
		{
			name:    "MEDS_ADDR port non numérique",
			mutate:  func(env map[string]string) { env["MEDS_ADDR"] = "localhost:abc" },
			wantErr: "MEDS_ADDR",
		},
		{
			name:    "MEDS_API_KEYS vide après trim",
			mutate:  func(env map[string]string) { env["MEDS_API_KEYS"] = "   " },
			wantErr: "MEDS_API_KEYS",
		},
		{
			name:    "MEDS_API_KEYS clé trop courte",
			mutate:  func(env map[string]string) { env["MEDS_API_KEYS"] = "short" },
			wantErr: "MEDS_API_KEYS",
		},
		{
			name: "MEDS_API_KEYS doublons refusés",
			mutate: func(env map[string]string) {
				env["MEDS_API_KEYS"] = longKey + "," + longKey
			},
			wantErr: "MEDS_API_KEYS",
		},
		{
			name: "MEDS_API_KEYS élément vide (virgule superflue)",
			mutate: func(env map[string]string) {
				env["MEDS_API_KEYS"] = longKey + ",," + longKey2
			},
			wantErr: "MEDS_API_KEYS",
		},
		{
			name: "MEDS_ADMIN_KEY identique à une clé API",
			mutate: func(env map[string]string) {
				env["MEDS_API_KEYS"] = longKey
				env["MEDS_ADMIN_KEY"] = longKey
			},
			wantErr: "MEDS_ADMIN_KEY",
		},
		{
			name: "MEDS_ADMIN_KEY trop courte",
			mutate: func(env map[string]string) {
				env["MEDS_ADMIN_KEY"] = "trop-court"
			},
			wantErr: "MEDS_ADMIN_KEY",
		},
		{
			name:    "MEDS_SYNC_CRON mal formé",
			mutate:  func(env map[string]string) { env["MEDS_SYNC_CRON"] = "not a cron" },
			wantErr: "MEDS_SYNC_CRON",
		},
		{
			name:    "MEDS_SYNC_JITTER non parsable",
			mutate:  func(env map[string]string) { env["MEDS_SYNC_JITTER"] = "not-a-duration" },
			wantErr: "MEDS_SYNC_JITTER",
		},
		{
			name:    "MEDS_SYNC_JITTER négatif",
			mutate:  func(env map[string]string) { env["MEDS_SYNC_JITTER"] = "-5m" },
			wantErr: "MEDS_SYNC_JITTER",
		},
		{
			name:    "MEDS_SYNC_ON_START non booléen",
			mutate:  func(env map[string]string) { env["MEDS_SYNC_ON_START"] = "maybe" },
			wantErr: "MEDS_SYNC_ON_START",
		},
		{
			name:    "MEDS_RATE_LIMIT non numérique",
			mutate:  func(env map[string]string) { env["MEDS_RATE_LIMIT"] = "abc" },
			wantErr: "MEDS_RATE_LIMIT",
		},
		{
			name:    "MEDS_RATE_LIMIT nul",
			mutate:  func(env map[string]string) { env["MEDS_RATE_LIMIT"] = "0" },
			wantErr: "MEDS_RATE_LIMIT",
		},
		{
			name:    "MEDS_RATE_LIMIT négatif",
			mutate:  func(env map[string]string) { env["MEDS_RATE_LIMIT"] = "-1" },
			wantErr: "MEDS_RATE_LIMIT",
		},
		{
			name: "MEDS_RATE_BURST inférieur à MEDS_RATE_LIMIT",
			mutate: func(env map[string]string) {
				env["MEDS_RATE_LIMIT"] = "100"
				env["MEDS_RATE_BURST"] = "50"
			},
			wantErr: "MEDS_RATE_BURST",
		},
		{
			name:    "MEDS_RATE_BURST non numérique",
			mutate:  func(env map[string]string) { env["MEDS_RATE_BURST"] = "abc" },
			wantErr: "MEDS_RATE_BURST",
		},
		{
			name:    "MEDS_CORS_ORIGINS joker refusé",
			mutate:  func(env map[string]string) { env["MEDS_CORS_ORIGINS"] = "*" },
			wantErr: "MEDS_CORS_ORIGINS",
		},
		{
			name:    "MEDS_CORS_ORIGINS sans schéma",
			mutate:  func(env map[string]string) { env["MEDS_CORS_ORIGINS"] = "example.com" },
			wantErr: "MEDS_CORS_ORIGINS",
		},
		{
			name:    "MEDS_CORS_ORIGINS avec chemin",
			mutate:  func(env map[string]string) { env["MEDS_CORS_ORIGINS"] = "https://example.com/app" },
			wantErr: "MEDS_CORS_ORIGINS",
		},
		{
			name:    "MEDS_CORS_ORIGINS schéma non http(s)",
			mutate:  func(env map[string]string) { env["MEDS_CORS_ORIGINS"] = "ftp://example.com" },
			wantErr: "MEDS_CORS_ORIGINS",
		},
		{
			name:    "MEDS_MAX_REJECT_RATIO non numérique",
			mutate:  func(env map[string]string) { env["MEDS_MAX_REJECT_RATIO"] = "abc" },
			wantErr: "MEDS_MAX_REJECT_RATIO",
		},
		{
			name:    "MEDS_MAX_REJECT_RATIO supérieur à 1",
			mutate:  func(env map[string]string) { env["MEDS_MAX_REJECT_RATIO"] = "1.5" },
			wantErr: "MEDS_MAX_REJECT_RATIO",
		},
		{
			name:    "MEDS_MAX_REJECT_RATIO négatif",
			mutate:  func(env map[string]string) { env["MEDS_MAX_REJECT_RATIO"] = "-0.1" },
			wantErr: "MEDS_MAX_REJECT_RATIO",
		},
		{
			name:    "MEDS_SNAPSHOT_KEEP non numérique",
			mutate:  func(env map[string]string) { env["MEDS_SNAPSHOT_KEEP"] = "abc" },
			wantErr: "MEDS_SNAPSHOT_KEEP",
		},
		{
			name:    "MEDS_SNAPSHOT_KEEP nul",
			mutate:  func(env map[string]string) { env["MEDS_SNAPSHOT_KEEP"] = "0" },
			wantErr: "MEDS_SNAPSHOT_KEEP",
		},
		{
			name:    "MEDS_LOG_LEVEL hors énumération",
			mutate:  func(env map[string]string) { env["MEDS_LOG_LEVEL"] = "verbose" },
			wantErr: "MEDS_LOG_LEVEL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := validEnv()
			tt.mutate(env)
			_, err := Load(getenvFrom(env))
			if err == nil {
				t.Fatalf("Load() aurait dû échouer pour %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("message d'erreur %q ne mentionne pas %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoad_ValidConfigurations(t *testing.T) {
	t.Parallel()

	longKey := strings.Repeat("a", 32)

	tests := []struct {
		name   string
		mutate func(env map[string]string)
		check  func(t *testing.T, cfg *Config)
	}{
		{
			name: "clés multiples avec espaces superflus",
			mutate: func(env map[string]string) {
				env["MEDS_API_KEYS"] = " " + longKey + " , " + strings.Repeat("b", 32) + " "
			},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.APIKeys) != 2 {
					t.Errorf("len(APIKeys) = %d, attendu 2", len(cfg.APIKeys))
				}
			},
		},
		{
			name: "MEDS_ADMIN_KEY différente et de longueur suffisante",
			mutate: func(env map[string]string) {
				env["MEDS_ADMIN_KEY"] = strings.Repeat("c", 32)
			},
			check: func(t *testing.T, cfg *Config) {
				if cfg.AdminKey == "" {
					t.Error("AdminKey aurait dû être renseignée")
				}
			},
		},
		{
			name:   "MEDS_SYNC_CRON explicitement vide désactive l'ordonnanceur",
			mutate: func(env map[string]string) { env["MEDS_SYNC_CRON"] = "" },
			check: func(t *testing.T, cfg *Config) {
				if cfg.SyncCron.Enabled {
					t.Error("SyncCron.Enabled devrait être faux quand MEDS_SYNC_CRON est vide")
				}
			},
		},
		{
			name:   "MEDS_SYNC_JITTER nul est valide",
			mutate: func(env map[string]string) { env["MEDS_SYNC_JITTER"] = "0s" },
			check: func(t *testing.T, cfg *Config) {
				if cfg.SyncJitter != 0 {
					t.Errorf("SyncJitter = %v, attendu 0", cfg.SyncJitter)
				}
			},
		},
		{
			name: "MEDS_RATE_BURST égal à MEDS_RATE_LIMIT est valide",
			mutate: func(env map[string]string) {
				env["MEDS_RATE_LIMIT"] = "50"
				env["MEDS_RATE_BURST"] = "50"
			},
			check: func(t *testing.T, cfg *Config) {
				if cfg.RateLimit != 50 || cfg.RateBurst != 50 {
					t.Errorf("RateLimit=%d RateBurst=%d, attendu 50/50", cfg.RateLimit, cfg.RateBurst)
				}
			},
		},
		{
			name: "MEDS_CORS_ORIGINS avec plusieurs origines valides",
			mutate: func(env map[string]string) {
				env["MEDS_CORS_ORIGINS"] = "https://app.example.com,https://admin.example.com"
			},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.CORSOrigins) != 2 {
					t.Errorf("len(CORSOrigins) = %d, attendu 2", len(cfg.CORSOrigins))
				}
			},
		},
		{
			name:   "MEDS_MAX_REJECT_RATIO à la borne 0",
			mutate: func(env map[string]string) { env["MEDS_MAX_REJECT_RATIO"] = "0" },
			check: func(t *testing.T, cfg *Config) {
				if cfg.MaxRejectRatio != 0 {
					t.Errorf("MaxRejectRatio = %v, attendu 0", cfg.MaxRejectRatio)
				}
			},
		},
		{
			name:   "MEDS_MAX_REJECT_RATIO à la borne 1",
			mutate: func(env map[string]string) { env["MEDS_MAX_REJECT_RATIO"] = "1" },
			check: func(t *testing.T, cfg *Config) {
				if cfg.MaxRejectRatio != 1 {
					t.Errorf("MaxRejectRatio = %v, attendu 1", cfg.MaxRejectRatio)
				}
			},
		},
		{
			name:   "MEDS_LOG_LEVEL insensible à la casse",
			mutate: func(env map[string]string) { env["MEDS_LOG_LEVEL"] = "DEBUG" },
			check: func(t *testing.T, cfg *Config) {
				if cfg.LogLevel != slog.LevelDebug {
					t.Errorf("LogLevel = %v, attendu debug", cfg.LogLevel)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := validEnv()
			tt.mutate(env)
			cfg, err := Load(getenvFrom(env))
			if err != nil {
				t.Fatalf("Load() erreur inattendue : %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

// TestLoad_AggregatesAllErrors prouve que toutes les variables fautives
// sont citées dans un seul message d'erreur, et non uniquement la première
// rencontrée.
func TestLoad_AggregatesAllErrors(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MEDS_ADDR":             "no-port-here",
		"MEDS_API_KEYS":         "short",
		"MEDS_SYNC_CRON":        "not a cron",
		"MEDS_SYNC_JITTER":      "-5m",
		"MEDS_RATE_LIMIT":       "-1",
		"MEDS_RATE_BURST":       "abc",
		"MEDS_CORS_ORIGINS":     "*",
		"MEDS_MAX_REJECT_RATIO": "5",
		"MEDS_SNAPSHOT_KEEP":    "0",
		"MEDS_LOG_LEVEL":        "verbose",
		"MEDS_SYNC_ON_START":    "maybe",
	}

	_, err := Load(getenvFrom(env))
	if err == nil {
		t.Fatal("Load() aurait dû échouer")
	}

	msg := err.Error()
	wantSubstrings := []string{
		"MEDS_ADDR",
		"MEDS_API_KEYS",
		"MEDS_SYNC_CRON",
		"MEDS_SYNC_JITTER",
		"MEDS_RATE_LIMIT",
		"MEDS_RATE_BURST",
		"MEDS_CORS_ORIGINS",
		"MEDS_MAX_REJECT_RATIO",
		"MEDS_SNAPSHOT_KEEP",
		"MEDS_LOG_LEVEL",
		"MEDS_SYNC_ON_START",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(msg, want) {
			t.Errorf("message d'erreur agrégé ne mentionne pas %q\nmessage complet :\n%s", want, msg)
		}
	}
}

// TestConfig_SecretsNeverLogged prouve qu'aucune valeur de clé API ni de
// clé d'administration n'apparaît jamais dans LogValue ou String.
func TestConfig_SecretsNeverLogged(t *testing.T) {
	t.Parallel()

	secretAPIKey1 := "sekret-api-key-aaaaaaaaaaaaaaaaaaaa"
	secretAPIKey2 := "sekret-api-key-bbbbbbbbbbbbbbbbbbbb"
	secretAdminKey := "sekret-admin-key-cccccccccccccccccc"

	env := map[string]string{
		"MEDS_API_KEYS":  secretAPIKey1 + "," + secretAPIKey2,
		"MEDS_ADMIN_KEY": secretAdminKey,
	}

	cfg, err := Load(getenvFrom(env))
	if err != nil {
		t.Fatalf("Load() erreur inattendue : %v", err)
	}

	str := cfg.String()
	logStr := slogValueString(t, cfg)

	for _, secret := range []string{secretAPIKey1, secretAPIKey2, secretAdminKey} {
		if strings.Contains(str, secret) {
			t.Errorf("String() expose un secret : %q apparaît dans %q", secret, str)
		}
		if strings.Contains(logStr, secret) {
			t.Errorf("LogValue() expose un secret : %q apparaît dans %q", secret, logStr)
		}
	}

	if !strings.Contains(str, "redacted") {
		t.Errorf("String() devrait mentionner le masquage : %q", str)
	}
	if !strings.Contains(logStr, "redacted") {
		t.Errorf("LogValue() devrait mentionner le masquage : %q", logStr)
	}
}

// slogValueString sérialise la sortie de LogValue via un handler texte
// slog, pour se rapprocher de ce qu'un exploitant verrait réellement dans
// les journaux de production.
func slogValueString(t *testing.T, cfg *Config) string {
	t.Helper()
	var sb strings.Builder
	logger := slog.New(slog.NewTextHandler(&sb, nil))
	logger.Info("configuration chargée", "config", cfg)
	return sb.String()
}

// TestLoad_SyncCronAbsentVersusEmpty verrouille la seule variable dont
// l'absence et la vacuité ont des sens opposés. Une régression ici est
// silencieuse et coûteuse : si l'absence se mettait à désactiver
// l'ordonnanceur, un déploiement par défaut servirait indéfiniment le
// snapshot de son premier démarrage sans qu'aucune erreur ne soit levée.
func TestLoad_SyncCronAbsentVersusEmpty(t *testing.T) {
	t.Parallel()

	absent := validEnv()
	cfgAbsent, err := Load(getenvFrom(absent))
	if err != nil {
		t.Fatalf("Load() sans MEDS_SYNC_CRON : erreur inattendue %v", err)
	}
	if !cfgAbsent.SyncCron.Enabled {
		t.Error("MEDS_SYNC_CRON absente devrait activer l'ordonnanceur sur le défaut documenté")
	}
	if cfgAbsent.SyncCron.Raw != DefaultSyncCronExpression {
		t.Errorf("MEDS_SYNC_CRON absente : Raw = %q, attendu %q",
			cfgAbsent.SyncCron.Raw, DefaultSyncCronExpression)
	}

	empty := validEnv()
	empty["MEDS_SYNC_CRON"] = ""
	cfgEmpty, err := Load(getenvFrom(empty))
	if err != nil {
		t.Fatalf("Load() avec MEDS_SYNC_CRON vide : erreur inattendue %v", err)
	}
	if cfgEmpty.SyncCron.Enabled {
		t.Error("MEDS_SYNC_CRON explicitement vide devrait désactiver l'ordonnanceur")
	}
}
