package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/config"
)

// Scheduler déclenche les synchronisations selon une expression cron.
type Scheduler struct {
	cron   config.CronSchedule
	jitter time.Duration
	pipe   *Pipeline
	log    *slog.Logger

	// now et sleep sont injectables pour les tests, qui ne peuvent pas
	// attendre quatre heures.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time
	rand  func(int64) int64
}

// NewScheduler construit un ordonnanceur.
func NewScheduler(cron config.CronSchedule, jitter time.Duration, p *Pipeline, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron: cron, jitter: jitter, pipe: p, log: log,
		now:   time.Now,
		after: time.After,
		rand:  rand.Int63n,
	}
}

// Run fait tourner l'ordonnanceur jusqu'à annulation du contexte.
//
// Une expression vide **désactive** proprement l'ordonnanceur : Run rend la
// main immédiatement. C'est un état valide, et non une erreur — c'est le
// réglage des instances secondaires d'un déploiement à volume partagé, où
// une seule instance synchronise.
func (s *Scheduler) Run(ctx context.Context) {
	if !s.cron.Enabled {
		s.log.Info("ordonnanceur désactivé (MEDS_SYNC_CRON vide)")
		return
	}

	for {
		now := s.now()
		next, ok := s.cron.Next(now)
		if !ok {
			s.log.Error("expression cron sans occurrence dans les quatre ans à venir",
				"cron", s.cron.Raw)
			return
		}

		// Le décalage aléatoire est une **courtoisie envers l'ANSM** : sans
		// lui, toutes les instances déployées de cette API taperaient à la
		// même seconde, transformant un service public en cible d'une charge
		// synchronisée.
		// L'écart est calculé depuis `now`, et non par time.Until : mélanger
		// l'horloge injectée et l'horloge réelle produirait un délai faux dès
		// que les deux divergent — ce qui est exactement le cas sous test, et
		// le serait aussi le jour où l'on voudrait rejouer un ordonnancement.
		delay := next.Sub(now)
		if s.jitter > 0 {
			delay += time.Duration(s.rand(int64(s.jitter)))
		}
		if delay < 0 {
			delay = 0
		}

		s.log.Info("prochaine synchronisation planifiée",
			"at", next.Format(time.RFC3339), "delay", delay.Round(time.Second).String())

		select {
		case <-ctx.Done():
			return
		case <-s.after(delay):
		}

		// Un déclenchement pendant qu'une synchronisation manuelle est en
		// cours est ignoré plutôt que mis en file : la suivante aura lieu au
		// prochain créneau, et empiler des ingestions n'a aucun intérêt.
		if _, err := s.pipe.Run(ctx); err != nil {
			// errors.Is et non == : le jour où Run enveloppera cette erreur,
			// une comparaison directe classerait un créneau simplement occupé
			// comme un échec de synchronisation, et déclencherait une alerte
			// pour un fonctionnement normal.
			if errors.Is(err, ErrInProgress) {
				s.log.Warn("créneau ignoré : une synchronisation était déjà en cours")
			} else {
				s.log.Error("synchronisation planifiée en échec", "error", err)
			}
		}
	}
}
