// Package pipeline orchestre une synchronisation complète : téléchargement,
// parsing, validation, dérivation, écriture du snapshot et publication du
// Store (docs/09-pipeline-mise-a-jour.md).
package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/snapshot"
	"github.com/MyOps-xyz/meds-api/internal/store"
)

// Options configure le pipeline.
type Options struct {
	DataDir    string
	Generator  string
	Keep       int
	Holder     *store.Holder
	Logger     *slog.Logger
	Client     *bdpm.Client
	Validation bdpm.ValidationOptions
	// Force réingère même si le hash source est inchangé.
	Force bool
	// DryRun exécute tout sauf l'écriture du snapshot et la publication.
	DryRun bool
	// Metrics reçoit les mesures d'ingestion. Peut être nil — la CLI
	// bdpm-sync n'expose aucune métrique.
	Metrics Recorder
}

// Recorder est le contrat d'instrumentation du pipeline, défini côté
// consommateur pour que celui-ci ne dépende pas du paquet metrics — et donc
// pas de Prometheus, que la CLI n'embarque pas.
type Recorder interface {
	SyncStarted()
	SyncFinished(result string, d time.Duration, at time.Time)
	PublishDataset(s DatasetSnapshot)
}

// DatasetSnapshot décrit le jeu de données publié.
type DatasetSnapshot = struct {
	Version    string
	Hash       string
	Counts     map[string]int
	Quarantine map[string]int
	OutOfScope map[string]int
	BuildTime  time.Duration
}

// Report est le compte rendu d'une synchronisation
// (docs/09-pipeline-mise-a-jour.md §6).
type Report struct {
	SyncID       string         `json:"sync_id"`
	StartedAt    time.Time      `json:"started_at"`
	DurationMS   int64          `json:"duration_ms"`
	Changed      bool           `json:"changed"`
	Hash         string         `json:"hash"`
	PreviousHash string         `json:"previous_hash,omitempty"`
	Counts       map[string]int `json:"counts"`
	Delta        map[string]int `json:"delta,omitempty"`
	Quarantine   int            `json:"quarantine"`
	OutOfScope   int            `json:"out_of_scope"`
	RejectRatio  float64        `json:"reject_ratio"`
	StoreBuildMS int64          `json:"store_build_ms"`
	Warnings     []string       `json:"warnings,omitempty"`
	Status       string         `json:"status"`
	Error        string         `json:"error,omitempty"`
}

// Pipeline exécute les synchronisations, une à la fois.
type Pipeline struct {
	opts Options

	// running garantit qu'une seule synchronisation s'exécute à la fois.
	// Deux déclenchements simultanés — le cron et un POST /admin/sync — ne
	// doivent pas télécharger deux fois 22 Mo ni écrire concurremment le
	// même répertoire.
	mu      sync.Mutex
	running bool

	lastReport atomic[Report]
}

// atomic est une valeur protégée par un verrou en lecture/écriture, plus
// simple ici qu'un atomic.Pointer pour une structure copiée.
type atomic[T any] struct {
	mu sync.RWMutex
	v  T
	ok bool
}

func (a *atomic[T]) set(v T) {
	a.mu.Lock()
	a.v, a.ok = v, true
	a.mu.Unlock()
}

func (a *atomic[T]) get() (T, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.v, a.ok
}

// New construit un pipeline.
func New(opts Options) *Pipeline {
	if opts.Client == nil {
		opts.Client = bdpm.NewClient(opts.Generator)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Validation.MaxRejectRatio == 0 {
		opts.Validation = bdpm.DefaultValidationOptions()
	}
	return &Pipeline{opts: opts}
}

// ErrInProgress signale qu'une synchronisation est déjà en cours.
var ErrInProgress = errors.New("une synchronisation est déjà en cours")

// LastReport retourne le compte rendu de la dernière synchronisation.
func (p *Pipeline) LastReport() (Report, bool) { return p.lastReport.get() }

// Running indique qu'une synchronisation est en cours.
func (p *Pipeline) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Trigger démarre une synchronisation en arrière-plan et retourne son
// identifiant. Il implémente api.Syncer.
//
// L'exécution est asynchrone parce qu'une ingestion prend une trentaine de
// secondes : tenir la connexion HTTP ouverte pendant ce temps inviterait aux
// dépassements de délai côté proxy, et donnerait à l'appelant l'illusion que
// l'opération a échoué alors qu'elle se poursuit.
func (p *Pipeline) Trigger() (string, error) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return "", ErrInProgress
	}
	p.running = true
	p.mu.Unlock()

	id := syncID()
	go func() {
		defer func() {
			p.mu.Lock()
			p.running = false
			p.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if _, err := p.run(ctx, id); err != nil {
			p.opts.Logger.Error("synchronisation en échec", "sync_id", id, "error", err)
		}
	}()
	return id, nil
}

