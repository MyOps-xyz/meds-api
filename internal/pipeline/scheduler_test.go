package pipeline

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/config"
)

func mustCron(t *testing.T, expr string) config.CronSchedule {
	t.Helper()
	c, err := config.ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q) : %v", expr, err)
	}
	return c
}

// Critère T-04/T-40 : la prochaine occurrence est calculée correctement,
// strictement après l'instant de référence.
func TestCronNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		{"0 4 * * *", "2026-08-15T03:59:00Z", "2026-08-15T04:00:00Z"},
		{"0 4 * * *", "2026-08-15T04:00:00Z", "2026-08-16T04:00:00Z"},
		{"0 4 * * *", "2026-08-15T04:00:30Z", "2026-08-16T04:00:00Z"},
		{"*/15 * * * *", "2026-08-15T10:07:00Z", "2026-08-15T10:15:00Z"},
		{"30 2 1 * *", "2026-08-15T10:00:00Z", "2026-09-01T02:30:00Z"},
		// 29 février : la recherche sur quatre ans doit le trouver.
		{"0 12 29 2 *", "2026-08-15T00:00:00Z", "2028-02-29T12:00:00Z"},
	}
	for _, c := range cases {
		from, err := time.Parse(time.RFC3339, c.from)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := mustCron(t, c.expr).Next(from)
		if !ok {
			t.Errorf("%s depuis %s : aucune occurrence", c.expr, c.from)
			continue
		}
		if got.UTC().Format(time.RFC3339) != c.want {
			t.Errorf("%s depuis %s → %s, veut %s",
				c.expr, c.from, got.UTC().Format(time.RFC3339), c.want)
		}
	}
}

// Sémantique cron historique : jour du mois **et** jour de semaine tous deux
// restreints ⇒ l'un ou l'autre suffit.
func TestCronNext_JourDuMoisOuJourDeSemaine(t *testing.T) {
	// Tous les 1ᵉʳ du mois et tous les lundis, à midi.
	c := mustCron(t, "0 12 1 * 1")

	// Le 1ᵉʳ septembre 2026 est un mardi : il doit tout de même déclencher.
	from := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC) // lundi 13 h
	got, ok := c.Next(from)
	if !ok {
		t.Fatal("aucune occurrence")
	}
	if want := "2026-09-01T12:00:00Z"; got.UTC().Format(time.RFC3339) != want {
		t.Errorf("Next = %s, veut %s (le 1ᵉʳ du mois déclenche même un mardi)",
			got.UTC().Format(time.RFC3339), want)
	}
}

func TestCronNext_ExpressionInsatisfiable(t *testing.T) {
	// Le 31 février n'existe pas.
	c := mustCron(t, "0 12 31 2 *")
	if _, ok := c.Next(time.Now()); ok {
		t.Error("une expression insatisfiable doit être signalée, pas boucler")
	}
}

func TestCronNext_Desactive(t *testing.T) {
	c := mustCron(t, "")
	if _, ok := c.Next(time.Now()); ok {
		t.Error("un ordonnanceur désactivé n'a pas d'occurrence")
	}
}

// Critère T-40 : `MEDS_SYNC_CRON` vide désactive proprement l'ordonnanceur.
func TestScheduler_DesactiveRendLaMain(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)
	s := NewScheduler(mustCron(t, ""), 0, p, slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan struct{})
	go func() { s.Run(context.Background()); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run n'a pas rendu la main sur une expression vide")
	}
}

// Critère T-40 : le cron déclenche à l'heure prévue, avec un décalage
// aléatoire compris dans la fenêtre de jitter.
func TestScheduler_DeclencheAvecJitterDansLaFenetre(t *testing.T) {
	fake := newFakeANSM()
	p, _, holder := testPipeline(t, fake.server(t), nil)

	var mu sync.Mutex
	var delays []time.Duration

	s := NewScheduler(mustCron(t, "0 4 * * *"), 30*time.Minute, p,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Horloge figée à 03:59:00 : la prochaine occurrence est à 04:00:00,
	// soit 60 s plus tard, plus le jitter.
	s.now = func() time.Time { return time.Date(2026, 8, 15, 3, 59, 0, 0, time.UTC) }
	// Jitter maximal, pour vérifier la borne haute.
	s.rand = func(n int64) int64 { return n - 1 }
	// Le délai est capturé puis court-circuité : le test n'attend rien.
	fired := make(chan struct{}, 4)
	s.after = func(d time.Duration) <-chan time.Time {
		mu.Lock()
		delays = append(delays, d)
		mu.Unlock()
		select {
		case fired <- struct{}{}:
		default:
		}
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("l'ordonnanceur n'a jamais planifié de créneau")
	}
	// Laisser une synchronisation aboutir avant d'arrêter.
	deadline := time.Now().Add(5 * time.Second)
	for holder.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(delays) == 0 {
		t.Fatal("aucun délai planifié")
	}
	d := delays[0]
	// 60 s jusqu'à l'occurrence, plus un jitter strictement inférieur à
	// 30 min.
	if d < 60*time.Second || d >= 60*time.Second+30*time.Minute {
		t.Errorf("délai = %v, veut [60s, 60s+30min[", d)
	}
	if holder.Load() == nil {
		t.Error("le créneau n'a déclenché aucune synchronisation")
	}
}

func TestScheduler_SansJitter(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)

	s := NewScheduler(mustCron(t, "0 4 * * *"), 0, p,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return time.Date(2026, 8, 15, 3, 59, 0, 0, time.UTC) }

	got := make(chan time.Duration, 1)
	s.after = func(d time.Duration) <-chan time.Time {
		select {
		case got <- d:
		default:
		}
		ch := make(chan time.Time)
		return ch // ne se déclenche jamais : on ne teste que la planification
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	select {
	case d := <-got:
		if d != 60*time.Second {
			t.Errorf("délai = %v, veut exactement 60 s sans jitter", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aucun créneau planifié")
	}
}

// Le contexte annulé arrête l'ordonnanceur pendant l'attente.
func TestScheduler_ArretPendantLAttente(t *testing.T) {
	fake := newFakeANSM()
	p, _, _ := testPipeline(t, fake.server(t), nil)

	s := NewScheduler(mustCron(t, "0 4 * * *"), 0, p,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.after = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("l'ordonnanceur ne s'est pas arrêté sur annulation du contexte")
	}
}
