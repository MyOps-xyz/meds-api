// Commande bdpm-sync : synchronisation ponctuelle de la BDPM, hors serveur.
//
// Elle existe pour les déploiements où aucune sortie Internet n'est tolérée
// depuis un service exposé : un cron de l'hôte l'invoque, elle écrit un
// snapshot dans le volume partagé, et le serveur détecte le nouveau snapshot
// au redémarrage (docs/08-deploiement-docker.md §7, montage B).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/MyOps-xyz/meds-api/internal/bdpm"
	"github.com/MyOps-xyz/meds-api/internal/pipeline"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var (
		dataDir     = flag.String("data-dir", "/data", "répertoire de données")
		force       = flag.Bool("force", false, "réingérer même si la source est inchangée")
		dryRun      = flag.Bool("dry-run", false, "tout exécuter sauf l'écriture du snapshot")
		keep        = flag.Int("keep", 3, "nombre de snapshots conservés")
		jsonOut     = flag.Bool("json", false, "forcer la sortie JSON")
		showVersion = flag.Bool("version", false, "afficher la version")
		timeout     = flag.Duration("timeout", 10*time.Minute, "délai global")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("bdpm-sync %s (commit %s, construit le %s)\n", version, commit, date)
		if info, ok := debug.ReadBuildInfo(); ok {
			fmt.Printf("go %s\n", info.GoVersion)
		}
		return
	}

	// Sortie lisible en mode interactif, JSON sinon : un cron redirige vers
	// un fichier, un humain lit son terminal. Détecter le cas plutôt que
	// d'imposer un drapeau évite d'avoir à s'en souvenir.
	interactive := isTerminal(os.Stdout) && !*jsonOut

	var logOut io.Writer = os.Stderr
	log := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pipe := pipeline.New(pipeline.Options{
		DataDir:    *dataDir,
		Generator:  "bdpm-sync/" + version,
		Keep:       *keep,
		Logger:     log,
		Force:      *force,
		DryRun:     *dryRun,
		Validation: bdpm.DefaultValidationOptions(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	rep, err := pipe.Run(ctx)

	if interactive {
		printHuman(rep, err, *dryRun)
	} else {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	}

	// Code de sortie explicite : c'est ce qui rend la commande utilisable
	// dans un cron, où seul le code de retour est observé.
	if err != nil {
		os.Exit(1)
	}
}

func printHuman(rep pipeline.Report, err error, dryRun bool) {
	fmt.Printf("synchronisation %s\n", rep.SyncID)
	if err != nil {
		fmt.Printf("  échec : %v\n", err)
		return
	}
	if !rep.Changed {
		fmt.Printf("  source inchangée (hash %s) — aucune écriture\n", short(rep.Hash))
		return
	}

	fmt.Printf("  hash        : %s", short(rep.Hash))
	if rep.PreviousHash != "" {
		fmt.Printf(" (précédent %s)", short(rep.PreviousHash))
	}
	fmt.Println()
	fmt.Printf("  durée       : %d ms\n", rep.DurationMS)
	for _, entity := range []string{
		"specialites", "presentations", "composants", "substances",
		"groupes_generiques", "avis_smr", "avis_asmr", "conditions", "ruptures", "mitm",
	} {
		line := fmt.Sprintf("  %-18s: %d", entity, rep.Counts[entity])
		if d, ok := rep.Delta[entity]; ok {
			line += fmt.Sprintf("  (%+d)", d)
		}
		fmt.Println(line)
	}
	fmt.Printf("  rejets      : %d (%.4f %%)\n", rep.Quarantine, rep.RejectRatio*100)
	fmt.Printf("  hors périmètre : %d\n", rep.OutOfScope)
	for _, w := range rep.Warnings {
		fmt.Printf("  ! %s\n", w)
	}
	if dryRun {
		fmt.Println("  simulation  : aucun snapshot écrit")
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// isTerminal indique si f est un terminal. La détection repose sur le mode
// du fichier : un tube ou une redirection n'est pas un périphérique de
// caractères.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