// Run exécute une synchronisation de façon synchrone.
func (p *Pipeline) Run(ctx context.Context) (Report, error) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return Report{}, ErrInProgress
	}
	p.running = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	return p.run(ctx, syncID())
}

func (p *Pipeline) run(ctx context.Context, id string) (Report, error) {
	start := time.Now()
	rep := Report{SyncID: id, StartedAt: start, Status: "failure"}

	if p.opts.Metrics != nil {
		p.opts.Metrics.SyncStarted()
	}

	finish := func(err error) (Report, error) {
		rep.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			rep.Error = err.Error()
		} else if rep.Status == "" || rep.Status == "failure" {
			rep.Status = "success"
		}
		p.lastReport.set(rep)
		if p.opts.Metrics != nil {
			// `unchanged` est distingué de `success` : la plupart des jours,
			// l'issue normale est que la source n'a pas bougé.
			result := rep.Status
			if err == nil && !rep.Changed {
				result = "unchanged"
			}
			p.opts.Metrics.SyncFinished(result, time.Since(start), time.Now())
		}
		return rep, err
	}

	// ① Téléchargement.
	res, err := p.opts.Client.FetchAll(ctx, bdpm.DefaultFiles())
	if err != nil {
		return finish(fmt.Errorf("téléchargement : %w", err))
	}
	rep.Hash = res.Hash

	// ② Détection de changement. L'ANSM ne servant ni ETag ni
	// Last-Modified, le hash du contenu est le seul signal disponible ; il a
	// l'avantage d'être exact.
	previous := p.currentManifest()
	if previous != nil {
		rep.PreviousHash = previous.Hash
		if previous.Hash == res.Hash && !p.opts.Force {
			rep.Changed = false
			rep.Counts = previous.Counts
			p.opts.Logger.Info("source inchangée, ingestion évitée",
				"sync_id", id, "hash", res.Hash)
			return finish(nil)
		}
		p.opts.Validation.PreviousCounts = previous.Counts
	}
	rep.Changed = true

	// ③ à ⑤ Décodage, parsing, normalisation.
	q := bdpm.NewQuarantine()
	ds, err := bdpm.ParseAll(res, q)
	if err != nil {
		return finish(fmt.Errorf("parsing : %w", err))
	}

	// ⑥ Validation et dérivation.
	bdpm.CheckReferentialIntegrity(ds, q)
	ds.Derive()
	if err := bdpm.CheckPlausibility(ds, q, p.opts.Validation); err != nil {
		// Le Store en service n'est pas touché : l'API continue de répondre
		// avec les données de la veille (exigence E3).
		return finish(err)
	}

	rep.Counts = ds.Counts()
	rep.Quarantine = q.Total()
	rep.OutOfScope = q.OutOfScopeTotal()
	rep.RejectRatio = bdpm.RejectRatio(ds, q)
	rep.Warnings = bdpm.Warnings(q)
	if previous != nil {
		rep.Delta = deltaCounts(previous.Counts, rep.Counts)
	}

	if p.opts.DryRun {
		p.opts.Logger.Info("simulation : aucun snapshot écrit", "sync_id", id)
		return finish(nil)
	}

	// ⑦ Écriture atomique du snapshot.
	prevHash := ""
	if previous != nil {
		prevHash = previous.Hash
	}
	out, err := snapshot.Write(ds, q, snapshot.WriteOptions{
		DataDir: p.opts.DataDir, Generator: p.opts.Generator,
		Hash: res.Hash, PreviousHash: prevHash, Keep: p.opts.Keep,
	})
	if err != nil {
		return finish(fmt.Errorf("écriture du snapshot : %w", err))
	}

	// ⑧ Construction et ⑨ bascule.
	if p.opts.Holder != nil {
		s, err := store.LoadStore(out.Dir)
		if err != nil {
			return finish(fmt.Errorf("construction du Store : %w", err))
		}
		rep.StoreBuildMS = s.BuildDuration.Milliseconds()
		p.opts.Holder.Store(s)
		p.publishDataset(s)
	}

	p.opts.Logger.Info("synchronisation terminée",
		"sync_id", id, "hash", res.Hash, "changed", rep.Changed,
		"counts", rep.Counts, "delta", rep.Delta,
		"quarantine", rep.Quarantine, "out_of_scope", rep.OutOfScope,
		"store_build_ms", rep.StoreBuildMS,
		"duration_ms", time.Since(start).Milliseconds())

	return finish(nil)
}

func (p *Pipeline) currentManifest() *snapshot.Manifest {
	dir, err := snapshot.Current(p.opts.DataDir)
	if err != nil {
		return nil
	}
	m, err := snapshot.ReadManifest(dir)
	if err != nil {
		return nil
	}
	return m
}

// publishDataset met à jour les jauges depuis le manifest du Store publié.
//
// Le manifest, et non l'état d'ingestion en mémoire : c'est la seule source
// disponible dans les deux chemins — synchronisation et chargement au
// démarrage — et elle fait autorité sur ce qui est réellement servi.
func (p *Pipeline) publishDataset(s *store.Store) {
	if p.opts.Metrics == nil || s == nil || s.Manifest == nil {
		return
	}
	m := s.Manifest
	p.opts.Metrics.PublishDataset(DatasetSnapshot{
		Version:    m.Version,
		Hash:       m.Hash,
		Counts:     m.Counts,
		Quarantine: keyedCounts(m.Quarantine.ByFile, m.Quarantine.ByReason),
		OutOfScope: keyedCounts(m.OutOfScope.ByFile, m.OutOfScope.ByReason),
		BuildTime:  s.BuildDuration,
	})
}

// keyedCounts croise les compteurs par fichier et par motif en une carte à
// clé « fichier|motif ».
//
// La quarantaine ne conserve pas le couple exact (fichier, motif) mais deux
// ventilations séparées ; les recroiser exactement demanderait de garder
// toutes les entrées en mémoire, ce que le plafond de quarantaine interdit
// précisément. La clé produite ici associe donc chaque fichier à son motif
// dominant, ce qui suffit à l'usage : repérer *quel fichier* se dégrade et
// *pour quelle raison*, pas reconstituer la matrice complète.
func keyedCounts(byFile, byReason map[string]int) map[string]int {
	if len(byFile) == 0 {
		return nil
	}
	dominant := ""
	best := -1
	for reason, n := range byReason {
		if n > best || (n == best && reason < dominant) {
			dominant, best = reason, n
		}
	}
	out := make(map[string]int, len(byFile))
	for file, n := range byFile {
		out[file+"|"+dominant] = n
	}
	return out
}

// deltaCounts est la variation par entité vis-à-vis du snapshot précédent.
// C'est la donnée la plus utile en exploitation : elle répond d'un coup
// d'œil à « qu'est-ce qui a changé cette nuit ? ».
func deltaCounts(previous, current map[string]int) map[string]int {
	out := make(map[string]int, len(current))
	for k, v := range current {
		if prev, ok := previous[k]; ok && prev != v {
			out[k] = v - prev
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadCurrent charge le snapshot en service au démarrage, s'il existe.
//
// L'absence de snapshot n'est pas une erreur : c'est l'état normal d'un
// premier démarrage, où /readyz répond 503 le temps de la première
// synchronisation.
func (p *Pipeline) LoadCurrent() error {
	if err := snapshot.PurgeTemp(p.opts.DataDir); err != nil {
		p.opts.Logger.Warn("purge des résidus d'écriture impossible", "error", err)
	}
	dir, err := snapshot.Current(p.opts.DataDir)
	if err != nil {
		if errors.Is(err, snapshot.ErrNoSnapshot) {
			return nil
		}
		return err
	}
	s, err := store.LoadStore(dir)
	if err != nil {
		return err
	}
	if p.opts.Holder != nil {
		p.opts.Holder.Store(s)
	}
	// Les jauges sont publiées ici aussi : sans cela, un serveur redémarré
	// sur un snapshot existant n'exposerait ni volumétrie ni qualité
	// d'ingestion jusqu'à la synchronisation suivante — soit jusqu'à
	// vingt-quatre heures d'angle mort en supervision.
	p.publishDataset(s)

	p.opts.Logger.Info("snapshot chargé au démarrage",
		"version", s.Manifest.Version, "hash", s.Manifest.Hash,
		"build_ms", s.BuildDuration.Milliseconds(),
		"specialites", s.NbSpecialites())
	return nil
}

// syncID produit un identifiant de synchronisation trié dans le temps.
func syncID() string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var buf [10]byte
	binary.BigEndian.PutUint64(buf[:8], uint64(time.Now().UnixMilli()))
	_, _ = rand.Read(buf[8:])

	out := make([]byte, 16)
	for i := range out {
		out[i] = crockford[buf[i%10]%32]
	}
	return "sync_" + string(out)
}
